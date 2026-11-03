// The agent marks its comments one by one, and the version it makes is live (spec/automation.md, "Marking progress"
// and "The live version"), on a computer and on a phone in Chromium: the story is lib/progress.ts. WebKit runs the
// phone in 23-progress-webkit.spec.ts. Screenshots: .retake-work/progress-shots/<what>-<device>.png.
import { devices, test } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { progressStory, startProgress, type Fixture } from '../lib/progress.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no agent');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium; WebKit runs 23-progress-webkit.spec.ts');

let fixture: Fixture | undefined;
test.afterEach(async () => {
  await fixture?.server.stop();
  fixture = undefined;
});

test('computer: comments finish one at a time, in the live v2', async ({ browser }) => {
  fixture = await startProgress('desktop', PORTS.progress);
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  try {
    await progressStory(await context.newPage(), fixture, 'desktop');
  } finally {
    await context.close();
  }
});

test('iPhone 15: comments finish one at a time, in the live v2', async ({ browser }) => {
  fixture = await startProgress('phone', PORTS.progress);
  const { defaultBrowserType: _, ...phone } = devices['iPhone 15'];
  const context = await browser.newContext(phone);
  try {
    await progressStory(await context.newPage(), fixture, 'iphone15');
  } finally {
    await context.close();
  }
});
