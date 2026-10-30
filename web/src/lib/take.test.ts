import { describe, expect, it } from 'vitest';
import {
  addChunk,
  archive,
  binCount,
  CLOCK_NOTE,
  grow,
  insertEv,
  newSegment,
  newTake,
  segFile,
  serialize,
  settleMime,
} from './take';
import type { Stroke, Take } from './types';

const rec = {} as MediaRecorder;

/** A take with two segments (S2 still recording), two strokes (one undone) and some voice. */
function sampleTake(): Take {
  const t = newTake('v6', binCount(40));
  t.id = '2026-10-29_20-41-05';
  t.created = '2026-10-29T20:41:05+02:00';
  t.t0 = 100;
  insertEv(t, {
    p: 100,
    video_t: 8.19734,
    event: 'start',
    playing: false,
    rate: 1,
    version: 'v6',
    base: 'v6',
    flip: false,
  });
  const s1 = newSegment(t, rec, 'audio/webm;codecs=opus', { version: 'v6', flip: false, videoTime: 8.1973 });
  s1.p0 = 100.0491;
  s1.p1 = 108.1244;
  s1.v1 = 10.2391;
  s1.vmax = 10.2391;
  t.segments.push(s1);
  insertEv(t, { p: 100.0491, video_t: 8.1973, event: 'rec_start', seg: 'S1', file: s1.file });
  const s2 = newSegment(t, rec, 'audio/ogg;codecs=opus', { version: 'v7', flip: true, videoTime: 5 });
  s2.p0 = 110;
  t.segments.push(s2);
  const st = (id: number, undone = false): Stroke => ({
    id,
    seg: 'S1',
    version: 'v6',
    flip: false,
    color: 'red',
    p0: 101.23456,
    p1: 102.5,
    video_t: 9.87654,
    video_t_end: 9.87654,
    paused: true,
    points: [
      [0.123456, 0.654321],
      [0.2, 0.3],
    ],
    undone,
  });
  t.strokes.push(st(1), st(2, true));
  t.bins[40] = 0.31234;
  t.bins[41] = 0.0005; // below the 0.001 floor: not saved
  return t;
}

const clock = {
  now: 112.5,
  videoTime: 7.25,
  duration: 40,
  width: 1920,
  height: 1080,
  date: new Date(2026, 9, 29, 20, 42),
};

describe('serialize (the POST /data body)', () => {
  it('has the legacy keys, in the legacy order, with format 3', () => {
    const d = serialize(sampleTake(), clock);
    expect(Object.keys(d)).toEqual(['session', 'segments', 'events', 'strokes', 'voice']);
    expect(Object.keys(d.session)).toEqual([
      'format',
      'version',
      'session',
      'created',
      'saved',
      'clock',
      'recorded',
      'video',
      'display',
    ]);
    expect(d.session.format).toBe(3);
    expect(d.session.version).toBe('v6');
    expect(d.session.session).toBe('2026-10-29_20-41-05');
    expect(d.session.clock).toBe(CLOCK_NOTE);
    expect(d.session.video).toEqual({ duration: 40, width: 1920, height: 1080 });
    expect(Object.keys(d.session.video)).toEqual(['duration', 'width', 'height']);
    expect(d.session.display).toEqual({ show_within: 1.5 });
    expect(d.session.saved).toMatch(/^2026-10-29T20:42:00[+-]\d{2}:\d{2}$/);
    expect(Object.keys(d.segments[0])).toEqual([
      'id',
      'file',
      'mime',
      'version',
      'flip',
      't_start',
      't_end',
      'recording',
      'video_start',
      'video_end',
      'video_min',
      'video_max',
    ]);
    expect(Object.keys(d.strokes[0])).toEqual([
      'id',
      'seg',
      'version',
      'flip',
      'color',
      'session_t',
      'session_t_end',
      'video_t',
      'video_t_end',
      'paused',
      'points',
    ]);
    expect(Object.keys(d.voice)).toEqual(['bin', 'bins']);
  });

  it('rounds times to 3 decimals and points to 4', () => {
    const d = serialize(sampleTake(), clock);
    expect(d.strokes[0]).toMatchObject({ session_t: 1.235, session_t_end: 2.5, video_t: 9.877, video_t_end: 9.877 });
    expect(d.strokes[0].points).toEqual([
      [0.1235, 0.6543],
      [0.2, 0.3],
    ]);
    expect(d.segments[0]).toMatchObject({ t_start: 0.049, t_end: 8.124, video_start: 8.197, video_end: 10.239 });
    expect(d.events[1]).toEqual({
      session_t: 0.049,
      video_t: 8.197,
      event: 'rec_start',
      seg: 'S1',
      file: 'seg_01.webm',
    });
  });

  it('turns page-clock events into session_t and keeps their extra fields in order', () => {
    const d = serialize(sampleTake(), clock);
    expect(d.events[0]).toEqual({
      session_t: 0,
      video_t: 8.197,
      event: 'start',
      playing: false,
      rate: 1,
      version: 'v6',
      base: 'v6',
      flip: false,
    });
    expect(Object.keys(d.events[0])).toEqual([
      'session_t',
      'video_t',
      'event',
      'playing',
      'rate',
      'version',
      'base',
      'flip',
    ]);
  });

  it('leaves undone strokes out', () => {
    const d = serialize(sampleTake(), clock);
    expect(d.strokes.map((s) => s.id)).toEqual([1]);
  });

  it('closes a segment still recording at now and the playhead, and flags it', () => {
    const d = serialize(sampleTake(), clock);
    expect(d.segments[1]).toEqual({
      id: 'S2',
      file: 'seg_02.ogg',
      mime: 'audio/ogg;codecs=opus',
      version: 'v7',
      flip: true,
      t_start: 10,
      t_end: 12.5,
      recording: true,
      video_start: 5,
      video_end: 7.25,
      video_min: 5,
      video_max: 5,
    });
    expect(d.segments[0].recording).toBe(false);
    expect(d.session.recorded).toBe(10.575); // 8.0753 + 2.5
  });

  it('saves voice bins above 0.001 as [index, level]', () => {
    const d = serialize(sampleTake(), clock);
    expect(d.voice).toEqual({ bin: 0.25, bins: [[40, 0.312]] });
  });

  it('round-trips through JSON unchanged', () => {
    const d = serialize(sampleTake(), clock);
    expect(JSON.parse(JSON.stringify(d))).toEqual(d);
  });
});

