// Plain projects on a server (spec/automation.md, "Version files" and "The media relay"): `retake serve`, and
// `retake connect` on a folder of finished MP4s with no timeline. The videos never upload: the page plays them from
// the computer through the server's relay, in byte ranges of at most one 8 MiB chunk, and seeks into a part it
// hasn't loaded. With the computer gone, the page shows the version's poster with "The editing computer is offline",
// and plays it once the computer is back. A screenshot of that goes to RETAKE_E2E_SHOTS when it is set.
import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { CONFIG_HOME, LEGACY, PORTS, RETAKE_BIN, WORK } from '../lib/env.ts';
import { run } from '../lib/proc.ts';
import { Review, sleep } from '../lib/review.ts';
import { startDaemon, startTeamServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no team server');

const DATA = join(WORK, 'relay-server');
const FOLDER = join(WORK, 'relay-videos');
const BASE = `http://127.0.0.1:${PORTS.relay}`;
const CHUNK = 8 << 20;
const SHOTS = process.env.RETAKE_E2E_SHOTS; // the offline page's screenshot goes there when set
let server: Server;
let connect: Server | null = null;
let link = '';
let token = '';
const pageErrors: string[] = [];

function admin(...args: string[]): Record<string, unknown> {
  const out = execFileSync(RETAKE_BIN, [...args, '--data', DATA, '--public-url', BASE, '--json'], {
    env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME },
  });
  return JSON.parse(out.toString()) as Record<string, unknown>;
}

/** H.264 + AAC at 1280x720 with a keyframe every second; `rate` set makes it constant bitrate (so big). */
async function encode(file: string, seconds: number, rate?: string): Promise<void> {
  const cbr = rate ? ['-b:v', rate, '-minrate', rate, '-maxrate', rate, '-bufsize', '2M', '-x264-params', 'nal-hrd=cbr'] : [];
  await run('ffmpeg', ['-v', 'error', '-nostdin', '-y', '-threads', '6',
    '-f', 'lavfi', '-i', `testsrc2=s=1280x720:r=25:d=${seconds}`,
    '-f', 'lavfi', '-i', `sine=f=440:sample_rate=48000:d=${seconds}`,
    '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-g', '25', ...cbr,
    '-c:a', 'aac', '-shortest', '-movflags', '+faststart', file], { nice: true });
}

function startConnect(): Server {
  const profiles = join(WORK, 'relay-profiles.json');
  writeFileSync(profiles, JSON.stringify([{ id: 'fake', name: 'Fake agent', command: ['sh', '-c', 'exit 0'] }]));
  return startDaemon('connect', RETAKE_BIN, ['connect', BASE, '--token', token, '--folder', FOLDER, '--name', 'e2e-pc',
    '--profiles', profiles]);
}

test.beforeAll(async () => {
  const promo = join(FOLDER, 'Promo');
  mkdirSync(promo, { recursive: true });
  await encode(join(promo, 'promo_v1.mp4'), 4);
  await encode(join(promo, 'promo_v2.mp4'), 16, '14M'); // about 28 MB: four relay chunks
  expect(statSync(join(promo, 'promo_v2.mp4')).size).toBeGreaterThan(3 * CHUNK);
  link = (admin('user', 'add', 'owner@example.com', '--name', 'Owner', '--admin') as { link: string }).link;
  server = await startTeamServer('relay', PORTS.relay, ['--data', DATA, '--public-url', BASE]);
});

test.afterAll(async () => {
  await connect?.stop();
  await server?.stop();
  expect(pageErrors).toEqual([]);
});

/** Signs the owner in. A sign-in link works once: after the first, a new one is made. */
async function signIn(page: Page): Promise<void> {
  if (!link) link = (admin('user', 'link', 'owner@example.com') as { link: string }).link;
  await page.goto(link);
  link = '';
  await page.getByRole('button', { name: /Sign in as/ }).click();
  await page.waitForURL(`${BASE}/`);
}

const video = (page: Page) =>
  page.evaluate(() => {
    const v = document.querySelector('video')!;
    return { t: v.currentTime, ready: v.readyState, seeking: v.seeking, poster: v.getAttribute('poster') };
  });

