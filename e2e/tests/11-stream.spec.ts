// Streaming versions on demand (spec/bridge.md, "Streaming"): v1 rendered from the editor's demo, a patch on the pop,
// then `retake push --render --draft` registers v2 at once, pending, with a poster, and renders nothing. The page sees
// it ready (chip and toast); ] shows the poster at once and starts watching, so the server renders it, slowed down by
// the renderer's test hook (RETAKE_EDIT_TEST_UNIT_DELAY_MS: the first three units come at ~2 s, the next two at ~11 s
// and the last at ~15 s). v2 plays from 0 while its playlist has no ENDLIST, waits at the end of what's rendered,
// and C flips to v1 at the same moment. Closing the page stops the render, keeping what it published; a server
// started again (without the slow-down) resumes from the cache when the page shows v2 again, and it completes:
// ENDLIST, the right duration, no chip, and C maps through the time map. Needs a built retake-edit ($RETAKE_EDIT,
// editor/target/release, or $PATH); it skips without one.
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { spawn } from 'node:child_process';
import { copyFileSync, mkdirSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { CONFIG_HOME, LEGACY, PORTS, REPO, RETAKE_BIN, retakeEdit, WORK } from '../lib/env.ts';
import { run } from '../lib/proc.ts';
import { Review, sleep } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new with streaming versions: the legacy player has none');
const EDIT = retakeEdit();
test.skip(!EDIT, 'needs retake-edit (cd editor && cargo build --release, or set RETAKE_EDIT)');

const DEMO = join(REPO, 'editor', 'examples', 'demo');
const DIR = join(WORK, 'stream');
const V1 = join(DIR, 'v1', 'demo_v1.mp4');
const PLAYLIST = join(DIR, 'v2', 'demo_v2.m3u8');
// Units of the demo: 2.0, 1.9, 1.5, 0.3, 5.37 and 5.03 s. Three render at a time (retake-edit's default --jobs).
const DELAYS = '2000,2000,2000,9000,9000,13000';
const FULL = 16.1;

let server: Server;
let context: BrowserContext;
let page: Page;
let ui: Review;
let stoppedRenders = 0;
const pageErrors: string[] = [];

/** Starts the review server on v1; `slow` passes the renderer's slow-down to the renders it runs. */
async function serve(slow: boolean, folder = false): Promise<Server> {
  const old = process.env.RETAKE_EDIT_TEST_UNIT_DELAY_MS;
  process.env.RETAKE_EDIT = EDIT!;
  if (slow) process.env.RETAKE_EDIT_TEST_UNIT_DELAY_MS = DELAYS;
  else delete process.env.RETAKE_EDIT_TEST_UNIT_DELAY_MS;
  try {
    return await startServer('stream', folder ? DIR : V1, PORTS.stream);
  } finally {
    if (old === undefined) delete process.env.RETAKE_EDIT_TEST_UNIT_DELAY_MS;
    else process.env.RETAKE_EDIT_TEST_UNIT_DELAY_MS = old;
  }
}

async function openPage(browser: Browser, path = ''): Promise<void> {
  context = await browser.newContext({ viewport: { width: 1920, height: 1080 }, permissions: ['microphone'] });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  await page.route("**/api/session/*/transcribe?*", r => r.fulfill({contentType: "application/json", body: JSON.stringify({ok:true})}));
  ui = new Review(page);
  await ui.open(new URL(path, server.url).href);
}

/** Runs `retake push` and resolves with its output. */
function push(): Promise<{ code: number; out: string; secs: number }> {
  const t0 = Date.now();
  return new Promise((res) => {
    let out = '';
    const child = spawn(RETAKE_BIN, ['push', V1, '--render', '--draft'], {
      env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME, RETAKE_EDIT: EDIT! }, stdio: ['ignore', 'pipe', 'pipe'],
    });
    child.stdout.on('data', (d: Buffer) => { out += d.toString(); });
    child.stderr.on('data', (d: Buffer) => { out += d.toString(); });
    child.on('close', (code) => res({ code: code ?? -1, out, secs: (Date.now() - t0) / 1000 }));
  });
}

/** retake-edit processes rendering v2 right now. */
function renders(): number {
  let n = 0;
  for (const pid of readdirSync('/proc').filter((d) => /^\d+$/.test(d))) {
    try {
      const cmd = readFileSync(`/proc/${pid}/cmdline`, 'utf8').split('\0');
      if (cmd.some((a) => a.endsWith('retake-edit')) && cmd.includes('render') && cmd.includes(PLAYLIST)) n++;
    } catch { /* gone meanwhile */ }
  }
  return n;
}

