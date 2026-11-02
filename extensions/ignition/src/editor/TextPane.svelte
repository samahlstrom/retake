<script module lang="ts">
  /** What the editor asks of the text. */
  export interface PaneApi {
    /** Shows a line (from 0) and puts the caret at its start. */
    reveal(line: number): void;
    /** Where the caret is, or null while the person hasn't put it anywhere. */
    caret(): number | null;
  }
</script>

<script lang="ts">
  // The text: a plain textarea, with a mirror of it on top that draws the same lines with the headings bold. The mirror
  // lets every click through; the textarea's text is transparent under it, so the caret, the selection and the
  // spell-check underlines are the textarea's own, and wrapping is the same in both (a heading is thickened with a
  // stroke, never a wider font, so it wraps where the textarea does). The pane grows with its text; the editor's
  // scroller around it scrolls, and keeps the caret's line in view above a phone's keyboard.
  import { onMount, untrack } from 'svelte';
  import { headingOf, keepLine, lineOf, lineStart, mapCaret } from '../lib/doc';
  import type { Doc } from './doc.svelte';

  let {
    doc,
    readonly,
    caret,
    focus,
    scroller,
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
    api?: PaneApi;
  } = $props();

  /** An empty line's content in the mirror: it gives the line its height and no width. */
  const ZWSP = '​';

  let ta: HTMLTextAreaElement;
  let mirror: HTMLDivElement;
  let ready = false;
  /** The person put the caret somewhere (or it was given): a talk-out goes there. */
  let placed = untrack(() => caret !== null);
  let flash = $state(-1);
  let flashTimer: ReturnType<typeof setTimeout> | null = null;

  interface Line {
    text: string;
    level: number;
    hashes: string;
    rest: string;
  }

  const lines = $derived(
    doc.text.split('\n').map((text): Line => {
      const h = headingOf(text);
      if (!h) return { text, level: 0, hashes: '', rest: '' };
      const m = /^(#{1,6})(.*)$/.exec(text)!;
      return { text, level: h.level, hashes: m[1]!, rest: m[2]! };
    }),
  );

  const lineEl = (line: number): HTMLElement | undefined => mirror?.children[line] as HTMLElement | undefined;
  const coarse = (): boolean => typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches;

  /** Keeps the caret's line in view: inside the scroller, and above an on-screen keyboard (the visual viewport). */
  function keepCaret(): void {
    if (!scroller || document.activeElement !== ta) return;
    const el = lineEl(lineOf(doc.text, ta.selectionEnd));
    if (!el) return;
    const r = el.getBoundingClientRect(),
      box = scroller.getBoundingClientRect();
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

  api = {
    reveal(line: number) {
      const l = Math.max(0, Math.min(line, doc.text.split('\n').length - 1));
      const pos = lineStart(doc.text, l);
      ta.setSelectionRange(pos, pos);
      placed = true;
      // A phone keeps its keyboard down: the caret waits there for the next tap or talk-out.
      if (!readonly && !coarse()) ta.focus({ preventScroll: true });
      lineEl(l)?.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
      flash = l;
      if (flashTimer) clearTimeout(flashTimer);
      flashTimer = setTimeout(() => (flash = -1), 1600);
    },
    caret: () => (placed && ta ? ta.selectionStart : null),
  };

  onMount(() => {
    ta.value = doc.text;
    const c = Math.min(Math.max(0, caret ?? doc.text.length), doc.text.length);
    ta.setSelectionRange(c, c);
    if (focus && !readonly) ta.focus({ preventScroll: true });
    if (caret !== null)
      requestAnimationFrame(() => lineEl(lineOf(doc.text, c))?.scrollIntoView?.({ block: 'nearest' }));
    ready = true;
    const sel = () => {
      if (document.activeElement === ta) keepSoon();
    };
    document.addEventListener('selectionchange', sel);
    const vv = window.visualViewport;
    vv?.addEventListener('resize', keepSoon);
    vv?.addEventListener('scroll', keepSoon);
    return () => {
      document.removeEventListener('selectionchange', sel);
      vv?.removeEventListener('resize', keepSoon);
      vv?.removeEventListener('scroll', keepSoon);
      if (flashTimer) clearTimeout(flashTimer);
    };
  });

  // A change from outside the textarea (a reload, a talk-out): show it and keep the caret in its place.
  $effect(() => {
    const x = doc.external;
    untrack(() => {
      if (!ready || !x || ta.value === doc.text) return;
      const s = ta.selectionStart,
        e = ta.selectionEnd;
      let ns: number, ne: number;
      if (x.kind === 'splice') {
        ns = x.caret ?? mapCaret(s, x.splice);
        ne = x.caret ?? mapCaret(e, x.splice);
      } else {
        ns = ne = keepLine(ta.value, s, doc.text);
      }
      ta.value = doc.text;
      ta.setSelectionRange(ns, ne);
    });
  });

  function keydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      // Esc leaves the text, so R talks again.
      e.preventDefault();
      ta.blur();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      void doc.flush();
    }
  }

  const touched = (): void => {
    placed = true;
  };
</script>

<div class="ig-paper">
  <textarea
    bind:this={ta}
    class="ig-text"
    data-part="ignition-text"
    aria-label="The document"
    rows="1"
    spellcheck="true"
    autocapitalize="sentences"
    {readonly}
    oninput={() => {
      placed = true;
      doc.input(ta.value);
      keepSoon();
    }}
    onkeydown={keydown}
    onkeyup={touched}
    onpointerdown={touched}
    onfocus={() => {
      touched();
      keepSoon();
    }}
    onblur={() => void doc.flush()}></textarea>
  <div class="ig-mirror" bind:this={mirror} aria-hidden="true">
    {#each lines as l, i (i)}
      <div class={['ig-ln', l.level && `ig-h ig-lv${l.level}`, flash === i && 'ig-flash']}>
        {#if l.level}<span class="ig-hm">{l.hashes}</span>{l.rest}{:else}{l.text || ZWSP}{/if}
      </div>
    {/each}
  </div>
</div>