test('a folder of finished MP4s plays and seeks on the server, from the computer', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  const ui = new Review(page);
  try {
    await signIn(page);
    const made = (await (await page.request.post(`${BASE}/api/connect`, { headers: { Origin: BASE } })).json()) as {
      token: string;
    };
    token = made.token;
    connect = startConnect();
    await expect
      .poll(async () => {
        const r = await page.request.get(`${BASE}/p/promo/api/info`);
        if (!r.ok()) return '';
        const info = (await r.json()) as { versions?: { label: string; relay?: boolean }[] };
        return (info.versions ?? []).map((v) => `${v.label}${v.relay ? ' relayed' : ''}`).join(', ');
      }, { timeout: 30_000 })
      .toBe('v1 relayed, v2 relayed');

    const answers: { status: number; range: string; length: number }[] = [];
    page.on('response', (r) => {
      if (!r.url().endsWith('/p/promo/video/v2')) return;
      const h = r.headers();
      answers.push({ status: r.status(), range: h['content-range'] ?? '', length: Number(h['content-length'] ?? -1) });
    });
    await ui.open(`${BASE}/p/promo/`);
    expect(await page.evaluate(() => window.__review.view.shown)).toBe('v2');

    // It plays.
    await page.evaluate(() => document.querySelector('video')!.play());
    await ui.waitFor(() => document.querySelector('video')!.currentTime > 1, 15_000);
    // A seek near the end: a part of the file the page hasn't loaded, in a later chunk.
    await page.evaluate(() => { document.querySelector('video')!.currentTime = 13; });
    await ui.waitFor(() => {
      const v = document.querySelector('video')!;
      return !v.seeking && v.readyState >= 2 && v.currentTime >= 13;
    }, 20_000);
    const at = (await video(page)).t;
    await sleep(1200);
    expect((await video(page)).t).toBeGreaterThan(at + 0.4);
    await page.evaluate(() => document.querySelector('video')!.pause());

    // Every answer was a byte range of at most one chunk, and one of them started past the first chunk.
    expect(answers.length).toBeGreaterThan(1);
    for (const a of answers) {
      expect(a.status).toBe(206);
      expect(a.range).toMatch(/^bytes \d+-\d+\/\d+$/);
      expect(a.length).toBeGreaterThan(0);
      expect(a.length).toBeLessThanOrEqual(CHUNK);
    }
    expect(answers.some((a) => Number(/^bytes (\d+)/.exec(a.range)![1]) >= CHUNK)).toBe(true);
    // The server kept what it relayed, and holds no copy of the files themselves.
    expect(readdirSync(join(DATA, 'relay-cache', 'promo')).length).toBeGreaterThan(1);
    expect(existsSync(join(DATA, 'projects', 'promo', 'promo_v2.mp4'))).toBe(false);
  } finally {
    await context.close();
  }
});

test('while the computer is offline the poster says so, then the video plays again', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  const ui = new Review(page);
  try {
    await signIn(page);
    await connect?.stop();
    connect = null;
    // The server calls a computer offline 10 s after its last poll (a request nobody takes gives up after 10 s too).
    await expect
      .poll(async () => (await page.request.head(`${BASE}/p/promo/video/v1`)).status(), { timeout: 40_000 })
      .toBe(503);
    // v2's start is in the server's cache, so it still opens.
    await ui.open(`${BASE}/p/promo/`);
    expect(await page.evaluate(() => window.__review.view.shown)).toBe('v2');
    // v1 was never played, so nothing of it is on the server: choosing it finds the computer offline.
    await ui.click('#verBtn');
    await ui.click('#verList [data-ver="v1"]');
    await expect(page.locator('#offline')).toHaveText('The editing computer is offline', { timeout: 15_000 });
    expect(await page.evaluate(() => window.__review.view.shown)).toBe('v1');
    expect((await video(page)).poster).toContain('api/poster');
    await expect(page.locator('#fatal')).toBeHidden();
    if (SHOTS) {
      mkdirSync(SHOTS, { recursive: true });
      await page.screenshot({ path: join(SHOTS, 'relay-offline.png') });
    }

    // The computer comes back: within a few seconds the page notices, and the video plays.
    connect = startConnect();
    await expect(page.locator('#offline')).toHaveCount(0, { timeout: 30_000 });
    await ui.waitFor(() => document.querySelector('video')!.readyState >= 2, 20_000);
    await page.evaluate(() => document.querySelector('video')!.play());
    await ui.waitFor(() => document.querySelector('video')!.currentTime > 0.5, 15_000);
  } finally {
    await context.close();
  }
});
