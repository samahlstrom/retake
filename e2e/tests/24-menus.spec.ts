// New (spec/home.md, "A video's ⋯"; spec/notes.md, "The video's ⋯"; spec/team.md, "Sharing a video from the page";
// spec/automation.md, "An agent for one video"). A folder of two videos (lib/menus.ts). On a computer and on an iPhone
// 15 (Chromium): a card's ⋯ shows on hover (always, on a phone); Share… makes a link through a tunnel the server
// starts (a stand-in for cloudflared), and a fresh browser opening it sees that video only and comments with the
// link's name; Move to Trash moves the folder into a scratch trash, and Undo puts it back; Show in folder runs the
// RETAKE_REVEAL stand-in; Agent for this video sends that video's comments to the other agent, and nothing about it
// shows near Send. Screenshots: .retake-work/menus-shots/.
import { devices, expect, test, type Browser, type Page } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { FailingProxy } from '../lib/proxy.ts';
import { READY } from '../lib/review.ts';
import {
  TUNNEL_HOST,
  automation,
  card,
  cardMenu,
  menu,
  openHome,
  pickAgentOnCard,
  revealedFolder,
  shareFromCard,
  shot,
  startMenus,
  trashAndUndo,
  type MenusFixture,
} from '../lib/menus.ts';
import { join } from 'node:path';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no video menu');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium; 24-menus-webkit.spec.ts is the WebKit phone');

let f: MenusFixture;
let proxy: FailingProxy;
const pageErrors: string[] = [];

test.beforeAll(async () => {
  f = await startMenus('chromium', PORTS.menus);
  // The tunnel's stand-in: requests reach the server with the tunnel's Host, as through cloudflared.
  proxy = new FailingProxy(PORTS.menusProxy, PORTS.menus, { host: TUNNEL_HOST });
  await proxy.start();
});

test.afterAll(async () => {
  await proxy?.stop();
  await f?.server.stop();
  expect(pageErrors).toEqual([]);
});

const { defaultBrowserType: _, ...iphone } = devices['iPhone 15'];

