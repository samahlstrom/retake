// The legacy end-to-end scenario (tests/e2e_headless.mjs + check_e2e.py of the Python player), ported to Playwright,
// plus on-disk checks of the take and schema validation. It runs against either server (RETAKE_SERVER).
//
//  A:  play WITHOUT recording, drag on the video: no take starts. On the legacy player nothing is drawn; on this one
//      the line starts a typed comment there and the video pauses (spec/notes.md, "Typed comments on a video"), and
//      Esc cancels it.
//  S1: R on, talk while playing, pause, draw (red), talk, R off. Then an unrecorded loop of the same clip, with speech
//      and a drag that must both be dropped.
//  S2: R on, talk, R off. C flips v6 to its partner at the same frame, and back: v7 (the newer 0:10 preview) on the
//      legacy player; v5 (the previous version) on this one, which opens the newest version, so its family has no
//      preview past v6.
//  S3: flipped to the partner, R on, talk, R off, C back to v6.   S4: R on, talk, draw (green), R off.   Done.
//  Versions: on the legacy player [ ] keep time and play state, and ] and C past the preview's end are refused; on
//  this one [ and ] are not keys.
//  Reload: the take's strokes come back as history.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { AUTHOR, LEGACY, PORTS } from '../lib/env.ts';
import { MIC_BURSTS } from '../lib/fixtures.ts';
import { line, loop, READY, Review, Schedule, sleep, type State } from '../lib/review.ts';
import { assertValid, validateTakeDir } from '../lib/schema.ts';
import { startServer, type Server } from '../lib/server.ts';
import { analyzeSegment, readTake, type Segment, type SegmentAudio, type Take } from '../lib/take.ts';
import { allTakeDirs, family, takeDirs, type Family } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });

let fam: Family;
let server: Server;
let page: Page;
let ui: Review;
const pageErrors: string[] = [];
const marks: Record<string, State> = {};
const c: {
  afterUnrecordedDrag?: State; afterUnrecordedLoopDrag?: State;
  flip?: { before: State; after: State }; flipBack?: State;
  stepPrev?: { before: State; after: State }; stepBack?: State; stepPastEnd?: State; stepIntoPreview?: State;
  stepBackFromPreview?: State; flipPastEnd?: State;
  saved?: { segments: Segment[]; strokes: Take['strokes']; events: Take['events']; [k: string]: unknown };
} = {};
let take: Take;
let audio: SegmentAudio[] = [];

const recording = () => !!window.__review.seg;
/** Where C goes from v6: the newer preview on the legacy player, the previous version here. */
const PARTNER = LEGACY ? 'v7' : 'v5';
const v6Review = () => join(fam.dir, 'v6', 'clip_v6.review');

async function shot(info: TestInfo, name: string): Promise<void> {
  await info.attach(name, { body: await ui.screenshot(), contentType: 'image/png' });
}

test.beforeAll(async ({ browser }) => {
  fam = family('scenario', LEGACY ? ['v5', 'v6', 'v7'] : ['v5', 'v6']);
  server = await startServer('scenario', fam.files.v6!, PORTS.scenario);
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  await context.grantPermissions(['microphone'], { origin: server.url.replace(/\/$/, '') });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  ui = new Review(page);
});

test.afterAll(async () => {
  await page?.context().close();
  await server?.stop();
});

test('family: v5, v6 and the v7 preview; the newer v5 draft decoy is skipped', async () => {
  await ui.open(server.url);
  await sleep(400);
  const info = await page.evaluate(() => {
    const i = window.__review.info;
    return { label: i.label, name: i.name, versions: (i.versions ?? []).map((v) => [v.label, v.name, v.duration]) };
  });
  expect(info.label).toBe('v6');
  expect(info.name).toBe('clip_v6.mp4');
  expect(info.versions.map(([l, n]) => [l, n])).toEqual([
    ['v5', 'clip_v5.mp4'], ['v6', 'clip_v6.mp4'], ...(LEGACY ? [['v7', 'clip_v7_0-010_preview.mp4']] : [])]);
  const d = Object.fromEntries(info.versions.map(([l, , dur]) => [l, dur as number]));
  expect(d.v5).toBeCloseTo(40, 0);
  expect(d.v6).toBeCloseTo(40, 0);
  if (LEGACY) expect(d.v7).toBeCloseTo(10, 0);
});

