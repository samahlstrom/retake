import { describe, expect, it } from 'vitest';
import {
  clampSide,
  loadSideWidth,
  saveSideWidth,
  SIDE_MAX,
  SIDE_MIN,
  SIDE_WIDTH_KEY,
  sideMax,
  sideWidthCss,
  STAGE_MIN,
} from './side-width';

/** A storage stand-in: a map, or one that throws (a blocked localStorage). */
function store(init: Record<string, string> = {}, blocked = false) {
  const m = new Map(Object.entries(init));
  const no = () => {
    throw new Error('blocked');
  };
  return {
    m,
    getItem: (k: string) => (blocked ? no() : (m.get(k) ?? null)),
    setItem: (k: string, v: string) => (blocked ? no() : void m.set(k, v)),
    removeItem: (k: string) => (blocked ? no() : void m.delete(k)),
  };
}

describe('the column’s width', () => {
  it('stays between its bounds and leaves the stage its room', () => {
    expect(clampSide(500, 1440)).toBe(500);
    expect(clampSide(100, 1440)).toBe(SIDE_MIN);
    expect(clampSide(5000, 2560)).toBe(SIDE_MAX);
    // A smaller window: the video keeps STAGE_MIN.
    expect(clampSide(700, 1000)).toBe(1000 - STAGE_MIN);
    expect(sideMax(1000)).toBe(580);
    // Too small for both: the column's least width wins (the layout is a phone's by then).
    expect(clampSide(700, 600)).toBe(SIDE_MIN);
    expect(clampSide(412.6, 1440)).toBe(413);
    expect(clampSide(NaN, 1440)).toBe(SIDE_MIN);
    expect(sideWidthCss(520)).toBe('min(520px, 100vw - 420px)');
  });

  it('is remembered per viewer, and forgotten on reset', () => {
    const s = store();
    expect(loadSideWidth(s)).toBeNull();
    saveSideWidth(s, 520.4);
    expect(s.m.get(SIDE_WIDTH_KEY)).toBe('520');
    expect(loadSideWidth(s)).toBe(520);
    saveSideWidth(s, null);
    expect(s.m.has(SIDE_WIDTH_KEY)).toBe(false);
    expect(loadSideWidth(s)).toBeNull();
  });

  it('reads a bad value as none, and an out-of-bounds one within bounds', () => {
    expect(loadSideWidth(store({ [SIDE_WIDTH_KEY]: 'wide' }))).toBeNull();
    expect(loadSideWidth(store({ [SIDE_WIDTH_KEY]: '' }))).toBeNull();
    expect(loadSideWidth(store({ [SIDE_WIDTH_KEY]: '-4' }))).toBeNull();
    expect(loadSideWidth(store({ [SIDE_WIDTH_KEY]: '90' }))).toBe(SIDE_MIN);
    expect(loadSideWidth(store({ [SIDE_WIDTH_KEY]: '9000' }))).toBe(SIDE_MAX);
  });

  it('never throws without a storage, or with a blocked one', () => {
    expect(loadSideWidth(null)).toBeNull();
    expect(() => saveSideWidth(null, 400)).not.toThrow();
    const blocked = store({}, true);
    expect(loadSideWidth(blocked)).toBeNull();
    expect(() => saveSideWidth(blocked, 400)).not.toThrow();
    expect(() => saveSideWidth(blocked, null)).not.toThrow();
  });
});
