// New in milestone 1: audio chunks stay in IndexedDB until the server confirms them, so a take survives failed
// uploads and a reload. The legacy page kept audio only in memory, so this runs against the new server only.
//
// While recording, every audio upload fails. Keep talking, then reload the page with uploads still failing. Stop
// failing: the reloaded page must resend everything from IndexedDB, and the segment file on disk must hold the full
// audio up to the reload, with each thing said at its moment.
//
// The uploads are failed by a proxy in front of the server (lib/proxy.ts), not by page.route: a keepalive upload sent
// while the page unloads bypasses browser-side routing, and would reach the server.
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { LEGACY, PORTS } from '../lib/env.ts';
import { MIC_BURSTS } from '../lib/fixtures.ts';
import { audioDuration } from '../lib/media.ts';
import { FailingProxy } from '../lib/proxy.ts';
import { READY, Review, Schedule, sleep } from '../lib/review.ts';
import { validateTakeDir } from '../lib/schema.ts';
import { startServer, type Server } from '../lib/server.ts';
import { analyzeSegment, readTake } from '../lib/take.ts';
import { family, takeDirs, type Family } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new in milestone 1: the legacy page keeps audio in memory only');

let fam: Family;
let server: Server;
let proxy: FailingProxy;
let context: BrowserContext;
let page: Page;
let ui: Review;
const pageErrors: string[] = [];

const recording = () => !!window.__review.seg;

/** The take's audio files on disk with their sizes. */
function audioOnDisk(): { dir: string; file: string; size: number }[] {
  return takeDirs(join(fam.dir, 'v6', 'clip_v6.review')).flatMap((dir) => readdirSync(dir)
    .filter((f) => /^seg_\d{2,3}\.(webm|ogg|m4a)$/.test(f))
    .map((file) => ({ dir, file, size: statSync(join(dir, file)).size })));
}

test.beforeAll(async ({ browser }) => {
  fam = family('crash', ['v5', 'v6']);
  server = await startServer('crash', fam.files.v6!, PORTS.crash);
  proxy = new FailingProxy(PORTS.crashProxy, PORTS.crash);
  await proxy.start();
  context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  await context.grantPermissions(['microphone'], { origin: proxy.url.replace(/\/$/, '') });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  ui = new Review(page);
});

test.afterAll(async () => {
  await context?.close();
  await proxy?.stop();
  await server?.stop();
});

test('audio recorded while uploads fail is resent from IndexedDB after a reload', async () => {
  await ui.open(proxy.url);
  proxy.failing = true;

  // Record while playing, and keep talking: the fake mic says something at 1.0 s and from 3.3 s to 7.5 s.
  await ui.key(' ');
  await ui.key('r');
  await ui.waitFor(recording, 8000);
  const s = new Schedule();
  s.start();
  const takeId = (await ui.state()).take;
  expect(takeId).toMatch(/^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$/);
  await s.at(11.0); // past two autosaves (every 5 s), so uploads were tried and failed
  const recorded = s.now();
  expect(proxy.failed, 'audio uploads were attempted, and failed').toBeGreaterThan(0);

  // Reload mid-recording, uploads still failing (the unload flush fails too).
  await page.reload();
  await ui.waitFor(READY, 20_000);
  await sleep(1000);
  expect(audioOnDisk().filter((a) => a.size > 0), 'no audio reached the server while uploads failed').toEqual([]);

  // Uploads work again: the page resends the buffered chunks on its own.
  proxy.failing = false;
  let dur = 0;
  await expect.poll(async () => {
    const files = audioOnDisk().filter((a) => a.size > 0);
    if (files.length !== 1) return files.length === 0 ? 'no audio file yet' : `${files.length} audio files`;
    try { dur = await audioDuration(join(files[0]!.dir, files[0]!.file)); } catch { return 'not decodable yet'; }
    return dur >= recorded - 1.5 ? 'complete' : `${dur.toFixed(2)} s of ${recorded.toFixed(2)} s`;
  }, { timeout: 45_000, intervals: [500] }).toBe('complete');

  // It is the same take and segment, the audio is as long as the recording up to the reload (the last second may
  // still have been in the recorder), and each thing said is at its moment.
  const [file] = audioOnDisk();
  expect(file!.dir.endsWith(takeId!)).toBe(true);
  expect(file!.file).toMatch(/^seg_01\.(webm|ogg|m4a)$/);
  expect(dur).toBeLessThan(recorded + 0.6);
  const take = readTake(file!.dir);
  const seg = take.segments.find((x) => x.file === file!.file);
  expect(seg, 'segments.json lists the segment').toBeTruthy();
  const a = await analyzeSegment(file!.dir, seg!);
  for (const b of MIC_BURSTS.filter((x) => x.at + x.len < recorded - 1.5)) {
    const t = a.tones.find((x) => x.hz === b.hz)!;
    expect(t.seconds, `the ${b.hz} Hz burst said at ${b.at} s is in the saved audio`).toBeGreaterThanOrEqual(0.6 * b.len);
    expect(Math.abs(t.first! - (b.at - seg!.t_start)), `${b.hz} Hz at its moment`).toBeLessThan(0.5);
  }
  validateTakeDir(file!.dir);
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
