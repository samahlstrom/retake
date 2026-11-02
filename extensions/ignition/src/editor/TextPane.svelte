<script module lang="ts">
  /** What the editor asks of the text. */
  export interface PaneApi {
    /** Shows a line (from 0), puts the caret at its start and lights it up for a moment. */
    reveal(line: number): void;
    /** Where the caret is (an offset in the document's text), or null while the person hasn't put it anywhere. */
    caret(): number | null;
  }
</script>

<script lang="ts">
  // The text: CodeMirror editing the document's raw Markdown, drawn as a live preview (live.ts). The pane grows with its
  // text; the editor's scroller around it scrolls, and keeps the caret's line in view above a phone's keyboard. A change
  // from outside (a reload, the agent, a talk-out) goes in as the smallest edit (lib/diff.ts), so the caret stays on
  // its word and the editor's own undo stays the person's typing.
  import { onMount, untrack } from 'svelte';
  import { EditorView, type ViewUpdate } from '@codemirror/view';
  import { Transaction } from '@codemirror/state';
  import { changes } from '../lib/diff';
  import { imageMarkdown, isImage } from '../lib/links';
  import { editorState, isCRLF, offsetOf, outside, posOf } from './setup';
  import { setFlash, setQuestions, type Question } from './live';
  import type { Doc } from './doc.svelte';

  let {
    doc,
    readonly,
    caret,
    focus,
    scroller,
    questions = [],
    image,
    answer,
    openQuestion,
    saveImage,
    api = $bindable(),
  }: {
    doc: Doc;
    readonly: boolean;
    /** Where the caret starts, or null: none yet (a talk-out then goes at the end). */
    caret: number | null;
    /** Put the focus in the text when it shows. */
    focus: boolean;
    /** The element that scrolls the text. */
    scroller: HTMLElement | undefined;
    /** The agent's open questions about this document, shown under their places. */
    questions?: Question[];
    /** An image's address from its source in the document, or null. */
    image(src: string): string | null;
    answer(q: Question, text: string): void;
    openQuestion(q: Question): void;
    /** Keeps a pasted or dropped image in v0/images/ and gives its name there. */
    saveImage(file: File): Promise<string>;
    api?: PaneApi;
  } = $props();

  let host: HTMLDivElement;
  let view: EditorView | undefined;
  /** The person put the caret somewhere (or it was given): a talk-out goes there. */
  let placed = untrack(() => caret !== null);
  let flashTimer: ReturnType<typeof setTimeout> | null = null;
  /** The document breaks its lines with CR LF: the editor's positions count each break as one character. */
  let crlf = false;

  const coarse = (): boolean => typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches;
  /** An offset in the document's text, as a position in the editor, and back. */
  const toPos = (off: number): number => (crlf ? posOf(doc.text, off) : off);
  const toOffset = (pos: number): number => (view ? offsetOf(view.state, pos) : pos);
  /** The editor's text as the file holds it. */
  const raw = (): string => view?.state.sliceDoc() ?? '';

  /** Keeps the caret's line in view: inside the scroller, and above an on-screen keyboard (the visual viewport). */
  function keepCaret(): void {
    if (!view || !scroller || !view.hasFocus) return;
    const r = view.coordsAtPos(view.state.selection.main.head);
    if (!r) return;
    const box = scroller.getBoundingClientRect();
    const vv = window.visualViewport;
    const bottom = Math.min(box.bottom, vv ? vv.offsetTop + vv.height : box.bottom) - 24;
    const top = box.top + 64;
    if (r.bottom > bottom) scroller.scrollTop += r.bottom - bottom;
    else if (r.top < top) scroller.scrollTop -= top - r.top;
  }
  let keepQueued = false;
  function keepSoon(): void {
    if (keepQueued) return;
    keepQueued = true;
    requestAnimationFrame(() => {
      keepQueued = false;
      keepCaret();
    });
  }

  function update(u: ViewUpdate): void {
    const fromOutside = u.transactions.some((t) => t.annotation(outside));
    if (u.docChanged && !fromOutside) {
      placed = true;
      doc.input(raw());
    }
    if (u.transactions.some((t) => t.isUserEvent('select'))) placed = true;
    if (u.focusChanged) {
      if (u.view.hasFocus) placed = true;
      else void doc.flush();
    }
    if (u.view.hasFocus && (u.selectionSet || u.docChanged || u.focusChanged)) keepSoon();
  }

  // ---------------------------------------------------------------- images: paste and drop
  async function addImages(list: File[], at: number | null): Promise<void> {
    const v = view;
    if (!v) return;
    const before = v.state.doc;
    const names: string[] = [];
    for (const f of list) {
      try {
        names.push(await saveImage(f));
      } catch {
        /* saveImage says why */
      }
    }
    if (!names.length || view !== v) return;
    const pos = at !== null && v.state.doc === before ? at : v.state.selection.main.head;
    const line = v.state.doc.lineAt(pos);
    const br = v.state.lineBreak;
    const md = names.map(imageMarkdown).join(br + br);
    // An image is a block of its own: on its own line, with a blank line around it.
    const d = v.state.doc;
    const blankAt = (n: number): boolean => n < 1 || n > d.lines || !d.line(n).text.trim();
    const lead = line.text.slice(0, pos - line.from).trim() ? br + br : blankAt(line.number - 1) ? '' : br;
    const tail = line.text.slice(pos - line.from).trim()
      ? br + br
      : line.number === d.lines || !blankAt(line.number + 1)
        ? br
        : '';
    const insert = `${lead}${md}${tail}`;
    v.dispatch({
      changes: { from: pos, insert },
      selection: { anchor: pos + v.state.toText(lead + md).length },
      userEvent: 'input.paste',
      scrollIntoView: true,
    });
  }

  const imagesOf = (data: DataTransfer | null): File[] => [...(data?.files ?? [])].filter(isImage);

  const imageEvents = EditorView.domEventHandlers({
    paste(e) {
      const list = imagesOf(e.clipboardData);
      if (!list.length || readonly) return false;
      e.preventDefault();
      void addImages(list, null);
      return true;
    },
    drop(e, v) {
      const list = imagesOf(e.dataTransfer);
      if (!list.length || readonly) return false;
      e.preventDefault();
      void addImages(list, v.posAtCoords({ x: e.clientX, y: e.clientY }));
      return true;
    },
  });

  // ---------------------------------------------------------------- the API, and the editor's life
  api = {
    reveal(line: number) {
      const v = view;
      if (!v) return;
      const n = Math.max(1, Math.min(line + 1, v.state.doc.lines));
      const pos = v.state.doc.line(n).from;
      placed = true;
      v.dispatch({
        selection: { anchor: pos },
        effects: [EditorView.scrollIntoView(pos, { y: 'center' }), setFlash.of(pos)],
      });
      // A phone keeps its keyboard down: the caret waits there for the next tap or talk-out.
      if (!readonly && !coarse()) v.focus();
      if (flashTimer) clearTimeout(flashTimer);
      flashTimer = setTimeout(() => view?.dispatch({ effects: setFlash.of(null) }), 1600);
    },
    caret: () => (placed && view ? toOffset(view.state.selection.main.head) : null),
  };

  function create(text: string, at: number): void {
    crlf = isCRLF(text);
    const state = editorState({
      text,
      caret: toPos(at),
      readonly,
      save: () => void doc.flush(),
      update,
      image,
      answer,
      openQuestion,
      extra: imageEvents,
    });
    if (view) view.setState(state);
    else view = new EditorView({ state, parent: host });
    // The text as the file holds it, and the caret: for tests and for whoever inspects the page.
    Object.defineProperty(view.contentDOM, 'ignition', {
      configurable: true,
      value: { text: raw, caret: () => toOffset(view?.state.selection.main.head ?? 0) },
    });
  }

  onMount(() => {
    const at = Math.min(Math.max(0, caret ?? doc.text.length), doc.text.length);
    create(doc.text, at);
    const v = view!;
    // The document opens at its top: the focus doesn't scroll to the caret.
    if (focus && !readonly) v.contentDOM.focus({ preventScroll: true });
    if (caret !== null) v.dispatch({ effects: EditorView.scrollIntoView(toPos(at), { y: 'nearest' }) });
    const vv = window.visualViewport;
    vv?.addEventListener('resize', keepSoon);
    vv?.addEventListener('scroll', keepSoon);
    return () => {
      vv?.removeEventListener('resize', keepSoon);
      vv?.removeEventListener('scroll', keepSoon);
      if (flashTimer) clearTimeout(flashTimer);
      view?.destroy();
      view = undefined;
    };
  });

  // A change from outside the editor (a reload, a talk-out): put it in as the smallest edit, keeping the caret.
  $effect(() => {
    const x = doc.external;
    untrack(() => {
      const v = view;
      if (!v || !x) return;
      const next = doc.text;
      const now = v.state.sliceDoc();
      if (now === next) return;
      if (isCRLF(next) !== crlf) {
        // The line breaks themselves changed: a new editor state (its undo starts again).
        const off = toOffset(v.state.selection.main.head);
        create(next, Math.min(off, next.length));
        return;
      }
      // The editor counts a line break as one character: diff in LF, and give it its own breaks back.
      const norm = (s: string): string => (crlf ? s.replace(/\r\n/g, '\n') : s);
      let cs = changes(norm(now), norm(next));
      if (crlf) cs = cs.map((c) => ({ ...c, insert: c.insert.replace(/\n/g, '\r\n') }));
      const sel = x.kind === 'splice' && x.caret !== undefined ? { anchor: toPos(x.caret) } : undefined;
      v.dispatch({
        changes: cs,
        selection: sel,
        annotations: [outside.of(true), Transaction.addToHistory.of(false)],
      });
    });
  });

  $effect(() => {
    const qs = questions;
    untrack(() => view?.dispatch({ effects: setQuestions.of(qs) }));
  });
</script>

<div class="ig-paper" bind:this={host}></div>
