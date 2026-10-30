// The pen layer: strokes are pinned to the video time they started at and drawn over the video frame.
import { COLORS, SHOW, SOLID } from './constants';
import type { HistoryStroke, Point } from './types';

type Ctx = CanvasRenderingContext2D;

/** Opacity of a stroke at playhead `vt`: 1 within ±SOLID s of its time, fading out linearly to 0 at ±SHOW s. */
export function alphaAt(st: Pick<HistoryStroke, 'video_t'>, vt: number): number {
  const d = Math.abs(vt - st.video_t);
  return d > SHOW ? 0 : d <= SOLID ? 1 : 1 - (d - SOLID) / (SHOW - SOLID);
}

/** The ink canvas box: the video's frame, letterboxed inside the stage (object-fit: contain). */
export function fitFrame(sw: number, sh: number, vw: number, vh: number) {
  const sc = Math.min(sw / vw, sh / vh),
    w = vw * sc,
    h = vh * sc;
  return { left: (sw - w) / 2, top: (sh - h) / 2, width: w, height: h };
}

/** Smooth path through the points (quadratic midpoints); a single point becomes a dot. */
export function penPath(g: Ctx, pts: Point[], W: number, H: number): void {
  const P = pts.map(([x, y]) => [x * W, y * H]);
  g.beginPath();
  g.moveTo(P[0][0], P[0][1]);
  if (P.length === 1) g.lineTo(P[0][0] + 0.01, P[0][1]);
  for (let i = 1; i < P.length - 1; i++)
    g.quadraticCurveTo(P[i][0], P[i][1], (P[i][0] + P[i + 1][0]) / 2, (P[i][1] + P[i + 1][1]) / 2);
  if (P.length > 1) g.lineTo(P[P.length - 1][0], P[P.length - 1][1]);
}

/** Line width for a W x H canvas: 0.65% of the short side, at least 3 px. */
export const penWidth = (W: number, H: number): number => Math.max(3, Math.min(W, H) * 0.0065);

/** A flat stroke, no outline. */
export function drawPen(g: Ctx, st: Pick<HistoryStroke, 'points' | 'color'>, W: number, H: number): void {
  if (!st.points.length) return;
  g.lineCap = 'round';
  g.lineJoin = 'round';
  penPath(g, st.points, W, H);
  g.strokeStyle = COLORS[st.color as keyof typeof COLORS] || COLORS.red;
  g.lineWidth = penWidth(W, H);
  g.stroke();
}

/**
 * Draw every visible stroke at playhead `vt`. Earlier marks are dimmer while not recording (`dim` 0.55). A fading
 * stroke is drawn on `layer` first and composited as one piece, so overlapping segments don't double up.
 */
export function renderInk(
  ictx: Ctx,
  lctx: Ctx,
  W: number,
  H: number,
  strokes: Iterable<HistoryStroke>,
  vt: number,
  dim: number,
  cur: Pick<HistoryStroke, 'points' | 'color'> | null,
): void {
  ictx.clearRect(0, 0, W, H);
  for (const st of strokes) {
    if (st.undone) continue;
    const a = alphaAt(st, vt) * dim;
    if (a <= 0.02) continue;
    if (a >= 0.99) {
      drawPen(ictx, st, W, H);
      continue;
    }
    lctx.clearRect(0, 0, W, H);
    drawPen(lctx, st, W, H);
    ictx.globalAlpha = a;
    ictx.drawImage(lctx.canvas, 0, 0);
    ictx.globalAlpha = 1;
  }
  if (cur) drawPen(ictx, cur, W, H);
}

/** Add `p` to a stroke in progress unless it is closer than 2 CSS px to the last point. */
export function addPoint(points: Point[], p: Point, inkW: number, inkH: number): boolean {
  const q = points[points.length - 1];
  if (Math.hypot((p[0] - q[0]) * inkW, (p[1] - q[1]) * inkH) < 2) return false;
  points.push(p);
  return true;
}

/** A click (or a wobble under 4 CSS px either way) draws nothing. */
export function isClick(points: Point[], inkW: number, inkH: number): boolean {
  const xs = points.map((p) => p[0] * inkW),
    ys = points.map((p) => p[1] * inkH);
  return Math.max(Math.max(...xs) - Math.min(...xs), Math.max(...ys) - Math.min(...ys)) < 4;
}
