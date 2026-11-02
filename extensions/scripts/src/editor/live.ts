// The live preview (spec/scripts.md, "The editor"): the document is edited as the Markdown it is, and drawn as it reads.
// Headings at their sizes, bold, italic, code, lists with a hanging indent, task boxes you tap, links you tap, quotes,
// rules and images. The Markdown's own characters (#, **, `, list marks, >, a link's brackets and address) are hidden,
// except on the lines the caret or the selection is on while the text has the focus, where they show to be edited.
// Nothing here changes the text: decorations only draw it (a task box's tap is the one edit, `[ ]` to `[x]` and back).
// The agent's open questions about the document show under their place, with their choices to tap. A web address alone
// on its line, or one raw <iframe>, shows as an embed under it (spec/scripts.md, "Embeds"; lib/embed.ts).
import {
  Facet,
  Prec,
  StateEffect,
  StateField,
  type EditorState,
  type Extension,
  type Line,
  type Range,
} from '@codemirror/state';
import { Decoration, EditorView, keymap, WidgetType, type DecorationSet } from '@codemirror/view';
import { syntaxTree } from '@codemirror/language';
import type { SyntaxNode, Tree } from '@lezer/common';
import { findAnchor, paragraph } from '../lib/doc';
import { cardFallback, embedOfLine, FRAME_ATTRS, type Embed } from '../lib/embed';
import { safeHref } from '../lib/links';
import type { LinkPreview } from '../types';

/** An open question of the agent's about a place in this document, as the preview shows it. */
export interface Question {
  /** Its thread id (what an answer names). */
  id: number;
  /** The comment it's under. */
  note: number;
  text: string;
  anchor: { line: number; heading?: string; quote?: string };
  options: { text: string; image?: string }[];
}

export interface LiveConfig {
  /** The address of an image the document names (`![](images/a.png)`), or null when it can't show. */
  image?(src: string): string | null;
  /** A viewer: task boxes and choices don't change anything. */
  readonly?: boolean;
  /** A choice of a question was tapped. */
  answer?(q: Question, text: string): void;
  /** "Answer in the comments": a question without choices. */
  openQuestion?(q: Question): void;
  /** This page's host name: Twitch's `parent`, and the one host a raw frame may not show. */
  page?: string;
  /** A link card's preview (ctx.linkPreview); without it a card shows its address. */
  preview?(url: string): Promise<LinkPreview>;
}

export const liveConfig = Facet.define<LiveConfig, LiveConfig>({ combine: (v) => v[0] ?? {} });

// ---------------------------------------------------------------- what the preview depends on besides the text

/** The text got or lost the focus: the caret's lines show their Markdown only while it has it. */
export const setFocus = StateEffect.define<boolean>();
const focused = StateField.define<boolean>({
  create: () => false,
  update(v, tr) {
    for (const e of tr.effects) if (e.is(setFocus)) v = e.value;
    return v;
  },
});

/** Lights up a line for a moment (a revealed place): a position on it, or null to stop. */
export const setFlash = StateEffect.define<number | null>();
const flash = StateField.define<number | null>({
  create: () => null,
  update(v, tr) {
    if (v !== null && tr.docChanged) v = tr.changes.mapPos(v);
    for (const e of tr.effects) if (e.is(setFlash)) v = e.value;
    return v;
  },
});

/** The agent's open questions about this document. */
export const setQuestions = StateEffect.define<Question[]>();
const questions = StateField.define<Question[]>({
  create: () => [],
  update(v, tr) {
    for (const e of tr.effects) if (e.is(setQuestions)) v = e.value;
    return v;
  },
});

/** The lines (from 1) whose Markdown shows: those the selection touches, as [first, last], while the text has the
 * focus; none without it. */
export function activeLines(state: EditorState): [number, number][] {
  if (!state.field(focused, false)) return [];
  return state.selection.ranges.map((r) => [state.doc.lineAt(r.from).number, state.doc.lineAt(r.to).number]);
}

const lineActive = (state: EditorState, line: number): boolean =>
  activeLines(state).some(([a, b]) => line >= a && line <= b);

