// Typing on a phone in Chromium's mobile emulation, on the iPhone 15's viewport (spec/notes.md, "Typing on a phone"):
// the same checks as 23-keyboard-webkit.spec.ts (lib/keyboard.ts), with the on-screen keyboard simulated through the
// visual viewport. Screenshots go to .retake-work/keyboard-shots/chromium-<what>.png.
import { devices, expect, test, type BrowserContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { LEGACY } from '../lib/env.ts';
import { keyboardServer, scriptKeyboard, SHOTS, videoKeyboard } from '../lib/keyboard.ts';
import type { Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no phone layout');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium only (23-keyboard-webkit.spec.ts is WebKit’s)');

const errors: string[] = [];

test.describe('typing on a phone in Chromium', () => {
  let server: Server;
  let context: BrowserContext;
  let page: Page;
  test.beforeAll(async ({ browser }) => {
    server = await keyboardServer('keyboard');
    const { defaultBrowserType: _, ...d } = devices['iPhone 15'];
    context = await browser.newContext(d);
    page = await context.newPage();
    page.on('pageerror', (e) => errors.push(`${e.name}: ${e.message}`));
  });
  test.afterAll(async () => {
    await context?.close();
    await server?.stop();
    expect(errors).toEqual([]);
  });

  const shot = async (what: string) => {
    mkdirSync(SHOTS, { recursive: true });
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(SHOTS, `chromium-${what}.png`) });
  };

  test("a video's comment box on the keyboard, compose mode and back", async () => {
    await videoKeyboard(page, server.url, 'chromium', shot);
  });

  test("a script's chat sheet on the keyboard, and typing in the document", async () => {
    await scriptKeyboard(page, server.url, 'chromium', shot);
  });
});
