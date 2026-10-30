import { describe, expect, it } from 'vitest';
import { segDist, strokesHit, sweep, undoTarget } from './eraser';
import type { Point } from './types';

const W = 1000,
  H = 500;
const line = (id: number, a: Point, b: Point, video_t = 10, undone = false) => ({
  id,
  points: [a, b],
  video_t,
  undone,
});

describe('segDist', () => {
  it('measures to the nearest point of the segment in CSS px', () => {
    // A horizontal segment 100..300 px at y 100 px; the point is 20 px below its middle.
    expect(segDist([0.2, 0.24], [0.1, 0.2], [0.3, 0.2], W, H)).toBeCloseTo(20, 6);
    // Past the end: the distance is to the end point.
    expect(segDist([0.33, 0.2], [0.1, 0.2], [0.3, 0.2], W, H)).toBeCloseTo(30, 6);
    // A zero-length segment is a point.
    expect(segDist([0.1, 0.24], [0.1, 0.2], [0.1, 0.2], W, H)).toBeCloseTo(20, 6);
  });
});

describe('strokesHit', () => {
  const strokes = [
    line(1, [0.1, 0.2], [0.3, 0.2]),
    line(2, [0.1, 0.6], [0.3, 0.6]),
    line(3, [0.1, 0.2], [0.3, 0.2], 10, true), // undone
    line(4, [0.1, 0.2], [0.3, 0.2], 30), // far in time: not on screen
  ];
  it('hits only on-screen, not-undone lines within the radius', () => {
    expect(strokesHit(strokes, [0.2, 0.21], 10, W, H, 12)).toEqual([1]);
    expect(strokesHit(strokes, [0.2, 0.25], 10, W, H, 12)).toEqual([]); // 25 px away
    expect(strokesHit(strokes, [0.2, 0.25], 10, W, H, 30)).toEqual([1]); // a finger reaches further
    expect(strokesHit(strokes, [0.2, 0.6], 10, W, H, 12)).toEqual([2]);
  });
  it('reaches lines that are fading out but still drawn, and not ones gone from the screen', () => {
    expect(strokesHit(strokes, [0.2, 0.2], 11.2, W, H, 12)).toEqual([1]); // 1.2 s away: fading
    expect(strokesHit(strokes, [0.2, 0.2], 12, W, H, 12)).toEqual([]); // 2 s away: gone
  });
});

describe('undoTarget', () => {
  const take = (strokeIds: number[], erased: { id: number; seq: number }[], undone: number[] = []) => ({
    strokes: strokeIds.map((id) => ({ ...line(id, [0, 0], [1, 1]), undone: undone.includes(id) })) as never,
    images: [],
    erased,
  });
  it('undoes the last line when nothing was erased', () => {
    expect(undoTarget(take([1, 2], []))).toMatchObject({ kind: 'stroke', item: { id: 2 } });
  });
  it('brings back the last erased line when the erase came after the last line drawn', () => {
    // Lines 1 and 2 drawn (next id 3), then 1 erased.
    expect(undoTarget(take([1, 2], [{ id: 1, seq: 3 }], [1]))).toEqual({ kind: 'unerase', mark: { id: 1, seq: 3 } });
  });
  it('undoes a line drawn after the erase first', () => {
    // 1 and 2 drawn, 1 erased (next id 3), then 3 drawn: Z removes 3 first.
    expect(undoTarget(take([1, 2, 3], [{ id: 1, seq: 3 }], [1]))).toMatchObject({ kind: 'stroke', item: { id: 3 } });
  });
  it('does nothing on an empty take', () => {
    expect(undoTarget(take([], []))).toBeNull();
  });
});

describe('sweep', () => {
  it('fills a fast swipe so no line between two pointer events is skipped', () => {
    const pts = sweep([0.3, 0.2], [0.3, 0.7], W, H, 6); // 250 px in steps of at most 6 px
    expect(pts[0]).toEqual([0.3, 0.2]);
    expect(pts[pts.length - 1]).toEqual([0.3, 0.7]);
    for (let i = 1; i < pts.length; i++) expect((pts[i]![1] - pts[i - 1]![1]) * H).toBeLessThanOrEqual(6.0001);
    // A line at y 0.3 (150 px) the two events jump over is still hit.
    const hits = pts.flatMap((p) => strokesHit([line(1, [0.2, 0.3], [0.5, 0.3])], p, 10, W, H, 12));
    expect(hits).toContain(1);
  });
  it('is one point when the pointer did not move', () => {
    expect(sweep([0.5, 0.5], [0.5, 0.5], W, H, 6)).toEqual([
      [0.5, 0.5],
      [0.5, 0.5],
    ]);
  });
});
