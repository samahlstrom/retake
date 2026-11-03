import { describe, expect, it } from 'vitest';
import {
  barLift,
  composeLayout,
  covered,
  KB_KEY,
  kbSlot,
  layoutFor,
  panned,
  recall,
  remember,
  typingOf,
  type FieldLike,
  type Reading,
  type Store,
} from './viewport';

/** An iPhone 15 in Safari: a 659 px layout viewport; with the keyboard up, 413 px of it show. */
const IPHONE = 659;
const read = (height: number, top = 0, scale = 1, inner = IPHONE): Reading => ({ inner, top, height, scale });

describe('the keyboard from innerHeight and the visual viewport', () => {
  it('is what the visual viewport leaves uncovered at the layout viewport’s bottom', () => {
    expect(covered(read(IPHONE))).toBe(0);
    expect(covered(read(413))).toBe(246);
    expect(covered(read(412.6))).toBe(246); // rounded
  });

  it('counts a pan: the visible area panned down covers less of the bottom', () => {
    expect(covered(read(413, 120))).toBe(126);
    expect(covered(read(413, 246))).toBe(0);
  });

  it('is nothing while zoomed, or before the browser says anything', () => {
    expect(covered(read(300, 40, 2))).toBe(0);
    expect(covered(read(0))).toBe(0);
    expect(covered({ inner: 0, top: 0, height: 0, scale: 1 })).toBe(0);
  });
});

describe('the layout while typing', () => {
  it('has no keyboard while no field has the focus, whatever covers the bottom', () => {
    expect(layoutFor(read(413), null, null)).toEqual({ top: 0, kb: 0, height: IPHONE, up: false });
  });

  it('measures the keyboard while a field has the focus', () => {
    expect(layoutFor(read(413), 'comment', null)).toEqual({ top: 0, kb: 246, height: 413, up: true });
  });

  it('takes a toolbar over the page for what it is, not a keyboard', () => {
    // Safari's toolbar over the bottom (50 px): the bar's lift, never "the keyboard is up".
    expect(layoutFor(read(609), 'field', null, 50)).toEqual({ top: 0, kb: 0, height: 609, up: false });
    expect(layoutFor(read(609), null, null, 50)).toEqual({ top: 0, kb: 0, height: 609, up: false });
  });

  it('lays out for the remembered keyboard while it comes up, and for the measured one once it is there', () => {
    expect(layoutFor(read(IPHONE), 'chat', 250)).toEqual({ top: 0, kb: 250, height: 409, up: true });
    expect(layoutFor(read(413), 'chat', 250)).toEqual({ top: 0, kb: 246, height: 413, up: true });
    // No field: no guess.
    expect(layoutFor(read(IPHONE), null, 250).up).toBe(false);
    // A guess smaller than a keyboard is none.
    expect(layoutFor(read(IPHONE), 'chat', 40).up).toBe(false);
  });

  it('keeps what is left of a pan, so the page’s top is the visible area’s', () => {
    const l = layoutFor(read(413, 100), 'comment', null);
    expect(l.top).toBe(100);
    expect(l.top + l.height).toBe(100 + 413); // its bottom is still the keyboard's top
    expect(l.kb).toBe(146);
  });

  it('ignores the visual viewport while zoomed in', () => {
    expect(layoutFor(read(300, 50, 2.2), 'field', null)).toEqual({ top: 0, kb: 0, height: IPHONE, up: false });
  });
});

describe('the page never pans', () => {
  it('scrolls back from any scroll, unless the person zoomed in', () => {
    expect(panned(0, 0, read(IPHONE))).toBe(false);
    expect(panned(0, 180, read(413))).toBe(true);
    expect(panned(3, 0, read(413))).toBe(true);
    expect(panned(0, 0.2, read(413))).toBe(false);
    expect(panned(0, 180, read(300, 0, 2))).toBe(false);
  });
});

describe('Record and Send’s lift', () => {
  it('follows a toolbar over the page’s bottom while nothing is typed', () => {
    expect(barLift(0, read(609), false)).toBe(50);
    expect(barLift(50, read(IPHONE), false)).toBe(0);
  });

  it('holds while a field has the focus', () => {
    expect(barLift(50, read(413), true)).toBe(50);
    expect(barLift(0, read(609), true)).toBe(0);
  });

  it('holds while the keyboard goes down after the focus left (more than a toolbar covers the bottom)', () => {
    expect(barLift(0, read(413), false)).toBe(0);
    expect(barLift(50, read(500), false)).toBe(50);
    // and follows again once it's down
    expect(barLift(50, read(609), false)).toBe(50);
    expect(barLift(50, read(IPHONE), false)).toBe(0);
  });
});