// ---------------------------------------------------------------- widgets

/** A list item's mark, off the caret's line: a bullet, or its number, in the hanging indent. */
class ListMark extends WidgetType {
  constructor(
    readonly text: string,
    readonly ordered: boolean,
  ) {
    super();
  }
  eq(o: ListMark): boolean {
    return o.text === this.text && o.ordered === this.ordered;
  }
  toDOM(): HTMLElement {
    const s = document.createElement('span');
    s.className = this.ordered ? 'ig-lm ig-lm-ol' : 'ig-lm';
    s.textContent = this.text;
    if (!this.ordered) s.setAttribute('aria-hidden', 'true');
    return s;
  }
  ignoreEvent(): boolean {
    return false;
  }
}

/** Where the `[ ]` of a task on a line is (its first character), or null. */
function taskBox(state: EditorState, pos: number): { at: number; checked: boolean } | null {
  const line = state.doc.lineAt(pos);
  const m = /^((?:[ \t]*>[ \t]?)*[ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+\[)([ xX])\]/.exec(line.text);
  if (!m) return null;
  return { at: line.from + m[1]!.length - 1, checked: m[2] !== ' ' };
}

/** A tap on the task box of the line at `pos`: the one character between its brackets, ` ` to `x` and back. */
export function toggleTask(state: EditorState, pos: number): { from: number; to: number; insert: string } | null {
  const t = taskBox(state, pos);
  return t ? { from: t.at + 1, to: t.at + 2, insert: t.checked ? ' ' : 'x' } : null;
}

const CHECK_SVG =
  '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" ' +
  'stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';

/** A tap that didn't move (a finger that scrolls never taps). */
function onTap(el: HTMLElement, fn: (e: Event) => void): void {
  let start: { x: number; y: number } | null = null;
  el.addEventListener('mousedown', (e) => e.preventDefault());
  el.addEventListener(
    'touchstart',
    (e) => {
      const t = e.touches[0];
      start = t ? { x: t.clientX, y: t.clientY } : null;
    },
    { passive: true },
  );
  el.addEventListener('touchend', (e) => {
    const t = e.changedTouches[0];
    const s = start;
    start = null;
    if (!s || !t || Math.hypot(t.clientX - s.x, t.clientY - s.y) > 10) return;
    e.preventDefault(); // no caret, no keyboard, no click after it
    fn(e);
  });
  el.addEventListener('click', (e) => {
    e.preventDefault();
    fn(e);
  });
}

/** A task's box, off the caret's line (in the hanging indent): a tap ticks it in the text. */
class TaskBox extends WidgetType {
  constructor(
    readonly checked: boolean,
    readonly readonly: boolean,
  ) {
    super();
  }
  eq(o: TaskBox): boolean {
    return o.checked === this.checked && o.readonly === this.readonly;
  }
  toDOM(view: EditorView): HTMLElement {
    const s = document.createElement('span');
    s.className = this.checked ? 'ig-lm ig-task ig-on' : 'ig-lm ig-task';
    s.dataset.part = 'scripts-task';
    s.setAttribute('role', 'checkbox');
    s.setAttribute('aria-checked', String(this.checked));
    s.setAttribute('aria-label', this.checked ? 'Done' : 'Not done');
    const box = document.createElement('span');
    box.className = 'ig-box';
    box.innerHTML = CHECK_SVG;
    s.append(box);
    if (!this.readonly)
      onTap(s, () => {
        const c = toggleTask(view.state, view.posAtDOM(s));
        if (c) view.dispatch({ changes: c, userEvent: 'input.task' });
      });
    return s;
  }
  ignoreEvent(): boolean {
    return true;
  }
}

/** An image the document names: in its line's place off the caret's line, under the line on it. */
class Picture extends WidgetType {
  constructor(
    readonly url: string,
    readonly caption: string,
    readonly below: boolean,
  ) {
    super();
  }
  eq(o: Picture): boolean {
    return o.url === this.url && o.caption === this.caption && o.below === this.below;
  }
  toDOM(view: EditorView): HTMLElement {
    const fig = document.createElement(this.below ? 'div' : 'span');
    fig.className = this.below ? 'ig-fig ig-fig-below' : 'ig-fig';
    fig.dataset.part = 'scripts-image';
    const cap = document.createElement('span');
    cap.className = 'ig-cap';
    cap.textContent = this.caption;
    const img = document.createElement('img');
    img.alt = this.caption;
    img.decoding = 'async';
    img.draggable = false;
    img.addEventListener('load', () => view.requestMeasure());
    img.addEventListener('error', () => {
      fig.classList.add('ig-fig-missing');
      cap.textContent = this.caption ? `${this.caption} (the image can’t load)` : 'The image can’t load';
      view.requestMeasure();
    });
    img.src = this.url;
    fig.append(img, cap);
    return fig;
  }
  get estimatedHeight(): number {
    return this.below ? 240 : -1;
  }
  ignoreEvent(): boolean {
    return false;
  }
}

/** The agent's open question about a place, under it: its words and its choices (pictures side by side). */
class Ask extends WidgetType {
  constructor(
    readonly q: Question,
    readonly cfg: LiveConfig,
  ) {
    super();
  }
  eq(o: Ask): boolean {
    return (
      o.q.id === this.q.id &&
      o.q.text === this.q.text &&
      o.cfg === this.cfg &&
      JSON.stringify(o.q.options) === JSON.stringify(this.q.options)
    );
  }
  toDOM(view: EditorView): HTMLElement {
    const outer = document.createElement('div');
    outer.className = 'ig-ask';
    outer.dataset.part = 'scripts-question';
    outer.dataset.ask = String(this.q.id);
    const box = document.createElement('div');
    box.className = 'ig-ask-card';
    outer.append(box);
    const label = document.createElement('span');
    label.className = 'ig-ask-label';
    label.textContent = 'Question';
    const text = document.createElement('p');
    text.className = 'ig-ask-text';
    text.textContent = this.q.text;
    box.append(label, text);
    const opts = this.q.options;
    if (opts.length) {
      const row = document.createElement('div');
      row.className = opts.some((o) => o.image) ? 'ig-ask-opts ig-ask-pics' : 'ig-ask-opts';
      for (const o of opts) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'ig-ask-opt';
        b.dataset.part = 'scripts-choice';
        b.disabled = !!this.cfg.readonly || !this.cfg.answer;
        if (o.image) {
          const img = document.createElement('img');
          img.alt = '';
          img.decoding = 'async';
          img.addEventListener('load', () => view.requestMeasure());
          img.src = o.image;
          b.append(img);
        }
        const t = document.createElement('span');
        t.textContent = o.text;
        b.append(t);
        b.addEventListener('mousedown', (e) => e.preventDefault());
        b.addEventListener('click', () => {
          for (const x of row.querySelectorAll('button')) x.disabled = true;
          b.classList.add('ig-on');
          this.cfg.answer?.(this.q, o.text);
        });
        row.append(b);
      }
      box.append(row);
    } else if (this.cfg.openQuestion) {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'ig-link ig-ask-reply';
      b.textContent = 'Answer in the comments';
      b.addEventListener('mousedown', (e) => e.preventDefault());
      b.addEventListener('click', () => this.cfg.openQuestion?.(this.q));
      box.append(b);
    }
    return outer;
  }
  get estimatedHeight(): number {
    return this.q.options.some((o) => o.image) ? 320 : 120;
  }
  ignoreEvent(): boolean {
    return true;
  }
}