test('A: without recording, playing works and the pen makes no take', async ({}, info) => {
  await ui.key(' ');
  await sleep(1200);
  await ui.drag(loop(0.5, 0.5));
  await sleep(300);
  await shot(info, 'ui_not_recording.png');
  if (!LEGACY) {
    expect(await page.evaluate(() => window.__review.draft?.lines ?? 0), 'the line is a typed comment’s').toBe(1);
    await ui.key('Escape'); // dropped, with its line
  }
  c.afterUnrecordedDrag = await ui.state();
  await sleep(1000);
  if (LEGACY) await ui.key(' ');
  await sleep(200);
  await ui.key('ArrowRight');
  await sleep(400);

  const a = c.afterUnrecordedDrag;
  expect(a.take, 'no take before the first R').toBeNull();
  expect(a.strokes).toBe(0);
  expect(a.rec).toBe(false);
  expect(a.t).toBeGreaterThan(1);
  if (LEGACY) {
    expect(a.toast).toContain('Press R');
    expect(a.paused, 'still playing').toBe(false);
  } else {
    // The line went to a typed comment, which paused the video; Esc dropped it with its line.
    expect(a.paused, 'a comment pauses the video').toBe(true);
    expect(await page.evaluate(() => window.__review.draft ?? null)).toBeNull();
  }
});

test('S1-S4: R-gated recording (the pen pinned to it), an unrecorded loop, C flips, then Done', async ({}, info) => {
  const s = new Schedule();
  const mark = async (label: string) => { marks[label] = await ui.state(); };
  // S1. The fake mic starts with the first R: its schedule is relative to this moment.
  await ui.key('r');
  await ui.waitFor(recording, 8000);
  s.start();
  await mark('S1 on');
  await s.at(0.5); await ui.key(' ');
  await s.at(2.6); await ui.key(' ');
  await s.at(2.9); await ui.key('1'); await ui.drag(loop(0.35, 0.45)); await mark('S1 stroke');
  await s.at(4.2); await shot(info, 'ui_recording.png');
  await s.at(8.0); await ui.key('r'); await mark('S1 off');
  // The same clip again, unrecorded: the speech at 9.5 s and this drag must be dropped.
  await s.at(8.5); await ui.key('ArrowLeft'); await s.at(8.7); await ui.key(' ');
  await s.at(10.5); await ui.drag(loop(0.6, 0.45)); c.afterUnrecordedLoopDrag = await ui.state();
  // This page: the drag started a typed comment (which paused the video); Esc drops it, and the video stays paused.
  if (!LEGACY) await ui.key('Escape');
  await s.at(12.0); if (LEGACY) await ui.key(' '); await mark('loop paused');
  // S2
  await s.at(12.5); await ui.key('r'); await ui.waitFor(recording); await mark('S2 on');
  await s.at(15.5); await ui.key('r'); await mark('S2 off');
  // C flips to the partner and back, paused: the same frame time.
  await s.at(16.0);
  const before = await ui.state();
  await ui.key('c'); await ui.settle(); await sleep(300);
  c.flip = { before, after: await ui.state() };
  await s.at(16.8); await shot(info, 'ui_flipped.png');
  await s.at(17.2); await ui.key('c'); await ui.settle(); c.flipBack = await ui.state();
  // S3 while flipped to the partner
  await s.at(18.0); await ui.key('c'); await ui.settle();
  await s.at(18.6); await ui.key('r'); await ui.waitFor(recording); await mark('S3 on (flipped)');
  await s.at(21.0); await ui.key('r'); await s.at(21.3); await ui.key('c'); await ui.settle();
  await mark('S3 off, back on v6');
  // S4, with a green stroke
  await s.at(22.0); await ui.key('r'); await ui.waitFor(recording); await mark('S4 on');
  await s.at(24.8); await ui.key('3'); await ui.drag(line([0.15, 0.2], [0.35, 0.2])); await mark('S4 stroke');
  await s.at(25.5); await ui.key('r'); await mark('S4 off');
  await s.at(26.0); await ui.click('#done');
  await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 10_000);
  await mark('done');
  c.saved = await page.evaluate(() => window.__review.serialize(window.__review.take)) as typeof c.saved;

  // Recording and pen gating along the way
  expect(marks['S1 on']!.rec).toBe(true);
  expect(marks['S1 on']!.take).toMatch(/^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$/);
  expect(marks['S1 stroke']!.strokes).toBe(1);
  expect(marks['S1 off']!.rec).toBe(false);
  expect(c.afterUnrecordedLoopDrag!.strokes, 'the unrecorded drag drew nothing into the take').toBe(1);
  expect(c.afterUnrecordedLoopDrag!.rec).toBe(false);
  expect(marks['S3 on (flipped)']).toMatchObject({ rec: true, shown: PARTNER, base: 'v6', flipped: true });
  expect(marks['S3 off, back on v6']).toMatchObject({ rec: false, shown: 'v6', flipped: false });
  expect(marks['S4 stroke']!.strokes).toBe(2);
  expect(marks.done!.rec).toBe(false);
  for (const m of ['S2 on', 'S3 on (flipped)', 'S4 on']) expect(marks[m]!.take, m).toBe(marks['S1 on']!.take);

  // C flips to the partner at the same frame, then back
  const { before: f0, after: f1 } = c.flip;
  expect(f1).toMatchObject({ shown: PARTNER, flipped: true, base: 'v6', paused: true });
  expect(Math.abs(f1.t - f0.t)).toBeLessThan(0.04);
  expect(c.flipBack).toMatchObject({ shown: 'v6', flipped: false });

  // The take as the page sees it: 4 segments on v6 (S3 while flipped to the partner), 2 strokes
  const sv = c.saved!;
  expect(sv.segments.map((x) => x.id)).toEqual(['S1', 'S2', 'S3', 'S4']);
  expect(sv.segments.map((x) => [x.version, x.flip])).toEqual([['v6', false], ['v6', false], [PARTNER, true],
    ['v6', false]]);
  expect(sv.strokes.map((x) => [x.id, x.seg, x.color, x.version, x.flip])).toEqual([
    [1, 'S1', 'red', 'v6', false], [2, 'S4', 'green', 'v6', false]]);
  assertValid('dataBody', sv, 'serialize(take) at Done');
});

