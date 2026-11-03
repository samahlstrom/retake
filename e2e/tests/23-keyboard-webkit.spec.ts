// Typing on a phone in WebKit, iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT, lib/webkit.ts), on the
// iPhone 13 and iPhone 15 viewports (spec/notes.md, "Typing on a phone"). The on-screen keyboard is simulated through
// the visual viewport (lib/keyboard.ts): on a video the comment box sits on the keyboard with nothing else moving and
// the rest hidden, a pan is followed, and leaving puts everything back with the list's scroll; on a script the chat
// sheet sits on the keyboard with nothing behind it moving, and typing in the document keeps the caret's line above
// the keyboard with the bar hidden. Screenshots go to .retake-work/keyboard-shots/webkit-<what>-<device>.png. Under
// Chromium the file skips (23-keyboard.spec.ts is Chromium's).
import { devices, expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { LEGACY } from '../lib/env.ts';
import { keyboardServer, scriptKeyboard, SHOTS, videoKeyboard } from '../lib/keyboard.ts';
import type { Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no phone layout');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

const PHONES = ['iPhone 13', 'iPhone 15'] as const;
const slug = (d: string) => d.toLowerCase().replace(/\s+/g, '');
const errors: string[] = [];

async function phonePage(browser: Browser, device: (typeof PHONES)[number]): Promise<{ context: BrowserContext; page: Page }> {
  const { defaultBrowserType: _, ...d } = devices[device];
  const context = await browser.newContext(d);
  const page = await context.newPage();
  page.on('pageerror', (e) => errors.push(`${device}: ${e.name}: ${e.message}`));
  return { context, page };
}

const shooter = (page: Page, device: string) => async (what: string) => {
  mkdirSync(SHOTS, { recursive: true });
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(SHOTS, `webkit-${what}-${slug(device)}.png`) });
};

test.describe('typing on a phone in WebKit', () => {
  let server: Server;
  test.beforeAll(async () => {
    server = await keyboardServer('keyboard-webkit');
  });
  test.afterAll(async () => {
    await server?.stop();
    expect(errors).toEqual([]);
  });

  for (const device of PHONES) {
    test(`${device}: a video's comment box on the keyboard, compose mode and back`, async ({ browser }) => {
      const { context, page } = await phonePage(browser, device);
      try {
        await videoKeyboard(page, server.url, device, shooter(page, device));
      } finally {
        await context.close();
      }
    });

    test(`${device}: a script's chat sheet on the keyboard, and typing in the document`, async ({ browser }) => {
      const { context, page } = await phonePage(browser, device);
      try {
        await scriptKeyboard(page, server.url, device, shooter(page, device));
      } finally {
        await context.close();
      }
    });
  }
});