/** Link cards' previews asked for already, by address: one request each while the page is open. */
const previews = new Map<string, Promise<LinkPreview | null>>();
function previewOf(cfg: LiveConfig, url: string): Promise<LinkPreview | null> {
  let p = previews.get(url);
  if (!p) {
    p = cfg.preview ? cfg.preview(url).catch(() => null) : Promise.resolve(null);
    previews.set(url, p);
  }
  return p;
}

/** An embed under its line: a frame in a box kept at its size from the start, or a link card of a fixed height. The
 * same element stays while the caret comes and goes (`eq`), so a playing video keeps playing. */
class EmbedView extends WidgetType {
  constructor(
    readonly e: Embed,
    readonly cfg: LiveConfig,
  ) {
    super();
  }
  eq(o: EmbedView): boolean {
    return JSON.stringify(o.e) === JSON.stringify(this.e);
  }
  toDOM(): HTMLElement {
    const outer = document.createElement('div');
    outer.className = 'ig-embed';
    const e = this.e;
    if (e.kind === 'frame') {
      outer.dataset.part = 'scripts-embed';
      outer.dataset.site = e.site;
      if (e.maxWidth) outer.style.maxWidth = `${e.maxWidth}px`;
      const box = document.createElement('div');
      box.className = 'ig-embed-box';
      if (e.ratio) box.style.aspectRatio = String(e.ratio);
      else box.style.height = `${e.height ?? 360}px`;
      const frame = document.createElement('iframe');
      for (const [k, v] of Object.entries(FRAME_ATTRS)) frame.setAttribute(k, v); // the sandbox before the address
      frame.title = e.title;
      frame.src = e.src;
      box.append(frame);
      outer.append(box);
      return outer;
    }
    outer.classList.add('ig-embed-card');
    outer.dataset.part = 'scripts-card';
    const a = document.createElement('a');
    a.className = 'ig-card';
    a.href = e.url;
    a.target = '_blank';
    a.rel = 'noopener noreferrer';
    const text = document.createElement('span');
    text.className = 'ig-card-text';
    const title = document.createElement('span');
    title.className = 'ig-card-title';
    const site = document.createElement('span');
    site.className = 'ig-card-site';
    const fb = cardFallback(e.url);
    title.textContent = fb.title;
    site.textContent = fb.site;
    text.append(title, site);
    a.append(text);
    onTap(a, () => openLink(e.url));
    outer.append(a);
    void previewOf(this.cfg, e.url).then((p) => {
      if (!p) return;
      if (p.title) title.textContent = p.title;
      if (p.site) site.textContent = p.site;
      outer.dataset.loaded = 'true';
      if (!p.image) return;
      const pic = document.createElement('span');
      pic.className = 'ig-card-pic';
      const img = document.createElement('img');
      img.alt = '';
      img.decoding = 'async';
      img.referrerPolicy = 'no-referrer';
      img.draggable = false;
      img.addEventListener('error', () => pic.remove());
      img.src = p.image;
      pic.append(img);
      a.append(pic);
    });
    return outer;
  }
  get estimatedHeight(): number {
    return this.e.kind === 'frame' ? (this.e.height ?? 380) : 92;
  }
  ignoreEvent(): boolean {
    return true;
  }
}