test('[ ] keep time and play state; ] and C past the preview\'s end are refused', async () => {
  if (!LEGACY) {
    // [ and ] are not keys here: the version picker and the comments' version strip switch versions.
    const before = await ui.state();
    await ui.key('['); await ui.key(']'); await sleep(300);
    expect(await ui.state()).toMatchObject({ shown: before.shown, base: before.base, flipped: false });
    return;
  }
  await ui.key(' ');
  await sleep(1500);
  const b1 = await ui.state();
  await ui.key('['); await ui.settle(); await sleep(150);
  c.stepPrev = { before: b1, after: await ui.state() };
  await sleep(500);
  await ui.key(']'); await ui.settle(); c.stepBack = await ui.state();
  await ui.waitFor(() => document.querySelector('video')!.currentTime > 10.3, 8000);
  await ui.key(']'); await sleep(400); c.stepPastEnd = await ui.state();
  await ui.key(' '); await sleep(200); await ui.key('ArrowLeft'); await sleep(300);
  await ui.key(']'); await ui.settle(); await sleep(200); c.stepIntoPreview = await ui.state();
  await ui.key('['); await ui.settle(); c.stepBackFromPreview = await ui.state();
  await ui.key(' ');
  await ui.waitFor(() => document.querySelector('video')!.currentTime > 10.3, 9000);
  await ui.key('c'); await sleep(400); c.flipPastEnd = await ui.state();
  await ui.key(' ');

  const { before: s0, after: s1 } = c.stepPrev;
  expect(s1).toMatchObject({ base: 'v5', shown: 'v5', paused: false });
  expect(Math.abs(s1.t - s0.t)).toBeLessThan(0.8);
  expect(c.stepBack.base).toBe('v6');
  expect(c.stepPastEnd.shown).toBe('v6');
  expect(c.stepPastEnd.toast).toContain('only covers');
  expect(c.stepIntoPreview.shown).toBe('v7');
  expect(c.stepBackFromPreview.base).toBe('v6');
  expect(c.flipPastEnd).toMatchObject({ shown: 'v6', flipped: false });
  expect(c.flipPastEnd.toast).toContain('only covers');
});

