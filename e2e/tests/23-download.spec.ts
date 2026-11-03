// Download a version as a YouTube-ready MP4 (spec/api.md, "Download"): the version menu's last entry, for the version
// on screen. A family of two small versions made here:
// - v2 (opened, the newest): an MKV with a loud tone. Choosing the entry gets it ready (ffmpeg copies the video into
//   an MP4 and levels the audio), the entry shows how far, and the download starts by itself: `talk_v2.mp4`, the size
//   the server says, H.264 + AAC-LC 48 kHz stereo;
// - v1: an MP4 that is all of that already: it downloads as it is (the same bytes).
// The menu is screenshotted while v2 gets ready and once it has: .retake-work/download-shots/menu-*.png.
import { mkdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import { downloadFamily, lufs, streams } from '../lib/download.ts';
import { LEGACY, PORTS, REPO, WORK } from '../lib/env.ts';
import { READY } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.skip(LEGACY, 'new: the legacy player has no downloads');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium: the phone check is 23-download-webkit');

const DIR = join(WORK, 'download');
const SHOTS = join(REPO, '.retake-work', 'download-shots');
let server: Server;

test.beforeAll(async () => {
  server = await startServer('download', downloadFamily(DIR), PORTS.hls, [], { env: { RETAKE_ENCODER: 'x264' } });
});
test.afterAll(async () => { await server?.stop(); });

const entry = (page: Page) => page.locator('#verList [data-part="download"]');

/** Opens the version menu (a click on its button while it's open would close it). */
async function openMenu(page: Page): Promise<void> {
  if (!(await page.locator('#verList').isVisible())) await page.click('#verBtn');
  await expect(page.locator('#verList')).toBeVisible();
}

test('the version menu downloads the version on screen, made once or as it is', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(server.url);
  await page.waitForFunction(READY);
  await openMenu(page);
  await expect(entry(page)).toHaveText(/^Download v2 \(MP4\)/);
  await expect(entry(page)).toHaveAttribute('href', 'video/v2/download');

  // v2 gets ready: the entry shows how far (Checking…, a percentage or Preparing…), then the download starts.
  const download = page.waitForEvent('download', { timeout: 120_000 });
  await entry(page).click();
  await expect(entry(page).locator('.d')).toHaveText(/Checking…|Preparing…|\d+%|KB|MB/);
  mkdirSync(SHOTS, { recursive: true });
  await page.locator('#verList').screenshot({ path: join(SHOTS, 'menu-preparing.png') });
  const d = await download;
  expect(d.suggestedFilename()).toBe('talk_v2.mp4');
  const saved = join(DIR, 'got_v2.mp4');
  await d.saveAs(saved);
  const st = (await (await page.request.get(`${server.url}api/download?v=v2`)).json()) as { size: number; as_is: boolean };
  expect(st.as_is).toBe(false);
  expect(statSync(saved).size).toBe(st.size);
  const got = streams(saved);
  expect(got.map((s) => [s.codec_type, s.codec_name, s.profile])).toEqual([
    ['video', 'h264', 'High'],
    ['audio', 'aac', 'LC'],
  ]);
  expect(got[1]!.sample_rate).toBe('48000');
  expect(Math.abs(lufs(saved) + 14)).toBeLessThanOrEqual(1);
  // Ready now: the entry shows the size, and the menu stays where it was.
  await expect(entry(page).locator('.d')).toHaveText(/\d+ (KB|MB)/);
  await page.locator('#verList').screenshot({ path: join(SHOTS, 'menu-ready.png') });

  // v1 is an MP4 YouTube takes as it is: the same bytes.
  await page.locator('#verList [data-ver="v1"]').click();
  await expect.poll(() => page.evaluate(() => window.__review.view.base)).toBe('v1');
  await openMenu(page);
  await expect(entry(page)).toHaveText(/^Download v1 \(MP4\)/);
  const again = page.waitForEvent('download', { timeout: 60_000 });
  await entry(page).click();
  const d1 = await again;
  expect(d1.suggestedFilename()).toBe('talk_v1.mp4');
  const saved1 = join(DIR, 'got_v1.mp4');
  await d1.saveAs(saved1);
  expect(readFileSync(saved1).equals(readFileSync(join(DIR, 'v1', 'talk_v1.mp4')))).toBe(true);
  // Ready now: the menu's link downloads it straight away (the browser's own download, no waiting), and closes it.
  await openMenu(page);
  await expect(entry(page).locator('.d')).toHaveText(/\d+ (KB|MB)/);
  const direct = page.waitForEvent('download', { timeout: 30_000 });
  await entry(page).click();
  expect((await direct).suggestedFilename()).toBe('talk_v1.mp4');
  await expect(page.locator('#verList')).toBeHidden();
  await openMenu(page);
  await page.screenshot({ path: join(SHOTS, 'desktop.png') });
  expect(errors).toEqual([]);
});