/** What a line embeds (an address alone on it, or one raw frame, outside code), or null. */
function embedAt(l: Line, tree: Tree, cfg: LiveConfig): Embed | null {
  if (l.length > MAX_EMBED_LINE || !/^\s*(<?https?:\/\/|<iframe\b)/i.test(l.text)) return null;
  const e = embedOfLine(l.text, cfg.page ?? '');
  return e && !inCode(tree, l.from + (l.text.length - l.text.trimStart().length)) ? e : null;
}

/** ArrowDown and ArrowUp onto an embed's folded line put the caret on it, where the address shows to be edited (the
 * line has no height of its own to land on). */
function stepOnto(dir: 1 | -1) {
  return (view: EditorView): boolean => {
    const s = view.state;
    if (s.selection.ranges.length !== 1 || !s.selection.main.empty) return false;
    const n = s.doc.lineAt(s.selection.main.head).number + dir;
    if (n < 1 || n > s.doc.lines) return false;
    const l = s.doc.line(n);
    if (!embedAt(l, syntaxTree(s), s.facet(liveConfig))) return false;
    view.dispatch({ selection: { anchor: dir > 0 ? l.from : l.to }, scrollIntoView: true, userEvent: 'select' });
    return true;
  };
}

/** Whether the position is in code or a comment, where an address is only text. */
function inCode(tree: Tree, pos: number): boolean {
  for (let n: SyntaxNode | null = tree.resolveInner(pos, 1); n; n = n.parent)
    if (/^(FencedCode|CodeBlock|CodeText|CommentBlock|Comment)$/.test(n.name)) return true;
  return false;
}

