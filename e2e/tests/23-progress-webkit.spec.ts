// The agent marks its comments one by one, in the live v2 (spec/automation.md, "Marking progress"), on an iPhone 15 in
// WebKit, iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT, lib/webkit.ts). The story is
// lib/progress.ts. Screenshots: .retake-work/progress-shots/<what>-webkit-iphone15.png. Under Chromium the file skips.
import { devices, test } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { progressStory, startProgress, type Fixture } from '../lib/progress.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no agent');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

let fixture: Fixture | undefined;
test.afterEach(async () => {
  await fixture?.server.stop();
  fixture = undefined;
});

test('iPhone 15 (WebKit): comments finish one at a time, in the live v2', async ({ browser }) => {
  fixture = await startProgress('webkit', PORTS.progress);
  const { defaultBrowserType: _, ...phone } = devices['iPhone 15'];
  const context = await browser.newContext(phone);
  try {
    await progressStory(await context.newPage(), fixture, 'webkit-iphone15');
  } finally {
    await context.close();
  }
});