test('reload: the take\'s strokes come back as history, and no take is open', async ({}, info) => {
  await page.reload();
  await ui.waitFor(READY, 20_000);
  await page.evaluate(() => { document.querySelector('video')!.currentTime = 9.0; });
  await sleep(800);
  await shot(info, 'ui_history.png');
  const history = await page.evaluate(() => ({
    strokes: window.__review.hist.strokes.length, take: window.__review.take.id ?? null }));
  expect(history).toEqual({ strokes: 2, take: null });
  await page.setViewportSize({ width: 2560, height: 1440 });
  await sleep(500);
  await shot(info, 'ui_2560_history.png');
});

test('on disk: one take on v6, nothing for v5 or v7; the files are what the page sent', async () => {
  expect(existsSync(join(fam.dir, 'v5', 'clip_v5.review')), 'no take saved for v5').toBe(false);
  expect(existsSync(join(fam.dir, 'v7', 'clip_v7_0-010_preview.review')), 'no take saved for v7').toBe(false);
  const takes = takeDirs(v6Review());
  expect(takes).toHaveLength(1);
  take = readTake(takes[0]!);
  expect(take.session.session).toBe(marks['S1 on']!.take);
  expect(take.session.version).toBe('v6');
  expect(take.session.versions.map((v) => v.label)).toEqual(LEGACY ? ['v5', 'v6', 'v7'] : ['v5', 'v6']);
  expect(take.session.video).toMatchObject({ name: 'clip_v6.mp4', width: 1280, height: 720, fps: 30 });
  expect(take.session.video.duration).toBeCloseTo(40, 0);
  // Done saved the final state: the server wrote exactly what the page serialized.
  expect(take.segments).toEqual(c.saved!.segments);
  expect(take.strokes).toEqual(c.saved!.strokes);
  expect(take.events).toEqual(c.saved!.events);
});

test('on disk: one audio file per segment, as long as the segment; nothing captured outside them', async () => {
  const segs = take.segments;
  expect(segs.map((s) => s.file)).toEqual(segs.map((s, i) => expect.stringMatching(
    new RegExp(`^seg_0${i + 1}\\.(webm|ogg|m4a)$`))));
  expect(take.audio, 'no audio files besides the segments').toEqual(segs.map((s) => s.file).sort());
  for (const [i, s] of segs.entries()) {
    expect(s.recording, `${s.id} closed`).toBe(false);
    expect(s.t_end).toBeGreaterThan(s.t_start);
    if (i > 0) expect(s.t_start, `${s.id} starts after the previous one ends`).toBeGreaterThanOrEqual(segs[i - 1]!.t_end);
    expect(s.video_min).toBeLessThanOrEqual(Math.min(s.video_start, s.video_end) + 0.001);
    expect(s.video_max).toBeGreaterThanOrEqual(Math.max(s.video_start, s.video_end) - 0.001);
  }
  audio = await Promise.all(segs.map((s) => analyzeSegment(take.dir, s)));
  test.info().annotations.push({ type: 'segment audio', description: JSON.stringify(audio.map((a) => ({
    id: a.id, duration: +a.duration.toFixed(2), expected: +a.expected.toFixed(2),
    tones: a.tones.filter((t) => t.seconds > 0).map((t) => `${t.hz}Hz ${t.seconds.toFixed(1)}s @${t.first}`) }))) });
  for (const a of audio) {
    // Each segment's audio covers exactly its R-on/R-off span (MediaRecorder webm has no duration: decoded length).
    expect(Math.abs(a.duration - a.expected), `${a.id}: audio ${a.duration.toFixed(2)} s, page clock `
      + `${a.expected.toFixed(2)} s`).toBeLessThan(0.6);
    const seg = segs.find((s) => s.id === a.id)!;
    for (const t of a.tones) {
      if (t.seg === a.id) {
        // Said during this segment: captured, at its moment (audio starts at the segment's t_start).
        const burst = MIC_BURSTS.find((b) => b.hz === t.hz)!;
        expect(t.seconds, `${a.id} has the ${t.hz} Hz burst`).toBeGreaterThanOrEqual(0.5 * burst.len);
        expect(Math.abs(t.first! - Math.max(0, burst.at - seg.t_start)), `${a.id}: ${t.hz} Hz burst starts at its `
          + 'moment').toBeLessThan(0.5);
      } else {
        // Said outside this segment (another segment's speech, or the unrecorded loop's): not in this file.
        expect(t.seconds, `${a.id} must not contain the ${t.hz} Hz burst said at ${t.at} s`).toBe(0);
      }
    }
  }
  // The burst said during the unrecorded loop is in no file at all.
  const loopBurst = MIC_BURSTS.find((b) => b.seg === null)!;
  expect(audio.flatMap((a) => a.tones).filter((t) => t.hz === loopBurst.hz && t.seconds > 0)).toEqual([]);
});

