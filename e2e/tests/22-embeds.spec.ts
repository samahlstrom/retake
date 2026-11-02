// Embeds in a script's document (spec/scripts.md, "Embeds"), in Chromium on a desktop and a phone: a YouTube address
// alone on its line plays in a frame on youtube-nocookie with exactly the frame attributes, a raw <iframe> is rebuilt
// with only its address and size, a Twitch clip names this page as its parent, an unknown page is a card whose title
// comes in without moving anything, a link in a sentence stays a link, the caret on an address shows it as typed with
// the same frame under it, a tapped card opens a new tab, and nothing is wider than a phone. No site is loaded: the
// test answers every outside request itself (lib/embeds.ts). WebKit: 22-embeds-webkit.
import { devices, expect, test, type Page } from '@playwright/test';
import {
  checkCard,
  checkFrames,
  embedsFolder,
  fitsWidth,
  onlyExpectedOutside,
  openDoc,
  PAGE,
  shot,
  stubTheWeb,
  toTop,
} from '../lib/embeds.ts';
import { LEGACY, PORTS } from '../lib/env.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no extensions');

let server: Server;
const errors: string[] = [];
const watch = (page: Page, where: string) => page.on('pageerror', (e) => errors.push(`${where}: ${e.message}`));

test.beforeAll(async () => {
  const { dir, home } = embedsFolder('embeds');
  server = await startServer('embeds', dir, PORTS.embeds, [], { env: { XDG_CONFIG_HOME: home } });
});
test.afterAll(async () => {
  await server?.stop();
  expect(errors).toEqual([]);
});

test('desktop: frames, a card, a link in a sentence, the caret on an address', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const seen = await stubTheWeb(context);
  try {
    const page = await context.newPage();
    watch(page, 'desktop');
    const text = await openDoc(page, `${server.url}p/deadlock-refs/`);
    const card = await checkCard(page);
    await checkFrames(page);
    await toTop(page);

    // The address line folds away off the caret; with the caret on it, it shows as typed and the same frame stays.
    const frame = page.locator('[data-part="scripts-embed"][data-site="YouTube"] iframe');
    await frame.evaluate((f) => ((f as HTMLIFrameElement & { tagged?: boolean }).tagged = true));
    const line = page.locator('[data-ext="scripts"] .cm-line', { hasText: 'youtube.com/watch' });
    expect((await line.boundingBox())?.height ?? 0).toBeLessThan(1);
    await page.locator('[data-ext="scripts"] .cm-line.ig-h1').click();
    await page.keyboard.press('End');
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('ArrowDown');
    await expect.poll(async () => (await line.boundingBox())?.height ?? 0).toBeGreaterThan(10);
    await expect(line).toHaveText('https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42');
    expect(await frame.evaluate((f) => (f as HTMLIFrameElement & { tagged?: boolean }).tagged)).toBe(true);
    await text.press('Escape');
    await expect.poll(async () => (await line.boundingBox())?.height ?? 0).toBeLessThan(1);

    // A click on the card opens the page in a new tab.
    const [tab] = await Promise.all([context.waitForEvent('page'), card.click()]);
    await tab.waitForLoadState();
    expect(tab.url()).toBe(PAGE);
    await tab.close();

    await fitsWidth(page, 'desktop');
    await toTop(page);
    await shot(page, 'chromium-desktop');
    onlyExpectedOutside(seen);
  } finally {
    await context.close();
  }
});

test('phone: the same, never wider than the screen', async ({ browser }) => {
  const { defaultBrowserType: _, ...phone } = devices['iPhone 15'];
  const context = await browser.newContext(phone);
  const seen = await stubTheWeb(context);
  try {
    const page = await context.newPage();
    watch(page, 'phone');
    await openDoc(page, `${server.url}p/deadlock-refs/`);
    const card = await checkCard(page);
    await checkFrames(page);
    await toTop(page);
    await fitsWidth(page, 'phone');
    await shot(page, 'chromium-iphone15');
    const [tab] = await Promise.all([context.waitForEvent('page'), card.tap()]);
    await tab.waitForLoadState();
    expect(tab.url()).toBe(PAGE);
    await tab.close();
    onlyExpectedOutside(seen);
  } finally {
    await context.close();
  }
});