// ---------------------------------------------------------------- the decorations

const HIDE = Decoration.replace({});
const marks = new Map<string, Decoration>();
const lines = new Map<string, Decoration>();
const markOf = (cls: string): Decoration => {
  let d = marks.get(cls);
  if (!d) marks.set(cls, (d = Decoration.mark({ class: cls })));
  return d;
};
const lineOf = (cls: string): Decoration => {
  let d = lines.get(cls);
  if (!d) lines.set(cls, (d = Decoration.line({ class: cls })));
  return d;
};
const space = (c: string | undefined): boolean => c === ' ' || c === '\t';
/** A line longer than this is never an embed (an address is at most 2048 characters; a frame's code a little more). */
const MAX_EMBED_LINE = 4096;

interface ListLine {
  depth: number;
  /** The item's first line: its mark, and its task box. */
  item: { mark: SyntaxNode | null; ordered: boolean; task: SyntaxNode | null } | null;
}

/** Everything the preview draws over the text, for the state as it is. `tree` defaults to the state's parse. */
export function build(state: EditorState, tree: Tree = syntaxTree(state)): DecorationSet {
  const doc = state.doc;
  const cfg = state.facet(liveConfig);
  const act = activeLines(state);
  const lineOn = (n: number): boolean => act.some(([a, b]) => n >= a && n <= b);
  const on = (from: number, to: number): boolean => {
    const a = doc.lineAt(from).number,
      b = doc.lineAt(to).number;
    return act.some(([x, y]) => x <= b && y >= a);
  };
  const out: Range<Decoration>[] = [];
  /** Per line: where the text hidden at its start ends (a quote's `> `), so a list's mark or indent never overlaps it.
   * Hidden text further on, such as the `**` of a bold word after a list's `1. `, doesn't count. */
  const hiddenTo = new Map<number, number>();
  const hide = (from: number, to: number): void => {
    if (to <= from) return;
    out.push(HIDE.range(from, to));
    const l = doc.lineAt(from);
    const start = hiddenTo.get(l.number) ?? l.from;
    if (from <= start || !doc.sliceString(start, from).trim()) hiddenTo.set(l.number, Math.max(start, to));
  };
  const mark = (cls: string, from: number, to: number, attrs?: Record<string, string>): void => {
    if (to > from) out.push((attrs ? Decoration.mark({ class: cls, attributes: attrs }) : markOf(cls)).range(from, to));
  };
  const markup = (from: number, to: number): void => mark('ig-mk', from, to);
  const line = (pos: number, cls: string): void => void out.push(lineOf(cls).range(doc.lineAt(pos).from));
  /** The first position after the blanks at `pos` on its line. */
  const after = (pos: number): number => {
    const l = doc.lineAt(pos);
    let i = pos - l.from;
    while (i < l.length && space(l.text[i])) i++;
    return l.from + i;
  };
  /** The node's marks of a type: shown dim on the caret's lines, else hidden. */
  const marksOf = (n: SyntaxNode, type: string): void => {
    const shown = on(n.from, n.to);
    for (const c of n.getChildren(type)) {
      if (shown) markup(c.from, c.to);
      else hide(c.from, c.to);
    }
  };
  const link = (from: number, to: number, url: string | null): void => {
    const href = url ? safeHref(url) : null;
    mark('ig-a', from, to, href ? { 'data-href': href } : undefined);
  };
  const lists = new Map<number, ListLine>();

  tree.iterate({
    enter(ref) {
      const n = ref.node;
      const name = ref.name;
      const h = /^ATXHeading([1-6])$/.exec(name);
      if (h) {
        line(n.from, `ig-h ig-h${h[1]}`);
        const l = doc.lineAt(n.from);
        const ms = n.getChildren('HeaderMark');
        if (on(n.from, n.to)) for (const m of ms) markup(m.from, m.to);
        else {
          let open = l.from;
          ms.forEach((m, i) => {
            if (i === 0 && m.from === n.from) {
              // The opening #s, with the blanks after them (and before them, when they start the line).
              open = after(m.to);
              hide(l.text.slice(0, n.from - l.from).trim() ? n.from : l.from, open);
            } else {
              let s = m.from;
              while (s > open && space(l.text[s - 1 - l.from])) s--;
              hide(s, m.to);
            }
          });
        }
        return;
      }
      switch (name) {
        case 'SetextHeading1':
        case 'SetextHeading2': {
          const a = doc.lineAt(n.from).number,
            b = doc.lineAt(n.to).number;
          for (let i = a; i < b; i++) line(doc.line(i).from, `ig-h ig-h${name.slice(-1)}`);
          const m = n.getChild('HeaderMark');
          if (m) markup(m.from, m.to);
          return;
        }
        case 'Emphasis':
          mark('ig-i', n.from, n.to);
          marksOf(n, 'EmphasisMark');
          return;
        case 'StrongEmphasis':
          mark('ig-b', n.from, n.to);
          marksOf(n, 'EmphasisMark');
          return;
        case 'Strikethrough':
          mark('ig-s', n.from, n.to);
          marksOf(n, 'StrikethroughMark');
          return;
        case 'InlineCode': {
          const ms = n.getChildren('CodeMark');
          if (ms.length < 2) return false;
          mark('ig-code', ms[0]!.to, ms[ms.length - 1]!.from);
          marksOf(n, 'CodeMark');
          return false;
        }
        case 'Link': {
          // Only [text](address) links: a bare [text] is a script's picture or sound, kept as typed, a little quieter.
          const ms = n.getChildren('LinkMark');
          const url = n.getChild('URL');
          if (ms.length < 3 || !url) {
            if (ms.length === 2 && !n.getChild('LinkLabel')) mark('ig-cue', n.from, n.to);
            return;
          }
          const from = ms[0]!.to,
            to = ms[1]!.from;
          link(from, to, doc.sliceString(url.from, url.to));
          if (on(n.from, n.to)) {
            markup(n.from, from);
            markup(to, n.to);
          } else {
            hide(n.from, from);
            hide(to, n.to);
          }
          return;
        }
        case 'Image': {
          const ms = n.getChildren('LinkMark');
          const url = n.getChild('URL');
          const src = url ? doc.sliceString(url.from, url.to) : '';
          const at = src && cfg.image ? cfg.image(src) : null;
          if (!at) {
            mark('ig-img-src', n.from, n.to);
            return false;
          }
          const caption = ms.length >= 2 ? doc.sliceString(ms[0]!.to, ms[1]!.from).trim() : '';
          if (on(n.from, n.to)) {
            markup(n.from, n.to);
            out.push(
              Decoration.widget({ widget: new Picture(at, caption, true), block: true, side: 1 }).range(
                doc.lineAt(n.to).to,
              ),
            );
          } else out.push(Decoration.replace({ widget: new Picture(at, caption, false) }).range(n.from, n.to));
          return false;
        }
        case 'Autolink': {
          const url = n.getChild('URL');
          if (url) link(url.from, url.to, doc.sliceString(url.from, url.to));
          marksOf(n, 'LinkMark');
          return false;
        }
        case 'URL': {
          // A bare address (GFM): links and images handle their own.
          if (!/^(Link|Image|Autolink|LinkReference)$/.test(n.parent?.name ?? '')) {
            link(n.from, n.to, doc.sliceString(n.from, n.to));
          }
          return;
        }
        case 'Blockquote': {
          const a = doc.lineAt(n.from).number,
            b = doc.lineAt(n.to).number;
          for (let i = a; i <= b; i++) line(doc.line(i).from, 'ig-quote');
          return;
        }
        case 'QuoteMark': {
          const l = doc.lineAt(n.from);
          if (lineOn(l.number)) markup(n.from, n.to);
          else hide(n.from, space(l.text[n.to - l.from]) ? n.to + 1 : n.to);
          return;
        }
        case 'HorizontalRule':
          line(n.from, 'ig-hr');
          if (on(n.from, n.to)) markup(n.from, n.to);
          else hide(n.from, n.to);
          return false;
        case 'FencedCode':
        case 'CodeBlock': {
          const a = doc.lineAt(n.from).number,
            b = doc.lineAt(n.to).number;
          for (let i = a; i <= b; i++)
            line(doc.line(i).from, `ig-pre${i === a ? ' ig-pre-top' : ''}${i === b ? ' ig-pre-end' : ''}`);
          for (const c of [...n.getChildren('CodeMark'), ...n.getChildren('CodeInfo')]) {
            if (lineOn(doc.lineAt(c.from).number)) markup(c.from, c.to);
            else hide(c.from, c.to);
          }
          return false;
        }
        case 'ListItem': {
          let depth = 0;
          for (let p = n.parent; p; p = p.parent) if (p.name === 'ListItem') depth++;
          const a = doc.lineAt(n.from).number,
            b = doc.lineAt(n.to).number;
          const item = {
            mark: n.getChild('ListMark'),
            ordered: n.parent?.name === 'OrderedList',
            task: n.getChild('Task')?.getChild('TaskMarker') ?? null,
          };
          for (let i = a; i <= b; i++) lists.set(i, { depth, item: i === a ? item : null });
          return;
        }
        case 'Escape':
          if (!on(n.from, n.to)) hide(n.from, n.from + 1);
          return;
        case 'Comment':
        case 'CommentBlock':
        case 'HTMLBlock':
        case 'HTMLTag':
        case 'ProcessingInstructionBlock':
          mark('ig-faint', n.from, n.to);
          return false;
      }
      return;
    },
  });

  for (const [num, li] of lists) {
    const l = doc.line(num);
    const active = lineOn(num);
    let cls = `ig-li ig-d${Math.min(li.depth, 5)}`;
    const it = li.item;
    if (it?.mark) {
      cls += ' ig-li-first';
      const task = it.task;
      const checked = !!task && /[xX]/.test(doc.sliceString(task.from + 1, task.to - 1));
      if (checked) cls += ' ig-done';
      if (!active) {
        let start = it.mark.from;
        while (start > l.from && space(l.text[start - 1 - l.from])) start--;
        start = Math.max(start, hiddenTo.get(num) ?? l.from);
        const end = after(task ? task.to : it.mark.to);
        if (end > start)
          out.push(
            Decoration.replace({
              widget: task
                ? new TaskBox(checked, !!cfg.readonly)
                : new ListMark(it.ordered ? doc.sliceString(it.mark.from, it.mark.to) : '•', it.ordered),
            }).range(start, end),
          );
      } else {
        markup(it.mark.from, it.mark.to);
        if (task) markup(task.from, task.to);
      }
    } else if (!it && !active) {
      // A later line of an item: its own indent is the hanging indent's.
      let ws = 0;
      while (ws < l.length && space(l.text[ws])) ws++;
      const from = Math.max(l.from, hiddenTo.get(num) ?? l.from);
      if (ws && ws < l.length && l.from + ws > from) hide(from, l.from + ws);
    }
    out.push(lineOf(cls).range(l.from));
  }

  // Embeds (spec/scripts.md, "Embeds"): off the caret's line the line folds away and only its embed shows.
  for (let i = 1; i <= doc.lines; i++) {
    const l = doc.line(i);
    const e = embedAt(l, tree, cfg);
    if (!e) continue;
    if (!lineOn(i)) out.push(lineOf('ig-embed-src').range(l.from));
    out.push(Decoration.widget({ widget: new EmbedView(e, cfg), block: true, side: 1 }).range(l.to));
  }

  const lit = state.field(flash, false);
  if (lit !== null && lit !== undefined && lit <= doc.length) out.push(lineOf('ig-flash').range(doc.lineAt(lit).from));

  const asks = state.field(questions, false) ?? [];
  if (asks.length) {
    const text = doc.toString();
    const all = text.split('\n');
    for (const q of asks) {
      const at = findAnchor(text, q.anchor);
      if (at < 0) continue;
      const p = paragraph(all, at);
      const end = doc.line((p ? p[1] : at) + 1).to;
      out.push(Decoration.widget({ widget: new Ask(q, cfg), block: true, side: 2 }).range(end));
    }
  }
  return Decoration.set(out, true);
}

