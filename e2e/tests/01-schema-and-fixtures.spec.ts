// No browser: the take schema accepts a well-formed take and rejects broken ones, and the generated fixtures are what
// the scenario assumes (durations, HLS layout, and fake-mic tones the audio checks can find).
import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { MEDIA, MIC_WAV } from '../lib/env.ts';
import { FULL, MIC_BURSTS, MIC_LENGTH, PREVIEW } from '../lib/fixtures.ts';
import { ANALYSIS_RATE, decodeAudio, findTones, probeDuration } from '../lib/media.ts';
import { run } from '../lib/proc.ts';
import { schemaErrors } from '../lib/schema.ts';

/** A small but complete take, shaped like the legacy player's output. */
function sampleTake() {
  const id = '2026-10-29_20-48-05';
  return {
    session: {
      format: 3, version: 'v6', session: id, created: '2026-10-29T20:48:05-06:00', saved: '2026-10-29T20:48:40-06:00',
      clock: 'session_t = seconds since this take started (first R)', recorded: 8.0,
      video: { path: '/x/v6/clip_v6.mp4', name: 'clip_v6.mp4', duration: 40, fps: 30, width: 1280, height: 720 },
      display: { show_within: 1.5 },
      versions: [{ label: 'v5', path: '/x/v5/clip_v5.mp4', duration: 40 }, { label: 'v6', path: '/x/v6/clip_v6.mp4',
        duration: 40 }],
    },
    segments: [{ id: 'S1', file: 'seg_01.webm', mime: 'audio/webm;codecs=opus', version: 'v6', flip: false, t_start: 0.01,
      t_end: 8.01, recording: false, video_start: 7.9, video_end: 10, video_min: 7.9, video_max: 10 }],
    events: [
      { session_t: 0, video_t: 7.9, event: 'start', playing: false, rate: 1, version: 'v6', base: 'v6', flip: false },
      { session_t: 0.01, video_t: 7.9, event: 'rec_start', seg: 'S1', file: 'seg_01.webm', playing: false, rate: 1,
        version: 'v6', flip: false },
      { session_t: 0.5, video_t: 7.9, event: 'play' },
      { session_t: 2.5, video_t: 9.9, event: 'tick' },
      { session_t: 2.6, video_t: 10, event: 'pause' },
      { session_t: 2.9, video_t: 10, event: 'draw_start', id: 1, seg: 'S1', color: 'red' },
      { session_t: 3.3, video_t: 10, event: 'draw_end', id: 1, seg: 'S1' },
      { session_t: 4, video_t: 10, event: 'seek', from: 10 },
      { session_t: 5, video_t: 5, event: 'version', shown: 'v5', base: 'v6', flip: true },
      { session_t: 6, video_t: 5, event: 'undo', id: 1 },
      { session_t: 8.01, video_t: 10, event: 'rec_stop', seg: 'S1' },
    ],
    strokes: [{ id: 1, seg: 'S1', version: 'v6', flip: false, color: 'red', session_t: 2.9, session_t_end: 3.3,
      video_t: 10, video_t_end: 10, paused: true, points: [[0.35, 0.45], [0.3912, 0.5]] }],
    voice: { bin: 0.25, bins: [[31, 0.25], [32, 0.1]] },
  };
}

type Take = ReturnType<typeof sampleTake>;
const clone = (): Take => structuredClone(sampleTake());

