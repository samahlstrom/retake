// The setup page (spec/setup.md) in WebKit, iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT,
// lib/webkit.ts), on the iPhone 13 and iPhone 15: it loads, nothing is wider than the screen, the folder list opens
// and its buttons are big enough for a thumb, and Test works with a fake agent. Under Chromium the file skips.
import { devices, expect, test } from '@playwright/test';
import { chmodSync, copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { LEGACY, PORTS, WORK } from '../lib/env.ts';
import { startRetake, type Server } from '../lib/server.ts';
import '../lib/home-window.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no setup');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

const ROOT = join(WORK, 'setup-webkit');
const SHOTS = process.env.RETAKE_E2E_SHOTS;
let server: Server;
const errors: string[] = [];

test.beforeAll(async () => {
  const home = join(ROOT, 'home');
  const clip = join(home, 'Videos', 'alpha', 'v1', 'alpha_v1.mp4');
  mkdirSync(dirname(clip), { recursive: true });
  copyFileSync(mediaLayout(WORK).v5, clip);
  const bin = join(ROOT, 'bin');
  mkdirSync(bin, { recursive: true });
  writeFileSync(join(bin, 'claude'), `#!/bin/sh\necho '{"type":"result","is_error":false,"result":"ready"}'\n`);
  chmodSync(join(bin, 'claude'), 0o755);
  server = await startRetake('setup-webkit', ['start', '--no-open', '--port', String(PORTS.setup), '--idle-exit', '0'],
    PORTS.setup, {
      HOME: home, XDG_CONFIG_HOME: join(ROOT, 'config'), XDG_STATE_HOME: join(ROOT, 'state'),
      PATH: `${bin}:/usr/bin:/bin`,
    });
});
test.afterAll(async () => {
  await server?.stop();
  expect(errors).toEqual([]);
});

for (const device of ['iPhone 15', 'iPhone 13'] as const) {
  test(`${device}: the setup page fits, the folder list opens, Test works`, async ({ browser }) => {
    const { defaultBrowserType: _, ...d } = devices[device];
    const context = await browser.newContext(d);
    try {
      const page = await context.newPage();
      page.on('pageerror', (e) => errors.push(`${device}: ${e.message}`));
      await page.goto(server.url);
      await page.waitForFunction(() => window.__setup?.loaded, null, { timeout: 20_000, polling: 80 });
      await expect(page.getByTestId('setup-folder')).toHaveText('~/Videos');

      const claude = page.getByRole('radiogroup', { name: 'Your agent' }).locator('.choice', { hasText: 'Claude Code' });
      await claude.getByRole('button', { name: 'Test' }).click();
      await expect(claude).toContainText(/Works · (<1|\d+) s/);

      await page.getByRole('button', { name: 'Change' }).click();
      const picker = page.locator('[data-part~="folder-picker"]');
      await expect(picker.getByRole('button', { name: 'alpha' })).toBeVisible();
      for (const name of ['alpha', 'Use this folder', 'Cancel', 'Up one folder']) {
        const box = await picker.getByRole('button', { name }).boundingBox();
        expect(box?.height ?? 0, name).toBeGreaterThanOrEqual(44);
      }
      const go = await page.getByRole('button', { name: 'Open my videos' }).boundingBox();
      expect(go?.height ?? 0).toBeGreaterThanOrEqual(44);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(overflow, 'nothing wider than the screen').toBeLessThanOrEqual(0);
      if (SHOTS) {
        await page.screenshot({ path: join(SHOTS, `setup-webkit-${device.toLowerCase().replace(/\s+/g, '')}.png`),
          fullPage: true });
      }
    } finally {
      await context.close();
    }
  });
}
