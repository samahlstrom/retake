import { describe, expect, it } from 'vitest';
import { keyAction, type KeyLike } from './keys';

const k = (key: string, mods: Partial<KeyLike> = {}): KeyLike => ({
  key,
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  repeat: false,
  ...mods,
});

describe('keys', () => {
  it('maps the legacy keys', () => {
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
      c: 'flip',
      '[': 'stepPrev',
      ']': 'stepNext',
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

  it('saves on Ctrl/Cmd+S and undoes on Ctrl/Cmd+Z without stopping the event', () => {
    expect(keyAction(k('s', { ctrlKey: true }))).toEqual({ act: 'save', stop: false });
    expect(keyAction(k('S', { metaKey: true }))).toEqual({ act: 'save', stop: false });
    expect(keyAction(k('z', { ctrlKey: true }))).toEqual({ act: 'undo', stop: false });
  });

  it('leaves other keys and modifier combinations alone', () => {
    for (const e of [k('x'), k('Enter'), k('Tab'), k('r', { ctrlKey: true }), k('r', { altKey: true }), k('5')])
      expect(keyAction(e)).toBeNull();
  });

  it('ignores auto-repeat of R, K, Z, F, C, [, ] and Space, but not of seeking or frame steps', () => {
    for (const key of ['r', 'k', 'z', 'f', 'c', '[', ']', ' '])
      expect(keyAction(k(key, { repeat: true }))).toEqual({ act: null, stop: true });
    for (const key of ['ArrowLeft', 'ArrowRight', ',', '.', '1'])
      expect(keyAction(k(key, { repeat: true }))?.act).not.toBeNull();
  });

  it('ignores inherited object keys', () => {
    expect(keyAction(k('constructor'))).toBeNull();
    expect(keyAction(k('toString'))).toBeNull();
  });
});
