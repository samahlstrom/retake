// One version menu in one place per layout and the home's cards while a version is live, on an iPhone 15 in WebKit,
// iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT, lib/webkit.ts). The story is lib/version-menu.ts.
// Screenshots: .retake-work/version-menu-shots/<what>-webkit-iphone15.png. Under Chromium the file skips.
import { devices, test } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { homeCards, phoneMenu, release, startMenuHome, type MenuFixture } from '../lib/version-menu.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no home or agent');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

let fixture: MenuFixture | undefined;
test.afterEach(async () => {
  if (fixture) release(fixture);
  await fixture?.server.stop();
  fixture = undefined;
});

test('iPhone 15 (WebKit): the one version menu in the controls row while v3 is live, and the home', async ({ browser }) => {
  fixture = await startMenuHome('webkit', PORTS.progress);
  const { defaultBrowserType: _, ...phone } = devices['iPhone 15'];
  const context = await browser.newContext(phone);
  try {
    const page = await context.newPage();
    await phoneMenu(page, fixture, 'webkit-iphone15');
    await homeCards(page, fixture, 'webkit-iphone15');
  } finally {
    await context.close();
  }
});
