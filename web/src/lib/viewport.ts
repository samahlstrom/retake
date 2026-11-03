// Typing on a phone (spec/notes.md, "Typing on a phone"): the page never pans, and lays itself out from the visual
// viewport instead. The math, pure so it's unit-tested; viewport.svelte.ts reads the browser and writes the CSS
// variables (--vv-top, --kb, --vv-h) and <html data-typing data-kb>.

/** Under this, what covers the page's bottom is a toolbar the browser lays over it, not an on-screen keyboard. */
export const KB_MIN = 80;
/** Record and Send's lift over such a toolbar: never more than this (more is a keyboard going down). */
export const TOOLBAR_MAX = 150;
/** A remembered keyboard height is dropped when no keyboard comes within this long (a hardware keyboard). */
export const GUESS_MS = 1000;
/** Compose mode never shrinks the video below this. */
export const MIN_SCALE = 0.25;

/** What the browser says: the layout viewport's height, and the visual viewport's offset, height and zoom. */
export interface Reading {
  /** `innerHeight`: the layout viewport, which the keyboard doesn't resize. */
  inner: number;
  /** `visualViewport.offsetTop`: how far the visible area is panned down inside it. */
  top: number;
  /** `visualViewport.height`: the visible area's height, above the keyboard. */
  height: number;
  /** `visualViewport.scale`: pinch zoom (1: none). */
  scale: number;
}

const zoomed = (r: Reading): boolean => Math.abs(r.scale - 1) > 0.01;

/** What covers the bottom of the layout viewport: the on-screen keyboard, or a toolbar the browser lays over the page.
 * Nothing while zoomed (a zoomed visible area is smaller without anything covering it). */
export function covered(r: Reading): number {
  if (!(r.inner > 0) || !(r.height > 0) || zoomed(r)) return 0;
  return Math.max(0, Math.round(r.inner - (r.top + r.height)));
}

/** Which text field has the focus: the comment box, the chat's composer, the document in the version slot, another. */
export type Typing = 'comment' | 'chat' | 'doc' | 'field';

/** How the page lays itself out now. */
export interface Layout {
  /** `--vv-top`: what's left of a pan of the visual viewport (normally 0: the page undoes pans). */
  top: number;
  /** `--kb`: the keyboard's height over the layout viewport's bottom; 0 when it isn't up. */
  kb: number;
  /** `--vv-h`: the height left above the keyboard (or above the toolbar, `lift`, when there is none). */
  height: number;
  /** `<html data-kb>`: the keyboard is up, measured or remembered. */
  up: boolean;
}

/**
 * The layout for a reading. A keyboard is only a keyboard while a field has the focus (`typing`) and covers 80 px or
 * more; until it has come up, the remembered height (`guess`) stands in for it. Without one, `lift` (a toolbar over
 * the page's bottom) is what the height leaves out.
 */
export function layoutFor(r: Reading, typing: Typing | null, guess: number | null, lift = 0): Layout {
  const top = zoomed(r) ? 0 : Math.max(0, Math.round(r.top));
  const measured = covered(r);
  let kb = 0;
  if (typing) {
    if (measured >= KB_MIN) kb = measured;
    else if (guess !== null && guess >= KB_MIN) kb = Math.round(guess);
  }
  const inner = Math.max(0, Math.round(r.inner));
  return { top, kb, height: Math.max(0, inner - top - (kb || lift)), up: kb > 0 };
}

/**
 * Record and Send's lift over a toolbar the browser lays over the page's bottom (spec/notes.md). While a field has
 * the focus it stays where it was, and so it does while more than a toolbar covers the bottom: that's the keyboard
 * still going down, and the bar would jump up over it.
 */
export function barLift(prev: number, r: Reading, typing: boolean): number {
  if (typing) return prev;
  const c = covered(r);
  return c > TOOLBAR_MAX ? prev : c;
}

/** The page has panned and must scroll back: anything but 0, unless the person zoomed in. */
export function panned(scrollX: number, scrollY: number, r: Reading): boolean {
  return !zoomed(r) && (Math.abs(scrollX) > 0.5 || Math.abs(scrollY) > 0.5);
}

// ---------------------------------------------------------------- the remembered keyboard heights