test.describe('take.schema.json', () => {
  test('accepts a complete take, each file on its own, and the /data body', () => {
    const t = clone();
    expect(schemaErrors('take', t)).toEqual([]);
    for (const f of ['session', 'segments', 'events', 'strokes', 'voice'] as const) expect(schemaErrors(f, t[f])).toEqual([]);
    const { versions: _v, ...pageSession } = t.session;
    expect(schemaErrors('dataBody', { ...t, session: { ...pageSession, video: { duration: 40, width: 1280, height: 720 } } }))
      .toEqual([]);
  });

  test('accepts unknown extra fields, empty arrays, null probe values and the new author', () => {
    const t = clone();
    Object.assign(t.session, { author: { name: 'Someone' }, future: { x: 1 } });
    (t.segments[0] as Record<string, unknown>).extra = true;
    (t.events[0] as Record<string, unknown>).extra = 'x';
    (t.strokes[0] as Record<string, unknown>).pressure = [0.5];
    Object.assign(t.voice, { extra: 1 });
    Object.assign(t.session.video, { duration: null, width: null, height: null });
    t.session.versions[0]!.duration = null as unknown as number;
    expect(schemaErrors('take', t)).toEqual([]);
    expect(schemaErrors('segments', [])).toEqual([]);
    expect(schemaErrors('strokes', [])).toEqual([]);
    expect(schemaErrors('events', [])).toEqual([]);
    expect(schemaErrors('voice', { bin: 0.25, bins: [] })).toEqual([]);
    expect(schemaErrors('events', [{ session_t: 1, video_t: 39.9, event: 'ended' }])).toEqual([]);
  });

  const broken: [string, (t: Take) => void][] = [
    ['session without versions (server fields missing)', (t) => { delete (t.session as Partial<Take['session']>).versions; }],
    ['session with a bad take id', (t) => { t.session.session = '2026-10-29 20:48'; }],
    ['session with a time without offset', (t) => { t.session.created = '2026-10-29T20:48:05'; }],
    ['session of another format', (t) => { t.session.format = 2; }],
    ['session.video without fps', (t) => { delete (t.session.video as Partial<Take['session']['video']>).fps; }],
    ['author without a name', (t) => { Object.assign(t.session, { author: {} }); }],
    ['segment without t_end', (t) => { delete (t.segments[0] as Partial<Take['segments'][0]>).t_end; }],
    ['segment with a bad file name', (t) => { t.segments[0]!.file = '../seg_01.webm'; }],
    ['segment with a bad id', (t) => { t.segments[0]!.id = 'seg1'; }],
    ['segment flip as a string', (t) => { (t.segments[0] as Record<string, unknown>).flip = 'false'; }],
    ['negative session_t', (t) => { t.segments[0]!.t_start = -1; }],
    ['unknown event kind', (t) => { t.events[2]!.event = 'scrub'; }],
    ['rec_start without seg', (t) => { delete (t.events[1] as Record<string, unknown>).seg; }],
    ['seek without from', (t) => { delete (t.events[7] as Record<string, unknown>).from; }],
    ['version event without shown', (t) => { delete (t.events[8] as Record<string, unknown>).shown; }],
    ['draw_start without color', (t) => { delete (t.events[5] as Record<string, unknown>).color; }],
    ['stroke of an unknown color', (t) => { t.strokes[0]!.color = 'blue'; }],
    ['stroke point outside the frame', (t) => { t.strokes[0]!.points[1] = [1.2, 0.5]; }],
    ['stroke point with three numbers', (t) => { (t.strokes[0]!.points as number[][])[0] = [0.1, 0.2, 0.3]; }],
    ['stroke without points', (t) => { t.strokes[0]!.points = []; }],
    ['stroke without seg', (t) => { delete (t.strokes[0] as Partial<Take['strokes'][0]>).seg; }],
    ['voice without bins', (t) => { delete (t.voice as Partial<Take['voice']>).bins; }],
    ['voice bin index not an integer', (t) => { t.voice.bins[0] = [1.5, 0.2]; }],
  ];
  for (const [name, breakIt] of broken) {
    test(`rejects ${name}`, () => {
      const t = clone();
      breakIt(t);
      expect(schemaErrors('take', t)).not.toEqual([]);
    });
  }
});

test.describe('fixtures', () => {
  test('the version family media have the planned lengths and size', async () => {
    const d = (f: string) => probeDuration(join(MEDIA, f));
    expect(await d('clip_v5.mp4')).toBeCloseTo(FULL, 1);
    expect(await d('clip_v6.mp4')).toBeCloseTo(FULL, 1);
    expect(await d('clip_v7_0-010_preview.mp4')).toBeCloseTo(PREVIEW, 1);
    expect(await d('clip_v5_draft.mp4')).toBeCloseTo(6, 1);
    expect(await d('clip_v8/clip_v8.m3u8')).toBeCloseTo(FULL, 1);
    const v = await run('ffprobe', ['-v', 'error', '-select_streams', 'v:0', '-show_entries',
      'stream=codec_name,width,height,pix_fmt,r_frame_rate', '-of', 'csv=p=0', join(MEDIA, 'clip_v6.mp4')]);
    expect(v.toString().trim()).toBe('h264,1280,720,yuv420p,30/1');
  });

  test('the HLS v8 is an fMP4 playlist whose segment URIs are plain names beside it', () => {
    const m3u8 = readFileSync(join(MEDIA, 'clip_v8', 'clip_v8.m3u8'), 'utf8');
    expect(m3u8).toContain('#EXT-X-MAP:URI="init.mp4"');
    expect(m3u8).toContain('#EXT-X-ENDLIST');
    const uris = m3u8.split('\n').filter((l) => l && !l.startsWith('#'));
    expect(uris.length).toBe(FULL / 4);
    for (const u of uris) expect(u).toMatch(/^seg_\d{3}\.m4s$/);
  });

  test('the fake mic is 48 kHz mono s16, and the analyzer finds each burst at its time and nothing else', async () => {
    const info = await run('ffprobe', ['-v', 'error', '-show_entries', 'stream=sample_rate,channels,codec_name', '-of',
      'csv=p=0', MIC_WAV]);
    expect(info.toString().trim()).toBe('pcm_s16le,48000,1');
    const x = await decodeAudio(MIC_WAV);
    expect(x.length / ANALYSIS_RATE).toBeCloseTo(MIC_LENGTH, 1);
    const { hits, win } = findTones(x, MIC_BURSTS.map((b) => b.hz));
    for (const b of MIC_BURSTS) {
      const h = hits.get(b.hz)!;
      expect(h.length * win, `${b.hz} Hz lasts ${b.len} s`).toBeGreaterThan(b.len - 0.25);
      expect(h[0]!, `${b.hz} Hz starts at ${b.at} s`).toBeGreaterThanOrEqual(b.at - win);
      expect(h[0]!).toBeLessThanOrEqual(b.at + win);
      expect(h[h.length - 1]!).toBeLessThanOrEqual(b.at + b.len);
    }
  });
});
