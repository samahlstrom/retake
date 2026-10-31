// New (spec/home.md): many videos in one sitting. `retake open <folder>` serves a folder of three generated projects:
//
//   alpha/v1/alpha_v1.mp4, alpha/v2/alpha_v2.mp4   to review (pushed last)
//   bravo/bravo_v1.mp4                             to review
//   charlie/v3/charlie_v3.mp4                      reviewed (a closed take is there already)
//   broll/sunset.mp4                               not a project (no version number)
//
// The home lists them with first-frame thumbnails and statuses, to review first. A card opens its review page, whose
// <video poster> is the same frame. A take and Done make it Reviewed, and "Next video" goes to the next one to
// review; leaving sends the unwatch call. > does the same from anywhere and says when none are left. On a phone the
// home is one column and the controls are thumb-sized.
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { LEGACY, PORTS, WORK } from '../lib/env.ts';
import { line, loop, READY, Review, sleep } from '../lib/review.ts';
import { validateTakeDir } from '../lib/schema.ts';
import { startServer, type Server } from '../lib/server.ts';
import { takeDirs } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no home screen');

const DIR = join(WORK, 'home-folder');
let server: Server;
const pageErrors: string[] = [];

interface HomeCard { name: string; version: string; status: string; thumb: string }
declare global {
  interface Window { __home: { loaded: boolean; projects: HomeCard[]; error: string } }
}

/** Copies a generated clip into the folder (a copy, so its own modification time is the push time). */
function put(src: string, rel: string, minutes: number): string {
  const dst = join(DIR, rel);
  mkdirSync(dirname(dst), { recursive: true });
  copyFileSync(src, dst);
  const t = new Date(Date.UTC(2026, 9, 1, 12, minutes));
  utimesSync(dst, t, t);
  return dst;
}

test.beforeAll(async () => {
  const m = mediaLayout(WORK);
  put(m.v5, 'alpha/v1/alpha_v1.mp4', 0);
  put(m.v6, 'alpha/v2/alpha_v2.mp4', 180);
  put(m.v7preview, 'bravo/bravo_v1.mp4', 120);
  const charlie = put(m.v5, 'charlie/v3/charlie_v3.mp4', 60);
  put(m.v7preview, 'broll/sunset.mp4', 0);
  const done = join(dirname(charlie), 'charlie_v3.review', '2026-10-01_13-00-00');
  mkdirSync(done, { recursive: true });
  writeFileSync(join(done, 'session.json'), JSON.stringify({ format: 3, closed: true }));
  server = await startServer('home', DIR, PORTS.home, ['--verbose']);
});

test.afterAll(async () => {
  await server?.stop();
});

async function newPage(browser: Browser, phone = false): Promise<{ context: BrowserContext; page: Page; ui: Review }> {
  const context = phone
    ? await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true,
      deviceScaleFactor: 2 })
    : await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  await context.grantPermissions(['microphone'], { origin: server.url.replace(/\/$/, '') });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  return { context, page, ui: new Review(page) };
}

/** Opens the home and waits for its cards. */
async function openHome(page: Page): Promise<HomeCard[]> {
  if (page.url() !== server.url) await page.goto(server.url);
  await page.waitForFunction(() => window.__home?.loaded && window.__home.projects.length > 0, null,
    { timeout: 20_000, polling: 80 });
  return page.evaluate(() => window.__home.projects);
}

/** What the cards on screen say, in order. */
async function cards(page: Page): Promise<string[]> {
  return page.locator('a.card').evaluateAll((els) => els.map((a) => {
    const el = a as HTMLElement;
    return [el.dataset.name, el.querySelector('.ver')?.textContent, el.querySelector('.chip')?.textContent].join(' ');
  }));
}

/** Every card's thumbnail has loaded with a frame. */
async function thumbsLoaded(page: Page): Promise<void> {
  await page.waitForFunction(() => {
    const imgs = [...document.querySelectorAll<HTMLImageElement>('a.card .thumb img')];
    return imgs.length === document.querySelectorAll('a.card').length && imgs.every((i) => i.complete
      && i.naturalWidth > 0);
  }, null, { timeout: 20_000, polling: 100 });
}