describe('the remembered keyboard heights', () => {
  const memory = (): Store & { data: Map<string, string> } => {
    const data = new Map<string, string>();
    return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v) };
  };

  it('are kept per orientation and window height', () => {
    expect(kbSlot(393, 659)).toBe('p659');
    expect(kbSlot(852, 361.4)).toBe('l361');
    const s = memory();
    remember(s, 'p659', 246);
    remember(s, 'l361', 190);
    expect(recall(s, 'p659')).toBe(246);
    expect(recall(s, 'l361')).toBe(190);
    expect(recall(s, 'p700')).toBeNull();
    remember(s, 'p659', 291.4);
    expect(recall(s, 'p659')).toBe(291);
  });

  it('keep the newest few windows', () => {
    const s = memory();
    for (let h = 600; h < 610; h++) remember(s, `p${h}`, 250);
    const kept = Object.keys(JSON.parse(s.data.get(KB_KEY)!) as object);
    expect(kept).toHaveLength(6);
    expect(kept).toContain('p609');
    expect(kept).not.toContain('p600');
  });

  it('never keep a toolbar, and survive broken or blocked storage', () => {
    const s = memory();
    remember(s, 'p659', 50);
    expect(recall(s, 'p659')).toBeNull();
    s.data.set(KB_KEY, '{oops');
    expect(recall(s, 'p659')).toBeNull();
    s.data.set(KB_KEY, JSON.stringify({ p659: 'x', p1: 9999, l1: 200 }));
    expect(recall(s, 'p659')).toBeNull();
    expect(recall(s, 'p1')).toBeNull();
    expect(recall(s, 'l1')).toBe(200);
    expect(recall(null, 'p659')).toBeNull();
    const full: Store = {
      getItem: () => null,
      setItem: () => {
        throw new Error('QuotaExceededError');
      },
    };
    expect(() => remember(full, 'p659', 246)).not.toThrow();
    remember(null, 'p659', 246);
  });
});

describe('which field has the focus', () => {
  /** A stand-in element: what it matches, and the attributes of its ancestors (itself first). */
  const el = (field: boolean, around: Record<string, string>[] = [], inStage = false): FieldLike => ({
    matches: () => field,
    closest: (sel) => {
      if (sel === '#stage') return inStage ? { getAttribute: () => null } : null;
      const name = /^\[([a-z-]+)\]$/.exec(sel)?.[1];
      const hit = around.find((a) => name && name in a);
      return hit ? { getAttribute: (n) => hit[n] ?? null } : null;
    },
  });

  it('is nothing for what isn’t a text field', () => {
    expect(typingOf(null)).toBeNull();
    expect(typingOf(el(false, [{ 'data-typing-as': 'comment' }]))).toBeNull();
    expect(typingOf({} as FieldLike)).toBeNull();
  });

  it('is what the field (or what holds it) says it is: the comment box, the chat, the document', () => {
    expect(typingOf(el(true, [{ 'data-typing-as': 'comment' }]))).toBe('comment');
    expect(typingOf(el(true, [{ 'data-typing-as': 'chat' }]))).toBe('chat');
    expect(typingOf(el(true, [{ 'data-typing-as': 'doc' }]))).toBe('doc');
    expect(typingOf(el(true, [{ 'data-typing-as': 'nonsense' }]))).toBe('field');
  });

  it('is the document in the stage (the version slot), any other field elsewhere', () => {
    expect(typingOf(el(true, [], true))).toBe('doc');
    expect(typingOf(el(true))).toBe('field');
  });
});

describe('compose mode on a video', () => {
  // iPhone 15 portrait: the stage 0..221 (16:9), the comments column ending at 553 (Record and Send under it), the box
  // 88 px (its line and the field).
  const at = { boxBottom: 553, boxHeight: 88, stageTop: 0, pictureBottom: 221 };

  it('leaves the box where it is until the keyboard is up', () => {
    expect(composeLayout({ ...at, kbTop: null })).toEqual({ dy: 0, scale: 1 });
  });

  it('moves the box onto the keyboard, and leaves a video that fits above it alone', () => {
    expect(composeLayout({ ...at, kbTop: 413 })).toEqual({ dy: -140, scale: 1 });
    // A shorter keyboard than the room under the box: the box goes down onto it.
    expect(composeLayout({ ...at, kbTop: 600 })).toEqual({ dy: 47, scale: 1 });
  });

  it('shrinks the video from its top only as much as it must to fit above the box', () => {
    // A tall video (9:16) whose picture reaches 480: the box's top is at 413 - 88 = 325.
    const { dy, scale } = composeLayout({ ...at, pictureBottom: 480, kbTop: 413 });
    expect(dy).toBe(-140);
    expect(scale).toBeCloseTo(325 / 480, 3);
    // A picture that just fits isn't touched.
    expect(composeLayout({ ...at, pictureBottom: 325, kbTop: 413 }).scale).toBe(1);
  });

  it('never shrinks it to nothing', () => {
    expect(composeLayout({ ...at, pictureBottom: 2000, kbTop: 150 }).scale).toBe(0.25);
  });

  it('works from wherever the stage starts (a safe area over it)', () => {
    const { scale } = composeLayout({
      boxBottom: 600,
      boxHeight: 88,
      stageTop: 47,
      pictureBottom: 47 + 500,
      kbTop: 420,
    });
    expect(scale).toBeCloseTo((420 - 88 - 47) / 500, 3);
  });
});