describe('take bookkeeping', () => {
  it('starts empty, with bins for the duration plus two', () => {
    const t = newTake('v6', binCount(40));
    expect(t).toMatchObject({ id: null, version: 'v6', nextId: 1, dirty: false, closed: false, saving: null });
    expect(t.bins.length).toBe(162);
    expect(binCount(1)).toBe(6);
    expect(binCount(10.1)).toBe(43);
  });

  it('inserts events in clock order; ticks alone do not dirty the take', () => {
    const t = newTake('v6', 6);
    insertEv(t, { p: 5, video_t: 0, event: 'tick' });
    expect(t.dirty).toBe(false);
    insertEv(t, { p: 9, video_t: 0, event: 'play' });
    insertEv(t, { p: 7, video_t: 0, event: 'draw_start' });
    expect(t.events.map((e) => e.event)).toEqual(['tick', 'draw_start', 'play']);
    expect(t.dirty).toBe(true);
  });

  it('grows bins without losing data, and only when needed', () => {
    const a = new Float32Array([1, 2]);
    expect(grow(a, 2)).toBe(a);
    const b = grow(a, 5);
    expect(Array.from(b)).toEqual([1, 2, 0, 0, 0]);
  });

  it('archives a closed take into the history (strokes not undone, bins summed)', () => {
    const t = sampleTake();
    const hist = { strokes: [], bins: new Float32Array(41) };
    hist.bins[40] = 1;
    archive(t, hist, binCount(40));
    expect(hist.strokes.length).toBe(1);
    expect(hist.bins.length).toBe(162);
    expect(hist.bins[40]).toBeCloseTo(1.31234, 5);
  });
});

describe('segments', () => {
  it('numbers segments S1, S2 with seg_01, seg_02 files named for the recorder type', () => {
    const t = newTake('v6', 6);
    const a = newSegment(t, rec, 'audio/webm;codecs=opus', { version: 'v6', flip: false, videoTime: 3 });
    t.segments.push(a);
    const b = newSegment(t, rec, 'audio/mp4', { version: 'v7', flip: true, videoTime: 4 });
    expect([a.id, a.file, b.id, b.file]).toEqual(['S1', 'seg_01.webm', 'S2', 'seg_02.m4a']);
    expect(b).toMatchObject({ v0: 4, vmin: 4, vmax: 4, p1: null, v1: null, uploaded: 0, size: 0, stored: 0 });
  });

  it('names segment files for their type: seg_NN.webm, .ogg, .m4a (three digits past 99)', () => {
    expect(segFile(1, 'audio/webm;codecs=opus')).toBe('seg_01.webm');
    expect(segFile(2, 'audio/ogg;codecs=opus')).toBe('seg_02.ogg');
    expect(segFile(3, 'audio/mp4')).toBe('seg_03.m4a');
    expect(segFile(100, 'audio/mp4;codecs=mp4a.40.2')).toBe('seg_100.m4a');
  });

  it("settles the segment's type once the recorder runs (Safari reports audio/mp4 only then)", () => {
    const t = newTake('v6', 6);
    const s = newSegment(t, rec, 'audio/webm', { version: 'v6', flip: false, videoTime: 0 });
    settleMime(s, ''); // still unknown: nothing changes
    expect([s.mime, s.file]).toEqual(['audio/webm', 'seg_01.webm']);
    settleMime(s, 'audio/mp4');
    expect([s.mime, s.file]).toEqual(['audio/mp4', 'seg_01.m4a']);
    settleMime(s, 'audio/mp4;codecs=mp4a.40.2'); // a fuller name for the same type
    expect([s.mime, s.file]).toEqual(['audio/mp4;codecs=mp4a.40.2', 'seg_01.m4a']);
  });

  it('never renames a segment once it has audio (every byte stays in one file)', () => {
    const t = newTake('v6', 6);
    const s = newSegment(t, rec, 'audio/webm;codecs=opus', { version: 'v6', flip: false, videoTime: 0 });
    addChunk(s, new Blob([new Uint8Array(4)]), async () => {});
    settleMime(s, 'audio/mp4');
    expect([s.mime, s.file]).toEqual(['audio/webm;codecs=opus', 'seg_01.webm']);
  });

  it('tracks chunk offsets, and counts bytes as stored only once the buffer has them', async () => {
    const t = newTake('v6', 6);
    const s = newSegment(t, rec, 'audio/webm', { version: 'v6', flip: false, videoTime: 0 });
    const writes: number[] = [];
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    addChunk(s, new Blob([new Uint8Array(10)]), async (o) => {
      await gate;
      writes.push(o);
    });
    addChunk(s, new Blob([new Uint8Array(5)]), async (o) => {
      writes.push(o);
    });
    expect(s.size).toBe(15);
    expect(s.chunks.length).toBe(2);
    expect(s.stored).toBe(0);
    release();
    await s.persisted;
    expect(writes).toEqual([0, 10]); // in order, even when the first write is slower
    expect(s.stored).toBe(15);
  });
});
