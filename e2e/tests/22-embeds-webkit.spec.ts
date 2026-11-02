// Embeds in a script's document (spec/scripts.md, "Embeds") in WebKit, iPhone Safari's engine (the `webkit` project:
// RETAKE_E2E_WEBKIT, lib/webkit.ts), on the iPhone 13 and iPhone 15: the frames' addresses and attributes, the card
// coming in without moving anything, a tap on it opening a new tab, and nothing wider than the screen. No site is
// loaded (lib/embeds.ts). Screenshots: .retake-work/embeds-shots/webkit-<device>.png. Under Chromium the file skips.
import { devices, expect, test } from '@playwright/test';
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
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

let server: Server;
const errors: string[] = [];

test.beforeAll(async () => {
  const { dir, home } = embedsFolder('embeds-webkit');
  server = await startServer('embeds-webkit', dir, PORTS.embeds, [], { env: { XDG_CONFIG_HOME: home } });
});
test.afterAll(async () => {
  await server?.stop();
  expect(errors).toEqual([]);
});

for (const device of ['iPhone 15', 'iPhone 13'] as const) {
  test(`${device}: frames, a card and an image in the document`, async ({ browser }) => {
    const { defaultBrowserType: _, ...d } = devices[device];
    const context = await browser.newContext(d);
    const seen = await stubTheWeb(context);
    try {
      const page = await context.newPage();
      page.on('pageerror', (e) => {
        // WebKit reports a ResizeObserver that settles over two frames as an error event; it's a notice, not a fault:
        // the editor measures its block widgets (the frames' boxes) once more after they're placed.
        if (!/^ResizeObserver loop completed/.test(e.message)) errors.push(`${device}: ${e.message}`);
      });
      await openDoc(page, `${server.url}p/deadlock-refs/`);
      const card = await checkCard(page);
      await checkFrames(page);
      await toTop(page);
      await fitsWidth(page, device);
      await toTop(page);
      const name = `webkit-${device.toLowerCase().replace(/\s+/g, '')}`;
      await shot(page, name);
      // The YouTube frame at the top: it, the card and the picture together.
      await page.locator('[data-ext="scripts"] .ig-scroll').evaluate((el) => {
        const yt = el.querySelector('[data-site="YouTube"]')!.getBoundingClientRect();
        el.scrollTop += yt.top - el.getBoundingClientRect().top - 56;
      });
      await shot(page, `${name}-embeds`);
      const [tab] = await Promise.all([context.waitForEvent('page'), card.tap()]);
      await tab.waitForLoadState();
      expect(tab.url()).toBe(PAGE);
      await tab.close();
      onlyExpectedOutside(seen);
    } finally {
      await context.close();
    }
  });
}
