import { describe, expect, it } from 'vitest';
import { TL_PAD } from './constants';
import { drawTimeline, TRACK, TRACK_PENDING, tlT, tlX, type TimelineData } from './timeline';

/** A 2D context that records the rounded rectangles it fills (the track and the played part) with their color. */
function recorder() {
  const rects: { fill: string; x: number; w: number }[] = [];
  let fill = '';
  let path: { x: number; w: number } | null = null;
  const g = {
    set fillStyle(v: string) {
      fill = v;
    },
    get fillStyle() {
      return fill;
    },
    globalAlpha: 1,
    clearRect() {},
    fillRect() {},
    beginPath() {
      path = null;
    },
    roundRect(x: number, _y: number, w: number) {
      path = { x, w };
    },
    arc() {
      path = null;
    },
    fill() {
      if (path) rects.push({ fill, ...path });
    },
  };
  return { g: g as unknown as CanvasRenderingContext2D, rects };
}

const base: TimelineData = {
  d: 100,
  xd: 100,
  vt: 10,
  recording: false,
  takeBins: new Float32Array(1),
  histBins: new Float32Array(1),
  histStrokes: [],
  takeStrokes: [],
};
const W = 1000 + 2 * TL_PAD;

describe('the timeline', () => {
  it('maps time to x and back over the padded track', () => {
    expect(tlX(50, W, 100)).toBe(TL_PAD + 500);
    expect(tlT(TL_PAD + 250, 0, W, 100)).toBe(25);
  });

  it('draws the whole track for a finished version', () => {
    const { g, rects } = recorder();
    drawTimeline(g, W, 20, base);
    expect(rects[0]).toEqual({ fill: TRACK, x: TL_PAD, w: 1000 });
    expect(rects.some((r) => r.fill === TRACK_PENDING)).toBe(false);
  });

  it('draws what a streamed render has so far brighter than the rest, on the expected scale', () => {
    const { g, rects } = recorder();
    drawTimeline(g, W, 20, { ...base, rendered: 41 });
    expect(rects[0]).toEqual({ fill: TRACK_PENDING, x: TL_PAD, w: 1000 }); // the whole expected length, faint
    expect(rects[1]).toEqual({ fill: TRACK, x: TL_PAD, w: 410 }); // rendered so far
    expect(rects[2]!.w).toBeCloseTo(100); // the played part on top, at the same scale
  });
});
