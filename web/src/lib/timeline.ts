// The timeline canvas: the playhead, where you talked (blue, above the track) and where you drew (ticks below it).
import { BIN, COLORS, TL_PAD, VOICE } from './constants';
import type { HistoryStroke } from './types';
import { clamp } from './util';

/** x of time `t` on a timeline `w` px wide, for a video of `d` seconds. */
export const tlX = (t: number, w: number, d: number): number => TL_PAD + (w - 2 * TL_PAD) * clamp(t / (d || 1), 0, 1);

/** Time under client x, for a timeline whose rect starts at `left` and is `width` px wide. */
export const tlT = (clientX: number, left: number, width: number, d: number): number =>
  clamp((clientX - left - TL_PAD) / (width - 2 * TL_PAD), 0, 1) * d;

/** Audio from one take, optionally isolated to one recorded clip. */
export interface VoiceTrack {
  take: string;
  seg?: string;
  bins: Float32Array;
  active?: boolean;
  transcribed?: boolean;
}

export interface TranscribedComment {
  take?: string;
  seg?: string;
  t0: number;
  t1: number;
  text: string;
  kind: string;
}

/** Replace only the audio that became a comment. Unrelated recordings at the same time stay visible. */
export function pendingVoiceBins(tracks: readonly VoiceTrack[], comments: readonly TranscribedComment[]): Float32Array {
  const size = tracks.reduce((n, track) => Math.max(n, track.bins.length), 0);
  const out = new Float32Array(size);
  for (const track of tracks) {
    if (track.transcribed && !track.active) continue;
    const matches = track.active
      ? []
      : comments.filter((c) => c.kind === 'note' && c.text.trim() && c.take === track.take);
    if (track.seg && matches.some((c) => c.seg === track.seg)) continue;
    const covered = new Uint8Array(size);
    // Old takes have one aggregate waveform; replace only the transcribed time ranges of that same take.
    if (!track.seg)
      for (const c of matches) {
        const start = Math.max(0, Math.floor(c.t0 / BIN));
        const end = Math.min(size, Math.floor(c.t1 / BIN) + 1);
        covered.fill(1, start, Math.max(start, end));
      }
    for (let i = 0; i < track.bins.length; i++) if (!covered[i]) out[i] += track.bins[i];
  }
  return out;
}

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
  /** A streamed render still filling in: the seconds rendered so far. The track after it is drawn fainter. */
  rendered?: number | null;
}

/** The track's fill before and after what a streamed render has rendered so far. */
export const TRACK = 'rgba(255,255,255,.14)';
export const TRACK_PENDING = 'rgba(255,255,255,.05)';

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
  if (x.rendered != null) {
    // a rendering version: the part it has rendered, then the rest, fainter, filling in as it renders
    const xr = tlX(x.rendered, w, x.xd);
    g.fillStyle = TRACK_PENDING;
    roundRect(g, x0, y - 2, x1 - x0, 4, 2);
    g.fillStyle = TRACK;
    roundRect(g, x0, y - 2, Math.max(0, xr - x0), 4, 2);
  } else {
    g.fillStyle = TRACK;
    roundRect(g, x0, y - 2, x1 - x0, 4, 2);
  }
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

// ---------------------------------------------------------------- one row of markers
/** A stroke belongs to a comment of its take when it falls within this many seconds of the comment's span. */
export const COVER = 2.5;

/** The stroke `st` was drawn for comment `row`: same take (a stroke of the take in progress has none: `current`),
 * and within COVER s of the comment. */
export function covers(
  row: { take?: string; t0: number; t1: number },
  st: { take?: string; video_t: number },
  current: string | null,
): boolean {
  const take = st.take ?? current;
  return !!row.take && row.take === take && st.video_t >= row.t0 - COVER && st.video_t <= row.t1 + COVER;
}

/**
 * The pen ticks the timeline still draws: only strokes no comment covers yet (the take being recorded, before it is
 * transcribed; old takes without notes). Once a comment covers a stroke, the comment's marker stands for both, so the
 * timeline has one row of markers instead of a dot and a tick for the same moment.
 */
export function pendingStrokes<T extends { take?: string; video_t: number }>(
  strokes: readonly T[],
  rows: readonly { take?: string; t0: number; t1: number }[],
  current: string | null,
): T[] {
  return strokes.filter((st) => !rows.some((row) => covers(row, st, current)));
}
