// New in milestone 1: a version can be a chunked playlist (.m3u8 with fMP4 segments). The family here is
// v6/clip_v6.mp4 plus clip_v8/clip_v8.m3u8 (+ init.mp4 and seg_NNN.m4s beside it): a sibling folder of v6, so family
// detection finds it as v8. The page opens the newest, v8, and C flips between the playlist and the mp4 like between
// any two versions; [ and ] are not keys.
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { FULL } from '../lib/fixtures.ts';
import { Review, sleep } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';
import { family, type Family } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new in milestone 1: the legacy player plays single files only');

let fam: Family;
let server: Server;
let context: BrowserContext;
let page: Page;
let ui: Review;
const pageErrors: string[] = [];

/** The <video> is playing the playlist: its own URL with native HLS, or a blob: URL when the page uses MSE. */
const playsV8 = (src: string) => src.startsWith('blob:') || src.includes('/video/v8/');

test.beforeAll(async ({ browser }) => {
  fam = family('hls', ['v6', 'v8']);
  server = await startServer('hls', fam.files.v6!, PORTS.hls);
  context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  ui = new Review(page);
});

test.afterAll(async () => {
  await context?.close();
  await server?.stop();
});

test('the server serves the playlist and the files beside it under /video/v8/, and nothing else', async () => {
  const get = (path: string, headers: Record<string, string> = {}) => fetch(new URL(path, server.url), { headers });
  const playlist = await get('video/v8/clip_v8.m3u8');
  expect(playlist.status).toBe(200);
  expect(playlist.headers.get('content-type')).toContain('application/vnd.apple.mpegurl');
  expect(await playlist.text()).toContain('seg_000.m4s');
  const init = await get('video/v8/init.mp4');
  expect(init.status).toBe(200);
  expect(init.headers.get('content-type')).toContain('video/mp4');
  await init.arrayBuffer();
  const seg = await get('video/v8/seg_003.m4s', { Range: 'bytes=0-99' });
  expect(seg.status).toBe(206);
  expect(seg.headers.get('content-type')).toMatch(/^video\/(mp4|iso\.segment)/);
  expect((await seg.arrayBuffer()).byteLength).toBe(100);
  for (const bad of ['video/v8/missing.m4s', 'video/v8/sub/seg_000.m4s', 'video/v9/clip_v8.m3u8']) {
    const r = await get(bad);
    await r.arrayBuffer();
    expect(r.status, bad).toBe(404);
  }
  // An encoded ../ never serves a file from outside the playlist's folder (a 4xx, or a router's clean-path redirect).
  for (const bad of ['video/v8/..%2F..%2Fmedia%2Fmic.wav', 'video/v8/%2E%2E%2Fclip_v8.m3u8']) {
    const r = await fetch(new URL(bad, server.url), { redirect: 'manual' });
    await r.arrayBuffer();
    expect(r.status, bad).toBeGreaterThanOrEqual(300);
  }
});

test('family: the mp4 v6 and the m3u8 v8 in its own folder', async () => {
  await ui.open(server.url);
  const versions = await page.evaluate(() => (window.__review.info.versions ?? []).map((v) => [v.label, v.name, v.duration]));
  expect(versions.map(([l, n]) => [l, n])).toEqual([['v6', 'clip_v6.mp4'], ['v8', 'clip_v8.m3u8']]);
  expect(versions[1]![2] as number).toBeCloseTo(FULL, 0);
});

test('C flips from the m3u8 to the mp4 at the same time, C flips back, and the playlist plays', async () => {
  await page.evaluate(() => { document.querySelector('video')!.currentTime = 12; });
  await ui.waitFor(() => !document.querySelector('video')!.seeking, 5000);
  const before = await ui.state();
  expect(before).toMatchObject({ shown: 'v8', base: 'v8', flipped: false });
  await ui.key('c'); await ui.settle(); await sleep(300);
  const flipped = await ui.state();
  expect(flipped).toMatchObject({ shown: 'v6', base: 'v8', flipped: true, paused: true });
  expect(Math.abs(flipped.t - before.t)).toBeLessThan(0.1);

  await ui.key('c'); await ui.settle(); await sleep(300);
  const after = await ui.state();
  expect(after).toMatchObject({ shown: 'v8', base: 'v8', flipped: false, paused: true });
  expect(Math.abs(after.t - before.t)).toBeLessThan(0.1);
  expect(playsV8((await ui.video()).src)).toBe(true);

  await ui.key(' ');
  await ui.waitUntil((t) => document.querySelector('video')!.currentTime > t + 1.5, after.t, 10_000);
  const v = await ui.video();
  expect(v.readyState).toBeGreaterThanOrEqual(2);
  expect(v.paused).toBe(false);
  expect(v.error).toBeNull();
  expect(v.duration).toBeCloseTo(FULL, 0);
  await ui.key(' ');
  await sleep(200);

  // C again, after playing: the mp4 at the same time.
  const b2 = await ui.state();
  await ui.key('c'); await ui.settle(); await sleep(200);
  const a2 = await ui.state();
  expect(a2).toMatchObject({ shown: 'v6', base: 'v8', flipped: true, paused: true });
  expect(Math.abs(a2.t - b2.t)).toBeLessThan(0.1);
  await ui.key('c'); await ui.settle(); await sleep(200);
  expect(await ui.state()).toMatchObject({ shown: 'v8', base: 'v8', flipped: false });
});

test('[ and ] are not keys: HLS playback and its playhead go on', async () => {
  await ui.key(' ');
  await sleep(1000);
  const before = await ui.state();
  await ui.key(']'); await ui.settle(); await sleep(150);
  const after = await ui.state();
  expect(after).toMatchObject({ base: 'v8', shown: 'v8', flipped: false, paused: false });
  expect(Math.abs(after.t - before.t)).toBeLessThan(0.8);
  expect(playsV8((await ui.video()).src)).toBe(true);
  await ui.waitUntil((t) => document.querySelector('video')!.currentTime > t + 1.5, after.t, 10_000);
  expect((await ui.video()).readyState).toBeGreaterThanOrEqual(2);

  const b2 = await ui.state();
  await ui.key('['); await ui.settle(); await sleep(150);
  const a2 = await ui.state();
  expect(a2).toMatchObject({ base: 'v8', shown: 'v8', paused: false });
  expect(Math.abs(a2.t - b2.t)).toBeLessThan(0.8);
  await ui.key(' ');
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