// ---------------------------------------------------------------- the extension

interface Built {
  deco: DecorationSet;
  tree: Tree;
  key: string;
}

const keyOf = (s: EditorState): string => activeLines(s).join(';');

/** The preview's decorations, or none when building them fails: a bug in the preview shows the plain Markdown, never
 * an empty page. */
function safeBuild(s: EditorState, tree: Tree): DecorationSet {
  try {
    return build(s, tree);
  } catch (e) {
    console.error('scripts: the live preview failed; showing plain text', e);
    return Decoration.none;
  }
}

const preview = StateField.define<Built>({
  create(s) {
    const tree = syntaxTree(s);
    return { deco: safeBuild(s, tree), tree, key: keyOf(s) };
  },
  update(v, tr) {
    const tree = syntaxTree(tr.state);
    const key = keyOf(tr.state);
    const redraw =
      tr.docChanged ||
      tree !== v.tree ||
      key !== v.key ||
      tr.effects.some((e) => e.is(setFlash) || e.is(setQuestions)) ||
      tr.startState.facet(liveConfig) !== tr.state.facet(liveConfig);
    return redraw ? { deco: safeBuild(tr.state, tree), tree, key } : v;
  },
  provide: (f) => EditorView.decorations.from(f, (v) => v.deco),
});

