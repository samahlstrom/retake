// Editing a comment's words (spec/notes.md, "Editing a comment") in Chromium: on a computer, a voice comment and a
// typed one, kept through the take's final pass and a reload; on the iPhone 15 (mobile emulation), a text tap seeks
// and ⋯ → Edit opens the comment box on the simulated keyboard (lib/edit.ts, lib/keyboard.ts). 24-edit-webkit.spec.ts
// is WebKit's phone.
// Screenshots go to .retake-work/edit-shots/chromium-<what>.png.
import { devices, expect, test, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { LEGACY, PORTS } from '../lib/env.ts';
import { desktopEdit, editServer, phoneEdit, SHOTS } from '../lib/edit.ts';
import type { Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no comment editing');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium only (24-edit-webkit.spec.ts is WebKit’s)');

const errors: string[] = [];
let server: Server;

test.beforeAll(async () => {
  server = await editServer('chromium', PORTS.batch); // 20-batch's port: it stopped its server
});
test.afterAll(async () => {
  await server?.stop();
  expect(errors).toEqual([]);
});

const shooter = (page: Page) => async (what: string) => {
  mkdirSync(SHOTS, { recursive: true });
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(SHOTS, `chromium-${what}.png`) });
};

test('a computer: edit a voice comment and a typed one; the words stay', async ({ page }) => {
  page.on('pageerror', (e) => errors.push(`${e.name}: ${e.message}`));
  await desktopEdit(page, server.url, shooter(page));
});

test('iPhone 15: text seeks; menu Edit opens the comment box on the keyboard', async ({ browser }) => {
  const { defaultBrowserType: _, ...d } = devices['iPhone 15'];
  const context = await browser.newContext(d);
  try {
    const page = await context.newPage();
    page.on('pageerror', (e) => errors.push(`iPhone 15: ${e.name}: ${e.message}`));
    await phoneEdit(page, server.url, 'chromium', shooter(page));
  } finally {
    await context.close();
  }
});
