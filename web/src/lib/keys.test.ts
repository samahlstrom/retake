import { describe, expect, it } from 'vitest';
import {
  HINT_KEY,
  hintSeen,
  SHORTCUT_ACTIONS,
  DEFAULT_SHORTCUTS,
  shortcutLabel,
  keyAction,
  markHintSeen,
  type KeyLike,
} from './keys';

const k = (key: string, mods: Partial<KeyLike> = {}): KeyLike => ({
  key,
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  repeat: false,
  ...mods,
});

describe('keys', () => {
  it('maps playback and recording keys', () => {
    const map: Record<string, string> = {
      r: 'toggleRec',
      R: 'toggleRec',
      ' ': 'togglePlay',
      k: 'togglePlay',
      ArrowLeft: 'back',
      ArrowRight: 'forward',
      '1': 'color1',
      '4': 'color4',
      z: 'undo',
      ',': 'framePrev',
      '.': 'frameNext',
      f: 'fullscreen',
      Escape: 'escape',
    };
    for (const [key, act] of Object.entries(map)) expect(keyAction(k(key))).toEqual({ act, stop: true });
  });

  it('N shows or hides the notes sidebar (once per press); Ctrl/Cmd+V is left to the paste event', () => {
    expect(keyAction(k('n'))).toEqual({ act: 'notes', stop: true });
    expect(keyAction(k('N'))).toEqual({ act: 'notes', stop: true });
    expect(keyAction(k('n', { repeat: true }))).toEqual({ act: null, stop: true });
    expect(keyAction(k('v', { ctrlKey: true }))).toBeNull();
    expect(keyAction(k('v', { metaKey: true }))).toBeNull();
  });

  it('E switches to the eraser and back (once per press)', () => {
    expect(keyAction(k('e'))).toEqual({ act: 'eraser', stop: true });
    expect(keyAction(k('E'))).toEqual({ act: 'eraser', stop: true });
    expect(keyAction(k('e', { repeat: true }))).toEqual({ act: null, stop: true });
  });

  it('> opens the next video of a home (once per press), and stays apart from . and N', () => {
    expect(keyAction(k('>'))).toEqual({ act: 'next', stop: true });
    expect(keyAction(k('>', { repeat: true }))).toEqual({ act: null, stop: true });
    expect(keyAction(k('.'))).toEqual({ act: 'frameNext', stop: true });
    expect(keyAction(k('N'))).toEqual({ act: 'notes', stop: true });
    expect(keyAction(k('>', { ctrlKey: true }))).toBeNull();
  });

  it('saves on Ctrl/Cmd+S and undoes on Ctrl/Cmd+Z without stopping the event', () => {
    expect(keyAction(k('s', { ctrlKey: true }))).toEqual({ act: 'save', stop: false });
    expect(keyAction(k('S', { metaKey: true }))).toEqual({ act: 'save', stop: false });
    expect(keyAction(k('z', { ctrlKey: true }))).toEqual({ act: 'undo', stop: false });
  });

  it('leaves other keys and modifier combinations alone', () => {
    for (const e of [k('x'), k('Enter'), k('Tab'), k('r', { ctrlKey: true }), k('r', { altKey: true }), k('5')])
      expect(keyAction(e)).toBeNull();
  });

  it('ignores auto-repeat of R, K, Z, F and Space, but not of seeking or frame steps', () => {
    for (const key of ['r', 'k', 'z', 'f', ' '])
      expect(keyAction(k(key, { repeat: true }))).toEqual({ act: null, stop: true });
    for (const key of ['ArrowLeft', 'ArrowRight', ',', '.', '1'])
      expect(keyAction(k(key, { repeat: true }))?.act).not.toBeNull();
  });

  it('C flips the version, once per press; [ and ] are not keys any more', () => {
    expect(keyAction(k('c'))).toEqual({ act: 'flip', stop: true });
    expect(keyAction(k('C'))).toEqual({ act: 'flip', stop: true });
    expect(keyAction(k('c', { repeat: true }))).toEqual({ act: null, stop: true });
    for (const key of ['[', ']']) expect(keyAction(k(key))).toBeNull();
  });

  it('ignores inherited object keys', () => {
    expect(keyAction(k('constructor'))).toBeNull();
    expect(keyAction(k('toString'))).toBeNull();
  });
});

describe('? and the hint that shows once', () => {
  it('? opens the keys sheet, and holding it does not flicker', () => {
    expect(keyAction(k('?'))).toEqual({ act: 'help', stop: true });
    expect(keyAction(k('?', { repeat: true }))).toEqual({ act: null, stop: true });
  });
  it('the sheet lists every key the player handles', () => {
    const listed = SHORTCUT_ACTIONS.map(({ action }) => shortcutLabel(DEFAULT_SHORTCUTS[action]));
    for (const key of ['R', 'K', '←', '→', ',', '.', '1', '4', 'E', 'Z', 'C', 'N', '>', 'F', '?'])
      expect(listed).toContain(key);
    for (const key of ['[', ']']) expect(listed).not.toContain(key);
  });
  it('remembers the dismissal, and survives blocked storage', () => {
    const store = new Map<string, string>();
    const storage = {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, v: string) => void store.set(key, v),
    };
    expect(hintSeen(storage)).toBe(false);
    markHintSeen(storage);
    expect(store.get(HINT_KEY)).toBe('seen');
    expect(hintSeen(storage)).toBe(true);
    const blocked = {
      getItem: () => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('blocked');
      },
    };
    expect(hintSeen(blocked)).toBe(false);
    expect(() => markHintSeen(blocked)).not.toThrow();
    expect(hintSeen(null)).toBe(false);
  });
});