const playlist = (): string => readFileSync(PLAYLIST, 'utf8');
const units = (): number => Math.max(0, ...[...playlist().matchAll(/^# unit (\d+)$/gm)].map((m) => Number(m[1])));
const v2 = () => window.__review.info.versions?.find((v) => v.label === 'v2');
const state = async () => (await page.evaluate(v2))?.stream?.state;

test.beforeAll(async ({ browser }) => {
  test.setTimeout(150_000);
  const edit = join(DIR, 'edit');
  mkdirSync(edit, { recursive: true });
  for (const f of ['timeline.json', 'vo.words.json']) copyFileSync(join(DEMO, f), join(edit, f));
  copyFileSync(join(DEMO, 'patches', '01-note-pop.json'), join(edit, 'pop.json'));
  await run('bash', [join(DEMO, 'make-media.sh'), join(edit, 'media'), EDIT!], { nice: true, env: { DEMO_SIZE: '320x180' } });
  const tl = join(edit, 'timeline.json');
  // v1 as a draft, so v2's draft reuses every unit but the pop's.
  await run(EDIT!, ['render', tl, '-o', V1, '--draft', '--json'], { nice: true });
  await run(EDIT!, ['patch', 'apply', tl, join(edit, 'pop.json')]);
  server = await serve(true);
  await openPage(browser);
});

test.afterAll(async () => {
  await context?.close();
  await server?.stop();
});

test('a pushed edit starts rendering automatically while feedback stays on its original frame', async () => {
  expect(await page.evaluate(() => window.__review.info.versions?.map((v) => v.label))).toEqual(['v1']);
  await ui.key('r');
  await ui.waitFor(() => !!window.__review.seg, 5000);
  const p = await push();
  expect(p.code, p.out).toBe(0);
  expect(p.out).toContain('pushed v2 from v1');
  expect(p.out).toContain('v2 streams when someone opens it: 6 units (5 cached)');
  console.log(`push returned in ${p.secs.toFixed(1)} s`);
  await ui.waitFor(() => !!window.__review.info.versions?.some((v) => v.label === 'v2'), 10_000);
  const v = await page.evaluate(v2);
  expect(v).toMatchObject({ name: 'demo_v2.m3u8', poster: 'video/v2/demo_v2.poster.jpg' });
  expect(v!.stream!.total).toBe(6);
  expect(v!.duration).toBeCloseTo(FULL, 1);
  const poster = await fetch(new URL(v!.poster!, server.url));
  expect(poster.status).toBe(200);
  expect(Buffer.from(await poster.arrayBuffer()).subarray(0, 2)).toEqual(Buffer.from([0xff, 0xd8]));
  await expect(page.locator('#verBtn')).toBeVisible();
  await expect.poll(renders).toBe(1);
  expect((await ui.state()).shown).toBe('v1');
  expect(await page.evaluate(() => !!window.__review.seg)).toBe(true);

});

test('stopping feedback follows the edited video and plays while its playlist grows', async () => {
  const t0 = Date.now();
  await ui.key('r');
  await ui.waitFor(() => window.__review.view.shown === 'v2' && !window.__review.view.switching, 20000);
  const switched = (Date.now() - t0) / 1000;
  expect(await ui.state()).toMatchObject({ base: 'v2', shown: 'v2', flipped: false });
  expect(await page.evaluate(() => document.querySelector('video')!.getAttribute('poster'))).toContain('demo_v2.poster.jpg');
  await expect(page.locator('#verBtn')).toBeVisible();
  await ui.key(' '); // play: remembered until the first segment is out
  await ui.waitFor(() => (document.querySelector('video')?.readyState ?? 0) >= 2
    && !document.querySelector('video')!.paused, 20_000);
  const firstFrame = (Date.now() - t0) / 1000;
  console.log(`edit followed in ${switched.toFixed(2)} s; playing ${firstFrame.toFixed(1)} s after feedback stopped`);
  expect(renders()).toBe(1);
  const start = (await ui.video()).t;
  expect(start).toBeLessThan(0.5); // from the start, not the live edge
  await ui.waitUntil((t) => document.querySelector('video')!.currentTime > t + 1.5, start, 10_000);
  expect(playlist()).toContain('#EXT-X-PLAYLIST-TYPE:EVENT');
  expect(playlist(), 'played while the playlist is still growing').not.toContain('#EXT-X-ENDLIST');
  expect(await state()).toBe('rendering');
  const chip = await page.evaluate(() => window.__review.info.versions?.find((v) => v.label === 'v2')?.stream);
  expect(chip!.done).toBeGreaterThanOrEqual(3);
  // The first three units are 5.4 s; the playhead gets there and waits (buffering) instead of failing or ending.
  await ui.waitFor(() => document.querySelector('video')!.currentTime > 5.0, 15_000);
  await sleep(1500);
  if (units() === 3) {
    const w = await page.evaluate(() => {
      const el = document.querySelector('video')!;
      return { t: el.currentTime, ended: el.ended, paused: el.paused, error: el.error };
    });
    expect(w).toMatchObject({ ended: false, paused: false, error: null });
    expect(w.t).toBeLessThan(5.5);
    console.log(`waiting at ${w.t.toFixed(2)} s for the next units`);
  }
});

test('removed comparison shortcuts leave the evolving video and playhead alone', async () => {
  await ui.key(' ');
  await page.evaluate(() => { document.querySelector('video')!.currentTime = 3; });
  await ui.waitFor(() => !document.querySelector('video')!.seeking, 5000);
  await ui.key('c'); await ui.key('[');
  expect(await ui.state()).toMatchObject({ shown: 'v2', flipped: false, paused: true });
  expect((await ui.state()).t).toBeCloseTo(3, 1);
});

test('closing the page stops the render; what it published stays', async () => {
  const before = units();
  // C above leaves the streamed version and comes back, which may already have stopped and restarted it.
  stoppedRenders = (JSON.parse(readFileSync(join(DIR, 'v2', 'demo_v2.push.json'), 'utf8')) as
    { stream: { renders: number } }).stream.renders;
  expect(before).toBeLessThan(6);
  await context.close(); // the page's unwatch beacon goes out as it closes
  const t0 = Date.now();
  while (renders() > 0 && Date.now() - t0 < 15_000) await sleep(100);
  expect(renders(), 'the render stopped').toBe(0);
  console.log(`render stopped ${((Date.now() - t0) / 1000).toFixed(1)} s after the page closed, at ${units()}/6 units`);
  const info = await (await fetch(new URL('api/info', server.url))).json() as
    { versions: { label: string; stream?: { state: string; done: number } }[] };
  const s = info.versions.find((v) => v.label === 'v2')!.stream!;
  expect(s.state).toBe('pending');
  expect(s.done).toBeGreaterThanOrEqual(before);
  expect(playlist()).not.toContain('#EXT-X-ENDLIST');
  const rec = JSON.parse(readFileSync(join(DIR, 'v2', 'demo_v2.push.json'), 'utf8')) as { stream: Record<string, unknown> };
  expect(rec.stream).toMatchObject({ state: 'pending', renders: stoppedRenders });
});

test('opened again (another server), it resumes from the cache and completes', async ({ browser }) => {
  await server.stop();
  server = await serve(false);
  await openPage(browser);
  const published = units();
  const t0 = Date.now();
  await ui.waitFor(() => window.__review.view.shown === 'v2', 20000);
  await ui.key(' ');
  await ui.waitFor(() => (document.querySelector('video')?.readyState ?? 0) >= 2
    && !document.querySelector('video')!.paused, 20_000);
  console.log(`resumed after ${published}/6 units: playing ${((Date.now() - t0) / 1000).toFixed(1)} s after ]`);
  await ui.waitFor(() => window.__review.info.versions?.find((v) => v.label === 'v2')?.stream?.state === 'complete', 30_000);
  expect(playlist().trimEnd().endsWith('#EXT-X-ENDLIST')).toBe(true);
  const total = [...playlist().matchAll(/#EXTINF:([\d.]+),/g)].reduce((s, m) => s + Number(m[1]), 0);
  expect(total).toBeCloseTo(FULL, 2);
  const rec = JSON.parse(readFileSync(join(DIR, 'v2', 'demo_v2.push.json'), 'utf8')) as { stream: Record<string, unknown> };
  expect(rec.stream).toMatchObject({ state: 'complete', renders: stoppedRenders + 1, done: 6 });
  const v = await page.evaluate(v2);
  expect(v!.stream!.state).toBe('complete');
  expect(v!.duration).toBeCloseTo(FULL, 1);
  // The video kept playing through the end of the render (nothing reloaded it).
  await ui.waitUntil((t) => document.querySelector('video')!.currentTime > t + 1, (await ui.video()).t, 15_000);
  const after = await ui.video();
  expect(after.error).toBeNull();
  expect(after.paused).toBe(false);
  // The completion event can arrive before the player's next playlist reload (one target duration).
  await expect.poll(async () => (await ui.video()).duration, { timeout: 10_000 }).toBeCloseTo(FULL, 0);
  await expect(page.locator('#verBtn')).toBeVisible();
  await ui.key(' ');
  expect((await ui.state()).shown).toBe('v2');
  await sleep(500);
  expect(renders(), 'a complete version never renders again').toBe(0);
});

test('a home project streams on open and returning to All videos stops it', async ({ browser }) => {
  await context.close();
  await server.stop();
  const p = await push();
  expect(p.code, p.out).toBe(0);
  expect(p.out).toContain('pushed v3 from v2');
  server = await serve(true, true);
  await openPage(browser, 'p/stream/');
  await ui.waitFor(() => window.__review.view.shown === 'v3' && !window.__review.view.switching, 20000);
  await ui.key(' ');
  await ui.waitFor(() => (document.querySelector('video')?.readyState ?? 0) >= 2
    && !document.querySelector('video')!.paused, 20_000);
  const record = () => JSON.parse(readFileSync(join(DIR, 'v3', 'demo_v3.push.json'), 'utf8')) as
    { stream: { state: string; done: number; total: number } };
  expect(record().stream.state).toBe('rendering');
  await page.locator('#allVideos').click();
  await expect(page).toHaveURL(server.url);
  await expect.poll(() => record().stream.state, { timeout: 10_000 }).toBe('pending');
  expect(record().stream.done).toBeGreaterThan(0);
  expect(record().stream.done).toBeLessThan(record().stream.total);
  await sleep(1500);
  expect(record().stream.state, 'the home card must not restart rendering').toBe('pending');
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
