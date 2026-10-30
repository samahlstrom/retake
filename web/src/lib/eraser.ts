// The eraser (E, or the eraser button): while recording, a swipe over a line of this take removes that whole line.
// Only lines on screen now can be erased, and only this take's (earlier takes are history). Z brings back the last
// erased line when the erase came after the last thing drawn.

import { alphaAt } from './pen';
import { lastUndoable } from './images';
import type { Point, Stroke, Take } from './types';

/** An erase, for Z: which stroke, and the take's next id at that moment (anything drawn later has an id >= seq). */
export interface EraseMark {
  id: number;
  seq: number;
}

/** How close (CSS px) the eraser must come to a line: a finger is wider than a mouse. */
export const ERASE_RADIUS = { mouse: 12, touch: 22 };

/** Distance in CSS px from `p` to the segment a–b. Points are normalized to the frame, which is W×H CSS px. */
export function segDist(p: Point, a: Point, b: Point, W: number, H: number): number {
  const px = p[0] * W,
    py = p[1] * H,
    ax = a[0] * W,
    ay = a[1] * H,
    bx = b[0] * W,
    by = b[1] * H;
  const dx = bx - ax,
    dy = by - ay,
    len2 = dx * dx + dy * dy;
  const k = len2 > 0 ? Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / len2)) : 0;
  return Math.hypot(px - (ax + k * dx), py - (ay + k * dy));
}

/** The ids of the strokes the eraser at `p` touches: on screen at `vt`, not undone, within `radius` of the line. */
export function strokesHit(
  strokes: Iterable<Pick<Stroke, 'id' | 'points' | 'video_t' | 'undone'>>,
  p: Point,
  vt: number,
  W: number,
  H: number,
  radius: number,
): number[] {
  const hit: number[] = [];
  for (const st of strokes) {
    if (st.undone || alphaAt(st, vt) <= 0.02 || !st.points.length) continue;
    const pts = st.points;
    let near = pts.length === 1 && segDist(p, pts[0], pts[0], W, H) <= radius;
    for (let i = 0; !near && i + 1 < pts.length; i++) near = segDist(p, pts[i], pts[i + 1], W, H) <= radius;
    if (near) hit.push(st.id);
  }
  return hit;
}

/** Points from `a` to `b` (both included) at most `step` CSS px apart: the path a swipe covered between two events. */
export function sweep(a: Point, b: Point, W: number, H: number, step: number): Point[] {
  const n = Math.max(1, Math.ceil(Math.hypot((b[0] - a[0]) * W, (b[1] - a[1]) * H) / Math.max(step, 1)));
  const out: Point[] = [];
  for (let i = 0; i <= n; i++) out.push([a[0] + ((b[0] - a[0]) * i) / n, a[1] + ((b[1] - a[1]) * i) / n]);
  return out;
}

/**
 * What Z does next: bring back the last erased stroke when that erase came after the last stroke or image was
 * added, else undo the last stroke or image (as before the eraser), or nothing.
 */
export function undoTarget(
  t: Pick<Take, 'strokes' | 'images' | 'erased'>,
): { kind: 'unerase'; mark: EraseMark } | ReturnType<typeof lastUndoable> {
  const mark = t.erased.length ? t.erased[t.erased.length - 1] : null;
  const last = lastUndoable(t);
  if (mark && (!last || last.item.id < mark.seq)) return { kind: 'unerase', mark };
  return last;
}