/** Where they're kept: `localStorage`, or nothing (blocked storage just doesn't remember). */
export interface Store {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}
export const KB_KEY = 'retake.kb';
/** How many heights are kept (orientations and window heights of one device). */
const KEEP = 6;

/** The key of a window: its orientation and height (a keyboard covers a different part of a different height). */
export const kbSlot = (width: number, height: number): string => `${width > height ? 'l' : 'p'}${Math.round(height)}`;

function readAll(store: Store | null): Record<string, number> {
  try {
    const v: unknown = JSON.parse(store?.getItem(KB_KEY) ?? '{}');
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {};
    const out: Record<string, number> = {};
    for (const [k, n] of Object.entries(v as Record<string, unknown>))
      if (typeof n === 'number' && n >= KB_MIN && n < 2000) out[k] = n;
    return out;
  } catch {
    return {};
  }
}

/** The keyboard's height the last time it was up in this window, or null. */
export function recall(store: Store | null, slot: string): number | null {
  return readAll(store)[slot] ?? null;
}

/** Keeps the keyboard's height for this window (the newest few windows). */
export function remember(store: Store | null, slot: string, kb: number): void {
  if (!store || kb < KB_MIN) return;
  const all = readAll(store);
  if (all[slot] === Math.round(kb)) return;
  delete all[slot];
  const kept = Object.entries(all).slice(-(KEEP - 1));
  try {
    store.setItem(KB_KEY, JSON.stringify(Object.fromEntries([...kept, [slot, Math.round(kb)]])));
  } catch {
    /* full or blocked: it just isn't remembered */
  }
}

// ---------------------------------------------------------------- which field

/** Fields that raise a keyboard: text inputs, text areas, editable text (not a read-only CodeMirror). */
export const TEXT_FIELD =
  'input:not([type=button],[type=checkbox],[type=radio],[type=range],[type=submit],[type=reset],[type=file],' +
  '[type=color],[type=image],[type=hidden]), textarea, [contenteditable]:not([contenteditable=false])';

/** What `typingOf` needs of an element (a DOM element has it). */
export interface FieldLike {
  matches(selector: string): boolean;
  closest(selector: string): { getAttribute(name: string): string | null } | null;
}

/** Which field this is: a `data-typing-as` on it or around it says (the comment box, the chat's composer and its
 * stand-in), else one in the stage (`#stage`, the version slot) is the document, else any field. */
export function typingOf(el: FieldLike | null | undefined): Typing | null {
  if (!el || typeof el.matches !== 'function' || !el.matches(TEXT_FIELD)) return null;
  const as = el.closest('[data-typing-as]')?.getAttribute('data-typing-as');
  if (as === 'comment' || as === 'chat' || as === 'doc') return as;
  return el.closest('#stage') ? 'doc' : 'field';
}

// ---------------------------------------------------------------- compose mode (a video)

/** Compose mode is on: a phone, the comment box has the focus, on a video (an extension's version has no video), for
 * someone who can comment. */
export function composeMode(o: { phone: boolean; typing: Typing | null; kind: boolean; canRecord: boolean }): boolean {
  return o.phone && o.typing === 'comment' && !o.kind && o.canRecord;
}

export interface ComposeIn {
  /** The keyboard's top (the visible area's bottom) in the page's coordinates; null while it isn't up. */
  kbTop: number | null;
  /** Where the comment box's bottom is in its place (the comments column's bottom). */
  boxBottom: number;
  /** The comment box's height. */
  boxHeight: number;
  /** The stage's top, and the bottom of the video's picture in it, unscaled. */
  stageTop: number;
  pictureBottom: number;
}

/**
 * Compose mode's two transforms: the box moves by `dy` to sit on the keyboard (not at all while it isn't up), and the
 * video scales from its top by `scale` when its picture wouldn't fit above the box (1: it stays as it is).
 */
export function composeLayout(c: ComposeIn): { dy: number; scale: number } {
  const dy = c.kbTop === null ? 0 : Math.round((c.kbTop - c.boxBottom) * 10) / 10;
  const room = c.boxBottom + dy - c.boxHeight - c.stageTop;
  const need = c.pictureBottom - c.stageTop;
  if (need <= 0 || need <= room + 0.5) return { dy, scale: 1 };
  return { dy, scale: Math.round(Math.max(MIN_SCALE, room / need) * 1000) / 1000 };
}
