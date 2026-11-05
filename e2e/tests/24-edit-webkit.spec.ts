// Editing a comment's words on a phone in WebKit, iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT,
// lib/webkit.ts), on the iPhone 15 (spec/notes.md, "Editing a comment"): a tap on a comment's words seeks, then
// ⋯ → Edit puts its words in the comment box on the simulated keyboard (lib/edit.ts, lib/keyboard.ts); Return saves;
// Cancel changes nothing; a reload keeps the words. Screenshots go to
// .retake-work/edit-shots/webkit-<what>.png. Under Chromium the file skips (24-edit.spec.ts is Chromium's).
import { devices, expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { LEGACY, PORTS } from '../lib/env.ts';
import { editServer, phoneEdit, SHOTS } from '../lib/edit.ts';
import type { Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no phone layout');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

const errors: string[] = [];
let server: Server;

test.beforeAll(async () => {
  server = await editServer('webkit', PORTS.batch);
});
test.afterAll(async () => {
  await server?.stop();
  expect(errors).toEqual([]);
});

test('iPhone 15: text seeks; menu Edit opens the comment box on the keyboard', async ({ browser }) => {
  const { defaultBrowserType: _, ...d } = devices['iPhone 15'];
  const context = await browser.newContext(d);
  try {
    const page = await context.newPage();
    page.on('pageerror', (e) => errors.push(`${e.name}: ${e.message}`));
    await phoneEdit(page, server.url, 'webkit', async (what) => {
      mkdirSync(SHOTS, { recursive: true });
      await page.waitForTimeout(300);
      await page.screenshot({ path: join(SHOTS, `webkit-${what}.png`) });
    });
  } finally {
    await context.close();
  }
});
