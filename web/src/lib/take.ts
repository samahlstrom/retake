// The take model: one folder per take. Recording happens only in segments (R on -> R off), each its own audio file.
// session_t = seconds since the take started (first R); a segment's audio starts at its t_start. video_t = playhead.
import { BIN, SHOW } from './constants';
import { extFor } from './mic';
import type { HistoryStroke, Segment, Take, TakeData, TakeEvent } from './types';
import { isoLocal, pad2, r3, r4 } from './util';

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

/** A segment's audio file: seg_01.webm, seg_02.m4a, … (the extension follows the recorder's mime type). */
export const segFile = (n: number, mime: string): string => `seg_${pad2(n)}.${extFor(mime)}`;

/** The next segment of take `t`: S1, S2, … with its own audio file seg_01.<ext>, seg_02.<ext>, … */
export function newSegment(
  t: Take,
  rec: MediaRecorder,
  mime: string,
  at: { version: string | null; flip: boolean; videoTime: number },
): Segment {
  const n = t.segments.length + 1;
  return {
    n,
    id: `S${n}`,
    rec,
    mime,
    version: at.version,
    flip: at.flip,
    file: segFile(n, mime),
    chunks: [],
    uploaded: 0,
    p0: 0,
    p1: null,
    v0: at.videoTime,
    v1: null,
    vmin: at.videoTime,
    vmax: at.videoTime,
    size: 0,
    stored: 0,
    persisted: Promise.resolve(),
  };
}

/**
 * The recorder has started and reports its real mime type (Safari picks audio/mp4 only then): the segment and its file
 * name follow it. Only before the first chunk, so every byte lands in one file.
 */
export function settleMime(s: Segment, mime: string): void {
  if (!mime || mime === s.mime || s.size) return;
  s.mime = mime;
  s.file = segFile(s.n, mime);
}

/**
 * A recorded chunk: kept in memory for the upload, and queued for the local buffer at its byte offset. `stored`
 * advances only once the buffer has it, and only stored bytes are uploaded.
 */
export function addChunk(s: Segment, data: Blob, write: (offset: number) => Promise<void>): void {
  s.chunks.push(data);
  const offset = s.size,
    size = data.size;
  s.size += size;
  s.persisted = s.persisted
    .then(() => write(offset))
    .then(() => {
      s.stored = offset + size;
    });
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