async function open(browser: Browser, phone: boolean): Promise<Page> {
  const context = await browser.newContext(phone ? iphone : { viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  await openHome(page, f);
  return page;
}

test('a card’s ⋯ shows on hover on a computer and lists what the owner can do', async ({ browser }) => {
  const page = await open(browser, false);
  try {
    const more = cardMenu(page, 'alpha');
    await expect(more).toHaveCSS('opacity', '0');
    await card(page, 'alpha').hover();
    await expect(more).toHaveCSS('opacity', '1');
    await more.click();
    await expect(menu(page).getByRole('menuitem')).toHaveText([
      'Share…',
      'Agent for this video',
      'Show in folder',
      'Move to Trash…',
    ]);
    await shot(page, 'card-menu', 'desktop');
    await menu(page).getByRole('menuitem', { name: 'Show in folder' }).click();
    await expect.poll(() => revealedFolder(f)).toBe(join(f.videos, 'alpha'));
    // Opening the menu doesn't open the video.
    expect(new URL(page.url()).pathname).toBe('/');
  } finally {
    await page.context().close();
  }
});

test('Share… makes a link that opens that video only, and their comments carry its name', async ({ browser }) => {
  const page = await open(browser, false);
  let url = '';
  try {
    url = await shareFromCard(page, 'alpha', 'Alex', 'desktop');
  } finally {
    await page.context().close();
  }
  const tok = url.split('/s/')[1]!;
  const guest = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  try {
    const g = await guest.newPage();
    g.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
    await g.goto(`${proxy.url}s/${tok}`);
    await g.waitForURL(`${proxy.url}p/alpha/`, { timeout: 10_000 });
    await g.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    // That video only.
    const home = await g.evaluate(async () => (await (await fetch('/api/home')).json()) as { projects: { name: string }[] });
    expect(home.projects.map((p) => p.name)).toEqual(['alpha']);
    expect(await g.evaluate(async () => (await fetch('/p/bravo/api/info')).status)).toBe(403);
    // No ⋯ for a reviewer's link (sharing and the agent are the owner's).
    await expect(g.locator('[data-part~="video-menu"]')).toHaveCount(0);
    // A typed comment, with the link's name.
    const box = g.locator('#side [data-part~="comment-line"] textarea');
    await box.click();
    await box.fill('Love this shot');
    await box.press('Enter');
    await expect
      .poll(async () => {
        const r = await fetch(`${f.server.url}p/alpha/api/notes?scope=video`);
        const b = (await r.json()) as { items?: { text: string; by: string }[] };
        return (b.items ?? []).find((n) => n.text === 'Love this shot')?.by ?? '';
      }, { timeout: 15_000 })
      .toBe('Alex');
  } finally {
    await guest.close();
  }
});

for (const phone of [false, true]) {
  const device = phone ? 'iphone15' : 'desktop';
  test(`Move to Trash asks, moves the folder to the trash, and Undo puts it back (${device})`, async ({ browser }) => {
    const page = await open(browser, phone);
    try {
      if (phone) expect(Number(await cardMenu(page, 'bravo').evaluate((e) => getComputedStyle(e).opacity))).toBeGreaterThan(0.3);
      await trashAndUndo(page, f, 'bravo', device);
    } finally {
      await page.context().close();
    }
  });
}

test('Agent for this video: its comments go to that agent, and nothing about it shows near Send', async ({ browser }) => {
  const page = await open(browser, false);
  try {
    await pickAgentOnCard(page, f, 'alpha', 'desktop');
    await page.goto(`${f.server.url}p/alpha/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    // The review page's ⋯ has the same list, with Agent Two checked.
    const more = page.locator('[data-part~="video-menu"]');
    await expect(more).toBeVisible();
    await more.click();
    await expect(menu(page).getByRole('menuitem')).toHaveText(['Share…', 'Agent for this video']);
    await menu(page).getByRole('menuitem', { name: 'Agent for this video' }).click();
    await expect(page.locator('.vm-agents').getByRole('menuitem', { name: 'Agent Two' })).toContainText('✓');
    await shot(page, 'review-menu', 'desktop');
    await page.keyboard.press('Escape');
    await page.keyboard.press('Escape');
    await expect(page.locator('#bar')).not.toContainText('Agent');
    // A comment, sent with Send: its task is Agent Two's.
    await page.evaluate(async () => {
      await fetch('api/notes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version: 'v1', t: 1, text: 'Tighten the start' }),
      });
    });
    await page.locator('[data-part~="send"]').first().click();
    await expect.poll(async () => (await automation(f, 'alpha')).tasks.map((t) => t.agent), { timeout: 15_000 }).toEqual([
      'two',
    ]);
  } finally {
    await page.context().close();
  }
});

test('on an iPhone 15 the ⋯ is always there, and the share sheet and agent list fit the screen', async ({ browser }) => {
  const page = await open(browser, true);
  try {
    const more = cardMenu(page, 'bravo');
    expect(Number(await more.evaluate((e) => getComputedStyle(e).opacity))).toBeGreaterThan(0.3);
    const box = (await more.boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(44);
    await more.click();
    await shot(page, 'card-menu', 'iphone15');
    await page.keyboard.press('Escape');
    await shareFromCard(page, 'bravo', 'Sam', 'iphone15');
    const sheet = (await page.locator('[data-part~="share-sheet"]').boundingBox())!;
    const vw = page.viewportSize()!;
    expect(sheet.x).toBeGreaterThanOrEqual(0);
    expect(sheet.x + sheet.width).toBeLessThanOrEqual(vw.width);
    await page.keyboard.press('Escape');
    await page.goto(`${f.server.url}p/bravo/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    const review = page.locator('[data-part~="video-menu"]');
    await expect(review).toBeVisible();
    expect(((await review.boundingBox())!).width).toBeGreaterThanOrEqual(44);
    await review.click();
    await shot(page, 'review-menu', 'iphone15');
  } finally {
    await page.context().close();
  }
});
