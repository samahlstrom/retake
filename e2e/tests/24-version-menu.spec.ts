// One version menu in one place per layout, and the home while a version is live (spec/notes.md, "The version menu";
// spec/home.md, "A version's name, never draft" and "Every card has a picture"), on a computer and an iPhone 15 in
// Chromium: the story is lib/version-menu.ts. WebKit runs the phone in 24-version-menu-webkit.spec.ts. Screenshots:
// .retake-work/version-menu-shots/.
import { devices, test } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { desktopMenu, homeCards, phoneMenu, release, startMenuHome, type MenuFixture } from '../lib/version-menu.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no home or agent');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium; WebKit runs 24-version-menu-webkit.spec.ts');

let fixture: MenuFixture | undefined;
test.beforeAll(async () => {
  fixture = await startMenuHome('chromium', PORTS.progress);
});
test.afterAll(async () => {
  if (fixture) release(fixture);
  await fixture?.server.stop();
});

test('computer: the bar has the one version menu while v3 is live; the home says v3 with a picture on every card', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  try {
    const page = await context.newPage();
    await desktopMenu(page, fixture!);
    await homeCards(page, fixture!, 'desktop');
  } finally {
    await context.close();
  }
});

test('iPhone 15: the controls row has the one version menu, roomy rows, no bubble; the home on a phone', async ({ browser }) => {
  const { defaultBrowserType: _, ...phone } = devices['iPhone 15'];
  const context = await browser.newContext(phone);
  try {
    const page = await context.newPage();
    await phoneMenu(page, fixture!, 'iphone15');
    await homeCards(page, fixture!, 'iphone15');
  } finally {
    await context.close();
  }
});
