// A video's ⋯ on an iPhone 15 in WebKit, iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT,
// lib/webkit.ts). The story is lib/menus.ts: the card's ⋯ is always there and thumb-sized, Share… makes a link through
// the stand-in tunnel and the sheet fits the screen, Move to Trash asks and Undo puts the folder back, and Agent for this
// video picks the other agent; the review page's ⋯ lists it checked. Screenshots: .retake-work/menus-shots/<what>-
// webkit-iphone15.png. Under Chromium the file skips (24-menus.spec.ts runs there).
import { devices, expect, test, type Page } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { READY } from '../lib/review.ts';
import {
  cardMenu,
  menu,
  openHome,
  pickAgentOnCard,
  shareFromCard,
  shot,
  startMenus,
  trashAndUndo,
  type MenusFixture,
} from '../lib/menus.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no video menu');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

const DEVICE = 'webkit-iphone15';
let f: MenusFixture;
const pageErrors: string[] = [];

test.beforeAll(async () => {
  f = await startMenus('webkit', PORTS.menus);
});
test.afterAll(async () => {
  await f?.server.stop();
  expect(pageErrors).toEqual([]);
});

async function phonePage(browser: import('@playwright/test').Browser): Promise<Page> {
  const { defaultBrowserType: _, ...phone } = devices['iPhone 15'];
  const context = await browser.newContext(phone);
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  await openHome(page, f);
  return page;
}

test('iPhone 15 (WebKit): the card’s ⋯, Share…, Move to Trash and Undo', async ({ browser }) => {
  const page = await phonePage(browser);
  try {
    const more = cardMenu(page, 'bravo');
    expect(Number(await more.evaluate((e) => getComputedStyle(e).opacity))).toBeGreaterThan(0.3);
    expect((await more.boundingBox())!.width).toBeGreaterThanOrEqual(44);
    await more.tap();
    await expect(menu(page).getByRole('menuitem')).toHaveText([
      'Share…',
      'Agent for this video',
      'Download v1 (MP4)',
      'Show in folder',
      'Move to Trash…',
    ]);
    await shot(page, 'card-menu', DEVICE);
    await page.keyboard.press('Escape');
    await shareFromCard(page, 'bravo', 'Sam', DEVICE);
    const sheet = (await page.locator('[data-part~="share-sheet"]').boundingBox())!;
    const vw = page.viewportSize()!;
    expect(sheet.x).toBeGreaterThanOrEqual(0);
    expect(sheet.x + sheet.width).toBeLessThanOrEqual(vw.width);
    expect(sheet.y + sheet.height).toBeLessThanOrEqual(vw.height);
    await page.keyboard.press('Escape');
    await expect(page.locator('[data-part~="share-sheet"]')).toBeHidden();
    await trashAndUndo(page, f, 'bravo', DEVICE);
  } finally {
    await page.context().close();
  }
});

test('iPhone 15 (WebKit): Agent for this video, checked on the review page too', async ({ browser }) => {
  const page = await phonePage(browser);
  try {
    await pickAgentOnCard(page, f, 'alpha', DEVICE);
    await page.goto(`${f.server.url}p/alpha/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    const more = page.locator('[data-part~="video-menu"]');
    await expect(more).toBeVisible();
    expect((await more.boundingBox())!.width).toBeGreaterThanOrEqual(44);
    await more.tap();
    await menu(page).getByRole('menuitem', { name: 'Agent for this video' }).tap();
    await expect(page.locator('.vm-agents').getByRole('menuitem', { name: 'Agent Two' })).toContainText('✓');
    await shot(page, 'review-menu', DEVICE);
  } finally {
    await page.context().close();
  }
});
