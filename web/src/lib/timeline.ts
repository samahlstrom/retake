// The timeline canvas: the playhead, where you talked (blue, above the track) and where you drew (ticks below it).
import { BIN, COLORS, TL_PAD, VOICE } from './constants';
import type { HistoryStroke } from './types';
import { clamp } from './util';

/** x of time `t` on a timeline `w` px wide, for a video of `d` seconds. */
export const tlX = (t: number, w: number, d: number): number => TL_PAD + (w - 2 * TL_PAD) * clamp(t / (d || 1), 0, 1);

/** Time under client x, for a timeline whose rect starts at `left` and is `width` px wide. */
export const tlT = (clientX: number, left: number, width: number, d: number): number =>
  clamp((clientX - left - TL_PAD) / (width - 2 * TL_PAD), 0, 1) * d;

function roundRect(g: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number): void {
  if (w <= 0) return;
  g.beginPath();
  g.roundRect(x, y, w, h, Math.min(r, w / 2));
  g.fill();
}

export interface TimelineData {
  /** `video.duration` (0 until known): nothing but the track and playhead is drawn without it. */
  d: number;
  /** For x positions: `video.duration || info.duration || 1`. */
  xd: number;
  vt: number;
  recording: boolean;
  takeBins: Float32Array;
  histBins: Float32Array;
  histStrokes: readonly HistoryStroke[];
  takeStrokes: readonly HistoryStroke[];
  /** The playhead knob's radius, px (default 6; the phone layout's is bigger, for the thumb). */
  knob?: number;
}

export function drawTimeline(g: CanvasRenderingContext2D, w: number, h: number, x: TimelineData): void {
  g.clearRect(0, 0, w, h);
  const y = Math.round(h / 2),
    d = x.d,
    x0 = TL_PAD,
    x1 = w - TL_PAD;
  if (d) {
    // voice above the track: taller and brighter where you talked more
    const cols = Math.max(1, Math.floor(x1 - x0)),
      per = d / BIN / cols;
    g.fillStyle = VOICE;
    for (let c = 0; c < cols; c++) {
      const a = Math.floor(c * per),
        b = Math.max(a + 1, Math.floor((c + 1) * per));
      let sum = 0;
      for (let i = a; i < b; i++) sum += (x.takeBins[i] || 0) + (x.histBins[i] || 0);
      if (sum <= 0.01) continue;
      const k = Math.min(1, sum / (b - a) / 0.5),
        bh = 2 + 7 * k;
      g.globalAlpha = 0.35 + 0.6 * k;
      g.fillRect(x0 + c, y - 5 - bh, 1, bh);
    }
    g.globalAlpha = 1;
  }
  g.fillStyle = 'rgba(255,255,255,.14)';
  roundRect(g, x0, y - 2, x1 - x0, 4, 2);
  g.fillStyle = 'rgba(255,255,255,.82)';
  roundRect(g, x0, y - 2, Math.max(0, tlX(x.vt, w, x.xd) - x0), 4, 2);
  if (d) {
    // pen marks under the track: this take's brighter than earlier ones
    for (const [list, alpha] of [
      [x.histStrokes, 0.6],
      [x.takeStrokes, 0.9],
    ] as const) {
      for (const st of list) {
        if (st.undone) continue;
        g.fillStyle = COLORS[st.color as keyof typeof COLORS] || COLORS.red;
        g.globalAlpha = alpha;
        g.fillRect(Math.round(tlX(st.video_t, w, x.xd)) - 1, y + 6, 2, 7);
      }
    }
    g.globalAlpha = 1;
  }
  g.fillStyle = x.recording ? '#ff6a60' : '#fff';
  g.beginPath();
  g.arc(tlX(x.vt, w, x.xd), y, x.knob ?? 6, 0, Math.PI * 2);
  g.fill();
}