/** The preview's decorations now (for tests). */
export const decorationsOf = (state: EditorState): DecorationSet => state.field(preview).deco;

/** A link's address under a tap or a click, when it opens: off the caret's lines (on them it's text to edit), or
 * anywhere with Ctrl or ⌘. */
function linkAt(e: MouseEvent | TouchEvent, view: EditorView): string | null {
  const el = (e.target as Element | null)?.closest?.('.ig-a[data-href]');
  if (!(el instanceof HTMLElement) || !view.contentDOM.contains(el)) return null;
  const mod = 'ctrlKey' in e && (e.ctrlKey || e.metaKey);
  if (!mod && lineActive(view.state, view.state.doc.lineAt(view.posAtDOM(el)).number)) return null;
  return el.dataset.href ?? null;
}

/** Opens a link in a new tab, without giving it this page (rel=noopener). */
export function openLink(href: string): void {
  const a = document.createElement('a');
  a.href = href;
  a.target = '_blank';
  a.rel = 'noopener noreferrer';
  a.click();
}

/** The live preview: fields, decorations, and taps on links. */
export function livePreview(cfg: LiveConfig): Extension {
  let touch: { x: number; y: number; href: string } | null = null;
  return [
    liveConfig.of(cfg),
    focused,
    flash,
    questions,
    preview,
    Prec.high(
      keymap.of([
        { key: 'ArrowDown', run: stepOnto(1) },
        { key: 'ArrowUp', run: stepOnto(-1) },
      ]),
    ),
    EditorView.focusChangeEffect.of((_, on) => setFocus.of(on)),
    EditorView.domEventHandlers({
      mousedown(e, view) {
        if (e.button !== 0 || !linkAt(e, view)) return false;
        e.preventDefault();
        return true;
      },
      click(e, view) {
        const href = e.button === 0 ? linkAt(e, view) : null;
        if (!href) return false;
        e.preventDefault();
        openLink(href);
        return true;
      },
      touchstart(e, view) {
        const t = e.touches[0];
        const href = t && e.touches.length === 1 ? linkAt(e, view) : null;
        touch = href && t ? { x: t.clientX, y: t.clientY, href } : null;
        return false;
      },
      touchend(e) {
        const t = e.changedTouches[0],
          s = touch;
        touch = null;
        if (!s || !t || Math.hypot(t.clientX - s.x, t.clientY - s.y) > 10) return false;
        e.preventDefault();
        openLink(s.href);
        return true;
      },
    }),
  ];
}