test('on disk: strokes with colors, versions, flips and their moments; event kinds; voice marks', async () => {
  const segs = new Map(take.segments.map((s) => [s.id, s]));
  expect(take.strokes.map((x) => [x.id, x.seg, x.color, x.version, x.flip, x.paused])).toEqual([
    [1, 'S1', 'red', 'v6', false, true], [2, 'S4', 'green', 'v6', false, true]]);
  for (const st of take.strokes) {
    const seg = segs.get(st.seg)!;
    // The pen only works while recording: every stroke lies inside its segment, on both clocks.
    expect(st.session_t).toBeGreaterThanOrEqual(seg.t_start);
    expect(st.session_t_end).toBeLessThanOrEqual(seg.t_end);
    expect(st.session_t_end).toBeGreaterThanOrEqual(st.session_t);
    expect(st.video_t).toBeGreaterThanOrEqual(seg.video_min - 0.001);
    expect(st.video_t).toBeLessThanOrEqual(seg.video_max + 0.001);
    expect(st.points.length).toBeGreaterThan(5);
  }
  // Points are normalized to the video frame: the S1 loop was drawn around (0.35, 0.45), the S4 line at y = 0.2.
  const [red, green] = take.strokes;
  const mean = (p: [number, number][], k: 0 | 1) => p.reduce((a, q) => a + q[k], 0) / p.length;
  expect(mean(red!.points, 0)).toBeCloseTo(0.35, 1);
  expect(mean(red!.points, 1)).toBeCloseTo(0.45, 1);
  expect(Math.min(...green!.points.map((p) => p[0]))).toBeCloseTo(0.15, 1);
  expect(Math.max(...green!.points.map((p) => p[0]))).toBeCloseTo(0.35, 1);
  for (const p of green!.points) expect(p[1]).toBeCloseTo(0.2, 2);

  const ev = take.events;
  const count = (k: string) => ev.filter((e) => e.event === k).length;
  expect(ev[0]).toMatchObject({ event: 'start', session_t: 0 });
  expect(ev.map((e) => e.session_t)).toEqual([...ev.map((e) => e.session_t)].sort((a, b) => a - b));
  expect(new Set(ev.map((e) => e.event))).toEqual(new Set(['start', 'rec_start', 'rec_stop', 'play', 'pause', 'seek',
    'tick', 'draw_start', 'draw_end', 'version']));
  expect({ start: count('start'), rec_start: count('rec_start'), rec_stop: count('rec_stop'),
    draw_start: count('draw_start'), draw_end: count('draw_end'), version: count('version') })
    .toEqual({ start: 1, rec_start: 4, rec_stop: 4, draw_start: 2, draw_end: 2, version: 4 });
  expect(ev.filter((e) => e.event === 'rec_start').map((e) => e.seg)).toEqual(['S1', 'S2', 'S3', 'S4']);
  expect(ev.filter((e) => e.event === 'version').map((e) => [e.shown, e.base, e.flip])).toEqual([
    [PARTNER, 'v6', true], ['v6', 'v6', false], [PARTNER, 'v6', true], ['v6', 'v6', false]]);
  for (const s of take.segments) {
    const on = ev.find((e) => e.event === 'rec_start' && e.seg === s.id)!;
    const off = ev.find((e) => e.event === 'rec_stop' && e.seg === s.id)!;
    expect(on.session_t).toBeCloseTo(s.t_start, 1);
    expect(off.session_t).toBeCloseTo(s.t_end, 1);
  }

  // Voice marks: the bursts said while recording show on the timeline, inside the video.
  expect(take.voice.bin).toBe(0.25);
  expect(take.voice.bins.length).toBeGreaterThan(0);
  for (const [i] of take.voice.bins) expect(i * take.voice.bin).toBeLessThanOrEqual(40.25);
});

test('every JSON file of every take matches spec/take.schema.json', async () => {
  const dirs = allTakeDirs(fam.dir);
  expect(dirs).toHaveLength(1);
  for (const d of dirs) validateTakeDir(d);
});

test('session.json names the author (new in v1)', async () => {
  test.skip(LEGACY, 'the legacy player has no author');
  expect(take.session.author).toEqual({ name: AUTHOR });
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
