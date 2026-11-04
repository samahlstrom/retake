// New (spec/home.md, "Folders, projects and videos"): the home's folder tree. `retake open <folder>` serves a
// nested folder:
//
//   macro/macro_v4_roughcut.mp4                    a video's folder in the opened one: shows is its project
//   2026-10-19/fullgame/fullgame_v2.mp4            a video of the project 2026-10-19, to review
//   2026-10-19/shorts/aura_v1.mp4                  another one, reviewed (a closed take is there)
//   item-shop/v1/itemshop_v1.mp4, v2/itemshop_v2   versions in vN folders, and more inside, so item-shop is a
//   item-shop/shorts/honored/v1/honored_v1.mp4     project: its own video, and the project item-shop/shorts
//   item-shop/assets/logo.png, broll/sunset.mp4    not videos
//
// The left column shows the tree under the opened folder; a chevron opens a folder, a name selects it and the main
// area shows its videos; a video opens its review, whose "← Project › video" goes back to the same place; a hash
// link opens a place; the filter, the resize handle and ☰ work; on a phone the column is a drawer.
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, utimesSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { LEGACY, PORTS, WORK } from '../lib/env.ts';
import { READY } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';
import '../lib/home-window.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no home screen');

const DIR = join(WORK, 'nav-folder', 'shows');
let server: Server;
const pageErrors: string[] = [];


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
  put(m.v5, 'macro/macro_v4_roughcut.mp4', 5);
  put(m.v6, '2026-10-19/fullgame/fullgame_v2.mp4', 50);
  const aura = put(m.v5, '2026-10-19/shorts/aura_v1.mp4', 40);
  put(m.v5, 'item-shop/v1/itemshop_v1.mp4', 10);
  put(m.v6, 'item-shop/v2/itemshop_v2.mp4', 30);
  put(m.v7preview, 'item-shop/shorts/honored/v1/honored_v1.mp4', 20);
  put(m.v7preview, 'broll/sunset.mp4', 0);
  mkdirSync(join(DIR, 'item-shop', 'assets'), { recursive: true });
  writeFileSync(join(DIR, 'item-shop', 'assets', 'logo.png'), 'not a video');
  const done = join(dirname(aura), 'aura_v1.review', '2026-10-01_13-00-00');
  mkdirSync(done, { recursive: true });
  writeFileSync(join(done, 'session.json'), JSON.stringify({ format: 3, closed: true }));
  server = await startServer('nav', DIR, PORTS.nav, ['--verbose']);
});

test.afterAll(async () => {
  await server?.stop();
});

async function newPage(browser: Browser, phone = false): Promise<{ context: BrowserContext; page: Page }> {
  const context = phone
    ? await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true,
      deviceScaleFactor: 2 })
    : await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  return { context, page };
}

async function openHome(page: Page, hash = ''): Promise<void> {
  await page.goto(server.url + hash);
  await page.waitForFunction(() => window.__home?.loaded && window.__home.projects.length > 0, null,
    { timeout: 20_000, polling: 80 });
}

const nav = (page: Page) => page.locator('[data-part="nav"]');
const row = (page: Page, path: string) => nav(page).locator(`[role="treeitem"][data-path="${path}"]`);

/** The rows the column shows, as "title" indented by level, ▸/▾ for closed and open, and the dot. */
async function rows(page: Page): Promise<string[]> {
  return nav(page).locator('[role="treeitem"]').evaluateAll((els) => els.map((el) => {
    const level = Number(el.getAttribute('aria-level')) - 1;
    const exp = el.getAttribute('aria-expanded');
    const mark = exp === 'true' ? '▾ ' : exp === 'false' ? '▸ ' : '';
    const dot = el.querySelector('.sdot')?.getAttribute('data-dot');
    return `${'  '.repeat(level)}${mark}${el.querySelector('.label')?.textContent}${dot ? ` ·${dot}` : ''}`;
  }));
}

async function cardNames(page: Page): Promise<string[]> {
  return page.locator('main a.card').evaluateAll((els) => els.map((a) => (a as HTMLElement).dataset.name ?? ''));
}

function overflowing(): string[] {
  const W = document.documentElement.clientWidth;
  const out: string[] = [];
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if ((r.width || r.height) && (r.left < -0.5 || r.right > W + 0.5)) out.push(`${el.tagName} ${r.left}..${r.right}`);
  }
  return out;
}

