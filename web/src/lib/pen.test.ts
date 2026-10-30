import { describe, expect, it } from 'vitest';
import { SHOW, SOLID } from './constants';
import { addPoint, alphaAt, fitFrame, isClick, penWidth } from './pen';
import type { Point } from './types';

describe('stroke visibility', () => {
  const st = { video_t: 10 };
  it('is solid within ±SOLID s of the stroke time', () => {
    expect(SOLID).toBe(0.5);
    for (const vt of [10, 9.5, 10.5, 10.25]) expect(alphaAt(st, vt)).toBe(1);
  });
  it('fades linearly to zero at ±SHOW s', () => {
    expect(SHOW).toBe(1.5);
    expect(alphaAt(st, 11)).toBeCloseTo(0.5);
    expect(alphaAt(st, 9)).toBeCloseTo(0.5);
    expect(alphaAt(st, 10.75)).toBeCloseTo(0.75);
    expect(alphaAt(st, 11.5)).toBeCloseTo(0);
  });
  it('is hidden past ±SHOW s', () => {
    expect(alphaAt(st, 11.51)).toBe(0);
    expect(alphaAt(st, 8.4)).toBe(0);
    expect(alphaAt(st, 0)).toBe(0);
  });
});

describe('pen geometry', () => {
  it('fits the ink layer to the letterboxed frame', () => {
    expect(fitFrame(1920, 1015, 1920, 1080)).toEqual({
      left: (1920 - (1920 * 1015) / 1080) / 2,
      top: 0,
      width: (1920 * 1015) / 1080,
      height: 1015,
    });
    expect(fitFrame(1000, 1000, 16, 9)).toEqual({ left: 0, top: 218.75, width: 1000, height: 562.5 });
  });
  it('draws at 0.65% of the short side, at least 3 px', () => {
    expect(penWidth(1920, 1080)).toBeCloseTo(7.02);
    expect(penWidth(300, 200)).toBe(3);
  });
  it('skips points closer than 2 px to the last one', () => {
    const pts: Point[] = [[0.5, 0.5]];
    expect(addPoint(pts, [0.5005, 0.5], 1000, 1000)).toBe(false);
    expect(addPoint(pts, [0.503, 0.5], 1000, 1000)).toBe(true);
    expect(pts.length).toBe(2);
  });
  it('treats a click or a tiny wobble as no stroke', () => {
    expect(isClick([[0.5, 0.5]], 1000, 500)).toBe(true);
    expect(
      isClick(
        [
          [0.5, 0.5],
          [0.503, 0.505],
        ],
        1000,
        500,
      ),
    ).toBe(true); // 3 x 2.5 px
    expect(
      isClick(
        [
          [0.5, 0.5],
          [0.505, 0.5],
        ],
        1000,
        500,
      ),
    ).toBe(false); // 5 px wide
  });
});
