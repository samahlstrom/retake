// The take model: one folder per take. Recording happens only in segments (R on -> R off), each its own audio file.
// session_t = seconds since the take started (first R); a segment's audio starts at its t_start. video_t = playhead.
import { BIN, SHOW } from './constants';
import type { Take, TakeData, TakeEvent, HistoryStroke } from './types';
import { isoLocal, r3, r4 } from './util';

export const CLOCK_NOTE =
  'session_t = seconds since this take started (first R); segment audio files start at their t_start; video_t = the playhead';

/** Number of voice bins for a video of `duration` seconds (two spare bins at the end, as legacy). */
export const binCount = (duration: number): number => Math.ceil(duration / BIN) + 2;

/** `arr`, grown (zero-filled) to at least `n` bins. Returns the same array when it is already long enough. */
export function grow(arr: Float32Array, n: number): Float32Array {
  if (arr.length >= n) return arr;
  const a = new Float32Array(n);
  a.set(arr);
  return a;
}

export function newTake(version: string | null, bins: number): Take {
  return {
    id: null,
    version,
    created: null,
    t0: null,
    events: [],
    strokes: [],
    segments: [],
    nextId: 1,
    dirty: false,
    lastSave: 0,
    saving: null,
    saveAgain: false,
    saveErr: null,
    closed: false,
    bins: new Float32Array(bins),
  };
}

/** Insert an event in page-clock order (later events usually go last). Anything but a tick marks the take dirty. */
export function insertEv(t: Take, e: TakeEvent): TakeEvent {
  let i = t.events.length;
  while (i > 0 && t.events[i - 1].p > e.p) i--;
  t.events.splice(i, 0, e);
  if (e.event !== 'tick') t.dirty = true;
  return e;
}

/** Earlier marks stay on the timeline: a closed take's strokes and voice join the history. */
export function archive(t: Take, hist: { strokes: HistoryStroke[]; bins: Float32Array }, bins: number): void {
  for (const st of t.strokes) if (!st.undone) hist.strokes.push(st);
  hist.bins = grow(hist.bins, bins);
  t.bins.forEach((v, i) => {
    if (v && i < hist.bins.length) hist.bins[i] += v;
  });
}

/** What serialize needs from the player at save time. */
export interface SaveClock {
  /** Page clock now (seconds), for segments still recording. */
  now: number;
  /** The playhead, for a segment still recording. */
  videoTime: number;
  /** `video.duration || info.duration`. */
  duration: number;
  width: number;
  height: number;
  /** Wall clock for `saved`. */
  date: Date;
}

/** The POST /data body. Same keys, order and rounding as the legacy player's serialize() (format 3). */
export function serialize(t: Take, c: SaveClock): TakeData {
  const t0 = t.t0 ?? 0;
  const st = (p: number) => r3(p - t0);
  const events = t.events.map((e) => {
    const { p, video_t, event, ...rest } = e;
    return { session_t: st(p), video_t: r3(video_t), event, ...rest };
  });
  const strokes = t.strokes
    .filter((x) => !x.undone)
    .map((x) => ({
      id: x.id,
      seg: x.seg,
      version: x.version,
      flip: x.flip,
      color: x.color,
      session_t: st(x.p0),
      session_t_end: st(x.p1 as number),
      video_t: r3(x.video_t),
      video_t_end: r3(x.video_t_end as number),
      paused: x.paused,
      points: x.points.map(([a, b]) => [r4(a), r4(b)] as [number, number]),
    }));
  const segments = t.segments.map((s) => ({
    id: s.id,
    file: s.file,
    mime: s.mime,
    version: s.version,
    flip: s.flip,
    t_start: st(s.p0),
    t_end: st(s.p1 ?? c.now),
    recording: s.p1 == null,
    video_start: r3(s.v0),
    video_end: r3(s.v1 ?? c.videoTime),
    video_min: r3(s.vmin),
    video_max: r3(s.vmax),
  }));
  const bins: [number, number][] = [];
  t.bins.forEach((v, i) => {
    if (v > 0.001) bins.push([i, r3(v)]);
  });
  return {
    session: {
      format: 3,
      version: t.version,
      session: t.id,
      created: t.created,
      saved: isoLocal(c.date),
      clock: CLOCK_NOTE,
      recorded: r3(t.segments.reduce((a, s) => a + ((s.p1 ?? c.now) - s.p0), 0)),
      video: { duration: c.duration, width: c.width, height: c.height },
      display: { show_within: SHOW },
    },
    segments,
    events,
    strokes,
    voice: { bin: BIN, bins },
  };
}
