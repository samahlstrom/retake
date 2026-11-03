// The Download entry on a phone, in WebKit (iPhone Safari's engine; the `webkit` project, lib/webkit.ts), on the
// iPhone 13 and iPhone 15 viewports: the version list (#verBtn) ends with `Download v2 (MP4)`, a real link to the file
// with `download` (an attachment, so Safari hands it to its own download sheet; never a blob), big enough to tap and
// inside the screen. Tapping it gets the file ready and shows how far; once ready it says the size. Screenshots go to
// .retake-work/download-shots/webkit-menu-<device>.png. Under Chromium the file skips.
import { devices, expect, test, type Browser } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { downloadFamily } from '../lib/download.ts';
import { LEGACY, PORTS, REPO, WORK } from '../lib/env.ts';
import { READY } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no downloads');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

const SHOTS = join(REPO, '.retake-work', 'download-shots');
const PHONES = ['iPhone 13', 'iPhone 15'] as const;
let server: Server;

test.beforeAll(async () => {
  server = await startServer('download-webkit', downloadFamily(join(WORK, 'download-webkit')), PORTS.mobile, [],
    { env: { RETAKE_ENCODER: 'x264' } });
});
test.afterAll(async () => { await server?.stop(); });

async function phone(browser: Browser, device: (typeof PHONES)[number]) {
  const { defaultBrowserType: _, ...d } = devices[device];
  const context = await browser.newContext(d);
  return { context, page: await context.newPage() };
}

for (const device of PHONES) {
  test(`the version list's Download entry on ${device}`, async ({ browser }) => {
    const { context, page } = await phone(browser, device);
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    try {
      await page.goto(server.url);
      await page.waitForFunction(READY);
      await expect(page.locator('html')).toHaveAttribute('data-layout', 'portrait');
      await page.locator('#verBtn').tap();
      const entry = page.locator('#verList [data-part="download"]');
      await expect(entry).toBeVisible();
      await expect(entry).toHaveText(/^Download v2 \(MP4\)/);
      await expect(entry).toHaveAttribute('href', 'video/v2/download');
      expect(await entry.evaluate((a) => a.tagName === 'A' && a.hasAttribute('download'))).toBe(true);
      const box = (await entry.boundingBox())!;
      const width = page.viewportSize()!.width;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(width + 0.5);
      expect(box.height).toBeGreaterThanOrEqual(44);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

      // Tapping it gets the file ready: progress at its right, then its size.
      await entry.tap();
      await expect(entry.locator('.d')).toHaveText(/Checking…|Preparing…|\d+%|\d+ (KB|MB)/);
      await expect(entry.locator('.d')).toHaveText(/\d+ (KB|MB)/, { timeout: 120_000 });
      // The file the link names is an attachment with its name and length, as Safari's download sheet needs.
      const head = await page.request.head(`${server.url}video/v2/download`);
      expect(head.status()).toBe(200);
      expect(head.headers()['content-disposition']).toContain('attachment; filename="talk_v2.mp4"');
      expect(Number(head.headers()['content-length'])).toBeGreaterThan(0);
      if (!(await entry.isVisible())) await page.locator('#verBtn').tap();
      mkdirSync(SHOTS, { recursive: true });
      await page.waitForTimeout(300);
      await page.screenshot({ path: join(SHOTS, `webkit-menu-${device.toLowerCase().replace(/\s+/g, '')}.png`) });
      expect(errors).toEqual([]);
    } finally {
      await context.close();
    }
  });
}