test('the tree: open a folder, select a project, open a video, and come back to it', async ({ browser }) => {
  const { context, page } = await newPage(browser);
  try {
    await openHome(page);
    // The opened folder, a project with its video macro, and the projects in it, closed, with the most pressing
    // dot inside; broll (no videos) isn't there.
    expect(await rows(page)).toEqual([
      '▾ shows',
      '  macro ·to_review',
      '  ▸ 2026-10-19 ·to_review',
      '  ▸ item-shop ·to_review',
    ]);
    await expect(page.locator('h1')).toHaveText('shows');
    expect(await cardNames(page)).toHaveLength(5);

    // The chevron opens without selecting. Video folders are videos, named after the folder.
    await row(page, '2026-10-19').locator('.chev').click();
    expect(await rows(page)).toEqual([
      '▾ shows',
      '  macro ·to_review',
      '  ▾ 2026-10-19',
      '    fullgame ·to_review',
      '    shorts',
      '  ▸ item-shop ·to_review',
    ]);
    await expect(page.locator('h1')).toHaveText('shows');
    // The top row closes and opens everything.
    await row(page, '').locator('.chev').click();
    expect(await rows(page)).toEqual(['▸ shows ·to_review']);
    await row(page, '').locator('.chev').click();

    // A name selects: its videos in the main area, the hash, the breadcrumb.
    await row(page, 'item-shop').locator('.label').click();
    await expect(page.locator('h1')).toHaveText('item-shop');
    expect(new URL(page.url()).hash).toBe('#/item-shop');
    expect(await cardNames(page)).toEqual(['item-shop', 'item-shop-shorts-honored']);
    await expect(page.locator('#todo')).toHaveText('2 videos · 2 to review');
    await expect(row(page, 'item-shop')).toHaveAttribute('aria-selected', 'true');
    expect((await rows(page)).slice(-3)).toEqual(['  ▾ item-shop', '    itemshop ·to_review',
      '    ▸ shorts ·to_review']);
    await expect(page.locator('main a.card[data-name="item-shop"] .title')).toHaveText('itemshop');

    // A project's cards; at the top, each card names its project when that says more.
    await row(page, '2026-10-19').locator('.label').click();
    expect(await cardNames(page)).toEqual(['2026-10-19-fullgame', '2026-10-19-shorts']);
    await expect(page.locator('main a.card[data-name="2026-10-19-fullgame"] .title')).toHaveText('fullgame');
    await expect(page.locator('main a.card[data-name="2026-10-19-fullgame"] .where')).toHaveCount(0);
    await row(page, '').locator('.label').click();
    await expect(page.locator('main a.card[data-name="2026-10-19-fullgame"] .where')).toHaveText('2026-10-19');
    await row(page, 'item-shop').locator('.label').click();

    // A video in the column opens its review; its back link names the project and goes back to where it was.
    await row(page, 'item-shop/shorts').locator('.chev').click();
    await nav(page).locator('a[data-name="item-shop-shorts-honored"]').click();
    await page.waitForURL(`${server.url}p/item-shop-shorts-honored/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    const back = page.locator('#allVideos');
    await expect(back).toHaveText('shorts › honored');
    await expect(back).toHaveAttribute('href', '/#/item-shop');
    await back.click();
    await page.waitForURL(`${server.url}#/item-shop`);
    await expect(page.locator('h1')).toHaveText('item-shop');
    await expect(row(page, 'item-shop')).toHaveAttribute('aria-selected', 'true');

    // The project's own video: the crumb is the project alone (item-shop and itemshop are one name).
    await page.locator('main a.card[data-name="item-shop"]').click();
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    await expect(page.locator('#allVideos')).toHaveText('item-shop');
    // Browser Back too.
    await page.goBack();
    await page.waitForURL(`${server.url}#/item-shop`);
    await expect(page.locator('h1')).toHaveText('item-shop');
    // A video's folder in the opened folder: "shows › macro".
    await page.goto(`${server.url}p/macro/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    await expect(page.locator('#allVideos')).toHaveText('shows › macro');
  } finally {
    await context.close();
  }
});

test('a link with a hash opens that place; the rest is remembered', async ({ browser }) => {
  const { context, page } = await newPage(browser);
  try {
    await openHome(page, '#/item-shop/shorts');
    await expect(page.locator('h1')).toHaveText('shorts');
    expect(await cardNames(page)).toEqual(['item-shop-shorts-honored']);
    await expect(row(page, 'item-shop/shorts')).toHaveAttribute('aria-selected', 'true');
    await expect(row(page, 'item-shop')).toHaveAttribute('aria-expanded', 'true');
    await expect(page.locator('.breadcrumbs')).toHaveText(/Retake\.\s*\/\s*item-shop\s*\/\s*shorts/); // the wordmark: Retake and its red period

    // A place that's gone shows everything.
    await page.goto(`${server.url}#/nope`);
    await page.waitForFunction(() => window.__home?.loaded);
    await expect(page.locator('h1')).toHaveText('shows');

    // The filter: matches and the way to them.
    await page.goto(server.url);
    await page.locator('[data-part="nav-search"]').fill('HONOR');
    expect(await rows(page)).toEqual(['▾ shows', '  ▾ item-shop', '    ▾ shorts', '      honored ·to_review']);
    await page.locator('[data-part="nav-search"]').fill('zzz');
    await expect(nav(page).getByText('Nothing matches “zzz”.')).toBeVisible();
    await page.locator('[data-part="nav-search"]').fill('');

    // The keyboard: ← closes, → opens, ↑ and ↓ move, Enter selects.
    const folder = row(page, '2026-10-19');
    await folder.focus();
    if ((await folder.getAttribute('aria-expanded')) === 'true') await page.keyboard.press('ArrowLeft');
    await expect(folder).toHaveAttribute('aria-expanded', 'false');
    await page.keyboard.press('ArrowRight');
    await expect(folder).toHaveAttribute('aria-expanded', 'true');
    await page.keyboard.press('ArrowUp'); // macro, a video
    await expect(nav(page).locator('a[data-name="macro"]')).toBeFocused();
    await page.keyboard.press('Home');
    await page.keyboard.press('Enter');
    await expect(page.locator('h1')).toHaveText('shows');
    await folder.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('h1')).toHaveText('2026-10-19');

    // The resize handle, and ☰ hiding the column: kept over a reload.
    const grip = nav(page).locator('[data-part="nav-grip"]');
    const box = (await grip.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + 200);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 80, box.y + 200, { steps: 4 });
    await page.mouse.up();
    const width = await nav(page).evaluate((el) => el.getBoundingClientRect().width);
    expect(width).toBeGreaterThan(330);
    await page.reload();
    await page.waitForFunction(() => window.__home?.loaded);
    expect(await nav(page).evaluate((el) => el.getBoundingClientRect().width)).toBe(width);
    await expect(page.locator('h1')).toHaveText('2026-10-19'); // the selection, remembered
    await page.locator('[data-part="nav-toggle"]').click();
    await expect(nav(page)).toBeHidden();
    const left = await page.locator('main a.card').first().evaluate((el) => el.getBoundingClientRect().left);
    expect(left).toBeLessThan(100);
    await page.reload();
    await expect(nav(page)).toBeHidden();
    await page.locator('[data-part="nav-toggle"]').click();
    await expect(nav(page)).toBeVisible();
  } finally {
    await context.close();
  }
});