/** Elements that stick out of the viewport sideways. */
function overflowing(): string[] {
  const W = document.documentElement.clientWidth;
  const out: string[] = [];
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if ((r.width || r.height) && (r.left < -0.5 || r.right > W + 0.5)) out.push(`${el.tagName} ${r.left}..${r.right}`);
  }
  return out;
}

async function homeStatus(page: Page): Promise<Record<string, string>> {
  return page.evaluate(async () => {
    const h = (await (await fetch('/api/home')).json()) as { projects: { name: string; status: string }[] };
    return Object.fromEntries(h.projects.map((p) => [p.name, p.status]));
  });
}

/** A take: R, a stroke, R. */
async function record(ui: Review, page: Page, touch = false): Promise<void> {
  await page.evaluate(async () => {
    const v = document.querySelector('video')!;
    const done = new Promise((r) => v.addEventListener('seeked', r, { once: true }));
    v.currentTime = 3;
    await done;
  });
  if (touch) await ui.tap('#rec');
  else await ui.key('r');
  await ui.waitFor(() => !!window.__review.seg, 8000);
  await sleep(1000);
  if (touch) await ui.touchDrag(line([0.3, 0.4], [0.6, 0.5]));
  else await ui.drag(loop(0.4, 0.45));
  await sleep(600);
  if (touch) await ui.tap('#rec');
  else await ui.key('r');
  await ui.waitFor(() => !window.__review.seg, 8000);
}

test('the home lists the projects with their first frames and statuses, to review first', async ({ browser }) => {
  const { context, page } = await newPage(browser);
  try {
    const projects = await openHome(page);
    expect(projects.map((p) => [p.name, p.version, p.status, p.thumb])).toEqual([
      ['alpha', 'v2', 'to_review', 'p/alpha/api/poster'],
      ['bravo', 'v1', 'to_review', 'p/bravo/api/poster'],
      ['charlie', 'v3', 'reviewed', 'p/charlie/api/poster'],
    ]);
    expect(await cards(page)).toEqual(['alpha v2 To review', 'bravo v1 To review', 'charlie v3 Reviewed']);
    await expect(page.locator('#todo')).toHaveText('2 to review');
    await expect(page.locator('h1')).toHaveText('home-folder');
    await expect(page.locator('a.card[data-name="alpha"] .len')).toHaveText('0:40');
    await thumbsLoaded(page);
    // Each thumbnail is the version's first frame, at most 640 px wide, from the project's api/poster.
    const sizes = await page.locator('a.card .thumb img').evaluateAll((els) =>
      els.map((i) => [(i as HTMLImageElement).naturalWidth, (i as HTMLImageElement).naturalHeight]));
    expect(sizes).toEqual([[640, 360], [640, 360], [640, 360]]);
    const poster = await page.request.get(`${server.url}p/bravo/api/poster`);
    expect(poster.status()).toBe(200);
    expect(poster.headers()['content-type']).toBe('image/jpeg');
    // The cards sit in a row at this size, all the same size, and nothing sticks out.
    const boxes = await page.locator('a.card').evaluateAll((els) => els.map((e) => e.getBoundingClientRect().top));
    expect(new Set(boxes).size).toBe(1);
    expect(await page.evaluate(overflowing)).toEqual([]);
  } finally {
    await context.close();
  }
});

