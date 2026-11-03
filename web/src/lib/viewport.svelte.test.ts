// The viewport store (lib/viewport.svelte.ts) against a stand-in browser: an iPhone's layout viewport, its visual
// viewport (what the keyboard leaves), the focus and localStorage.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { composeMode, GUESS_MS, KB_KEY } from './viewport';
import { Viewport } from './viewport.svelte';

class Target {
  #on = new Map<string, Set<(e: unknown) => void>>();
  addEventListener(t: string, f: (e: unknown) => void): void {
    if (!this.#on.has(t)) this.#on.set(t, new Set());
    this.#on.get(t)!.add(f);
  }
  removeEventListener(t: string, f: (e: unknown) => void): void {
    this.#on.get(t)?.delete(f);
  }
  emit(t: string, e: unknown = {}): void {
    for (const f of this.#on.get(t) ?? []) f(e);
  }
}

/** A field: what kind of field it says it is (data-typing-as), or a plain one. */
const field = (as?: string) => ({
  matches: () => true,
  closest: (sel: string) =>
    sel === '[data-typing-as]' && as ? { getAttribute: () => as } : sel === '#stage' ? null : null,
});

let win: Target & { innerHeight: number; innerWidth: number; scrollX: number; scrollY: number; scrollTo: () => void };
let vv: Target & { offsetTop: number; height: number; scale: number };
let doc: Target & { activeElement: unknown; documentElement: Root };
let data: Map<string, string>;

interface Root {
  vars: Map<string, string>;
  dataset: Record<string, string>;
  attrs: Set<string>;
  style: { setProperty(k: string, v: string): void; removeProperty(k: string): void };
  toggleAttribute(n: string, on: boolean): void;
  removeAttribute(n: string): void;
}

function root(): Root {
  const vars = new Map<string, string>();
  const attrs = new Set<string>();
  return {
    vars,
    attrs,
    dataset: {},
    style: { setProperty: (k, v) => void vars.set(k, v), removeProperty: (k) => void vars.delete(k) },
    toggleAttribute: (n, on) => void (on ? attrs.add(n) : attrs.delete(n)),
    removeAttribute: (n) => void attrs.delete(n),
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  vv = Object.assign(new Target(), { offsetTop: 0, height: 659, scale: 1 });
  win = Object.assign(new Target(), {
    innerHeight: 659,
    innerWidth: 393,
    scrollX: 0,
    scrollY: 0,
    scrollTo: vi.fn(() => {
      win.scrollY = 0;
      vv.offsetTop = 0;
    }),
    visualViewport: vv,
  });
  doc = Object.assign(new Target(), { activeElement: null, documentElement: root() });
  data = new Map();
  vi.stubGlobal('window', win);
  vi.stubGlobal('document', doc);
  vi.stubGlobal('requestAnimationFrame', (f: () => void) => setTimeout(f, 16));
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function start(phone = true): Viewport {
  const v = new Viewport({
    active: () => phone,
    storage: () => ({ getItem: (k) => data.get(k) ?? null, setItem: (k, s) => void data.set(k, s) }),
  });
  v.start();
  return v;
}

/** The person taps a field: it takes the focus. */
function focus(el: unknown): void {
  doc.activeElement = el;
  doc.emit('focusin', { target: el });
}
function blur(): void {
  doc.activeElement = null;
  doc.emit('focusout');
  vi.advanceTimersByTime(1);
}
/** The keyboard comes up (or goes down): the visual viewport's new height. */
function keyboard(height: number, offsetTop = 0): void {
  vv.height = height;
  vv.offsetTop = offsetTop;
  vv.emit('resize');
}

describe('the viewport store', () => {
  it('writes nothing on a desktop', () => {
    start(false);
    focus(field('comment'));
    keyboard(413);
    expect(doc.documentElement.vars.size).toBe(0);
    expect(doc.documentElement.dataset.typing).toBeUndefined();
  });

  it('says which field has the focus, and lays out above the keyboard once it is up', () => {
    const v = start();
    expect(doc.documentElement.vars.get('--vv-h')).toBe('659px');
    focus(field('comment'));
    expect(v.typing).toBe('comment');
    expect(doc.documentElement.dataset.typing).toBe('comment');
    expect(v.up).toBe(false); // nothing remembered yet: it waits for the keyboard
    keyboard(413);
    expect(v.up).toBe(true);
    expect(doc.documentElement.vars.get('--kb')).toBe('246px');
    expect(doc.documentElement.vars.get('--vv-h')).toBe('413px');
    expect(doc.documentElement.attrs.has('data-kb')).toBe(true);
    blur();
    expect(v.typing).toBeNull();
    expect(v.up).toBe(false); // at once, while the keyboard is still going down
    expect(doc.documentElement.dataset.typing).toBeUndefined();
    expect(doc.documentElement.vars.get('--kb')).toBe('0px');
  });

  it('remembers the keyboard, and lays out for it the next time before it has come up', () => {
    const v = start();
    focus(field('chat'));
    keyboard(413);
    blur();
    keyboard(659);
    expect(JSON.parse(data.get(KB_KEY)!)).toEqual({ p659: 246 });
    focus(field('chat'));
    expect(v.up).toBe(true);
    expect(v.kb).toBe(246);
    expect(v.height).toBe(413);
    keyboard(420); // a slightly different one this time
    expect(v.kb).toBe(239);
  });

  it('drops a remembered keyboard that never comes (a hardware keyboard)', () => {
    data.set(KB_KEY, JSON.stringify({ p659: 246 }));
    const v = start();
    focus(field('comment'));
    expect(v.up).toBe(true);
    vi.advanceTimersByTime(GUESS_MS + 1);
    expect(v.up).toBe(false);
    expect(v.height).toBe(659);
  });

  it('undoes a pan at once', () => {
    const v = start();
    focus(field('comment'));
    win.scrollY = 180;
    vv.offsetTop = 180;
    win.emit('scroll');
    expect(win.scrollTo).toHaveBeenCalledWith(0, 0);
    expect(v.top).toBe(0);
  });

  it('follows what is left of a pan of the visual viewport', () => {
    const v = start();
    focus(field('comment'));
    keyboard(413, 120);
    expect(v.top).toBe(120);
    expect(v.top + v.height).toBe(533); // still the keyboard's top
    expect(doc.documentElement.vars.get('--vv-top')).toBe('120px');
  });

  it('holds the bar’s lift while typing and while the keyboard goes down', () => {
    const v = start();
    keyboard(609); // Safari's toolbar over the page
    expect(v.lift).toBe(50);
    focus(field());
    keyboard(413);
    expect(v.lift).toBe(50);
    blur();
    keyboard(500); // going down
    expect(v.lift).toBe(50);
    keyboard(659);
    expect(v.lift).toBe(0);
  });

  it('keeps the field through a hand-over (the stand-in to the real one)', () => {
    const v = start();
    focus(field('chat')); // the stand-in, in the tap
    doc.emit('focusout');
    focus(field('chat')); // the composer takes it over
    vi.advanceTimersByTime(1);
    expect(v.typing).toBe('chat');
  });
});

describe('compose mode', () => {
  it('is the comment box with the focus, on a video, on a phone, for someone who can comment', () => {
    const on = { phone: true, typing: 'comment' as const, kind: false, canRecord: true };
    expect(composeMode(on)).toBe(true);
    expect(composeMode({ ...on, phone: false })).toBe(false);
    expect(composeMode({ ...on, typing: 'field' })).toBe(false);
    expect(composeMode({ ...on, typing: null })).toBe(false);
    expect(composeMode({ ...on, typing: 'chat' })).toBe(false);
    expect(composeMode({ ...on, kind: true })).toBe(false);
    expect(composeMode({ ...on, canRecord: false })).toBe(false);
  });
});