test('on a phone the folders are a drawer', async ({ browser }) => {
  const { context, page } = await newPage(browser, true);
  try {
    await openHome(page);
    await expect(nav(page)).toBeHidden();
    expect(await page.evaluate(overflowing)).toEqual([]);
    const toggle = page.locator('[data-part="nav-toggle"]');
    const tb = (await toggle.boundingBox())!;
    expect(tb.width >= 44 && tb.height >= 44 && tb.x >= 0).toBe(true);
    await toggle.tap();
    await expect(nav(page)).toBeVisible();
    await expect.poll(async () => (await nav(page).boundingBox())?.x).toBe(0); // after it slid in
    expect((await nav(page).boundingBox())!.width).toBeLessThanOrEqual(340);
    const rb = (await row(page, 'item-shop').boundingBox())!;
    expect(rb.height).toBeGreaterThanOrEqual(44);
    await row(page, 'item-shop').locator('.label').tap();
    await expect(nav(page)).toBeHidden();
    await expect(page.locator('h1')).toHaveText('item-shop');
    expect(await cardNames(page)).toEqual(['item-shop', 'item-shop-shorts-honored']);
    // Escape and the backdrop close it too.
    await toggle.tap();
    await expect(nav(page)).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(nav(page)).toBeHidden();
    await toggle.tap();
    // The Feedback tab occupies the middle of the right edge; choose the backdrop above it.
    await page.locator('.scrim').click({ position: { x: 380, y: 100 } });
    await expect(nav(page)).toBeHidden();
    expect(await page.evaluate(overflowing)).toEqual([]);
  } finally {
    await context.close();
  }
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