test('a card opens its review page with the poster; Done makes it Reviewed; Next video goes on', async ({ browser }) => {
  const { context, page, ui } = await newPage(browser);
  try {
    await openHome(page);
    await page.locator('a.card[data-name="alpha"]').click();
    await page.waitForURL(`${server.url}p/alpha/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    // The same frame as the card, while the video loads.
    expect(await page.evaluate(() => document.querySelector('video')!.poster)).toBe(`${server.url}p/alpha/api/poster?v=v2`);
    expect(await page.evaluate(() => window.__review.info.label)).toBe('v2');
    await expect(page.locator('#allVideos')).toBeVisible();
    await expect(page.locator('#allVideos')).toHaveAttribute('href', '/');
    await expect(page.locator('#nextVideo')).toHaveCount(0); // not before Done
    expect(await page.evaluate(() => (window.__review as unknown as { home: unknown }).home))
      .toEqual({ available: true, next: 'bravo' });
    // The review page is as before: the hint and the bar.
    await expect(page.locator('#hint')).toHaveText(/^R record/);
    await expect(page.locator('#rec')).toBeVisible();

    await record(ui, page);
    await ui.click('#done');
    await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 15_000);
    await expect(page.locator('#nextVideo')).toBeVisible();
    await expect(page.locator('#nextVideo')).toHaveText(/Next video/);
    expect(await homeStatus(page)).toMatchObject({ alpha: 'reviewed', bravo: 'to_review', charlie: 'reviewed' });

    await page.locator('#nextVideo').click();
    await page.waitForURL(`${server.url}p/bravo/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    expect(await page.evaluate(() => window.__review.info.label)).toBe('v1');
    // Leaving alpha told the server it no longer watches v2.
    await expect.poll(() => readFileSync(server.log, 'utf8'), { timeout: 5000 })
      .toContain('"POST /p/alpha/api/versions/v2/unwatch" 204');

    await page.locator('#allVideos').click();
    await page.waitForURL(server.url);
    await openHome(page);
    expect(await cards(page)).toEqual(['bravo v1 To review', 'alpha v2 Reviewed', 'charlie v3 Reviewed']);
  } finally {
    await context.close();
  }
  const dirs = takeDirs(join(DIR, 'alpha', 'v2', 'alpha_v2.review'));
  expect(dirs).toHaveLength(1);
  validateTakeDir(dirs[0]!);
});

test('the home and the controls on a phone', async ({ browser }) => {
  const { context, page, ui } = await newPage(browser, true);
  try {
    await openHome(page);
    await thumbsLoaded(page);
    // One column, the width of the screen less its margins; nothing sticks out; every card is a big tap target.
    const boxes = await page.locator('a.card').evaluateAll((els) => els.map((e) => {
      const r = e.getBoundingClientRect();
      return { left: r.left, width: r.width, height: r.height };
    }));
    expect(boxes).toHaveLength(3);
    for (const b of boxes) {
      expect(b.left).toBe(16);
      expect(b.width).toBe(390 - 32);
      expect(b.height).toBeGreaterThan(44);
    }
    expect(await page.evaluate(overflowing)).toEqual([]);

    // A reviewed one can be reviewed again; after Done the next one to review is a thumb away.
    await page.locator('a.card[data-name="charlie"]').tap();
    await page.waitForURL(`${server.url}p/charlie/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    expect(await page.evaluate(() => document.documentElement.dataset.layout)).toBe('portrait');
    const back = await page.locator('#allVideos').boundingBox();
    expect(back && back.width >= 44 && back.height >= 44 && back.x >= 0 && back.y >= 0).toBe(true);
    await record(ui, page, true);
    await ui.tap('#done');
    await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 15_000);
    const next = page.locator('#nextVideo');
    await expect(next).toBeVisible();
    const nb = await next.boundingBox();
    expect(nb && nb.height >= 44 && nb.x + nb.width <= 390 && nb.y + nb.height <= 844).toBe(true);
    expect(await page.evaluate(overflowing)).toEqual([]);
    await next.tap();
    await page.waitForURL(`${server.url}p/bravo/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    await page.locator('#allVideos').tap();
    await page.waitForURL(server.url);
    await openHome(page);
  } finally {
    await context.close();
  }
  const dirs = takeDirs(join(DIR, 'charlie', 'v3', 'charlie_v3.review'));
  expect(dirs).toHaveLength(2);
  validateTakeDir(dirs[1]!);
});

test('> goes to the next video to review, and says when none are left', async ({ browser }) => {
  const { context, page, ui } = await newPage(browser);
  try {
    await page.goto(`${server.url}p/charlie/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    await ui.waitFor(() => (window.__review as unknown as { home: { available: boolean } }).home.available, 10_000);
    await ui.key('>');
    await page.waitForURL(`${server.url}p/bravo/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    await ui.waitFor(() => (window.__review as unknown as { home: { available: boolean } }).home.available, 10_000);
    // bravo is the last one to review: > stays, and says so.
    await ui.key('>');
    await expect(page.locator('#toast')).toHaveText('No more videos to review');
    expect(page.url()).toBe(`${server.url}p/bravo/`);
  } finally {
    await context.close();
  }
});

test('outside a home the review page is as it was', async () => {
  // retake open <video> has no home: /api/home is a 404 there, so the page shows neither control (checked by the
  // other specs' pages, which never show #allVideos); here, the folder server's own 404s.
  const res = await fetch(`${server.url}p/nope/api/info`);
  expect(res.status).toBe(404);
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
