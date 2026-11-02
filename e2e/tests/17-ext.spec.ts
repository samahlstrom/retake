// Extensions (spec/extensions.md, docs/extensions.md): the hello extension of docs/extensions.md, turned on for a run
// with `retake open <folder> --ext DIR`. Its nav section opens its page, which lists the videos; its tab next to the
// comments follows the playhead; its CSS is in its own layer with the theme's tokens. A broken copy shows one quiet
// line in its own slot while review keeps working (R still records). `retake ext check` passes the hello extension
// and flags a broken one.
import { expect, test, type Page } from '@playwright/test';
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { copyFileSync } from 'node:fs';
import { mediaLayout } from '../lib/fixtures.ts';
import { CONFIG_HOME, LEGACY, PORTS, REPO, RETAKE_BIN, WORK } from '../lib/env.ts';
import { READY } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no extensions');

const HELLO = join(REPO, 'internal', 'ext', 'testdata', 'hello');
const DIR = join(WORK, 'ext-folder', 'shows');
const BROKEN = join(WORK, 'ext-broken');
let server: Server;
const pageErrors: string[] = [];

test.beforeAll(async () => {
  const m = mediaLayout(WORK);
  for (const [src, rel] of [[m.v5, 'alpha/v1/alpha_v1.mp4'], [m.v6, 'alpha/v2/alpha_v2.mp4']] as const) {
    mkdirSync(dirname(join(DIR, rel)), { recursive: true });
    copyFileSync(src, join(DIR, rel));
  }
  // A broken copy: its tab's mount throws.
  cpSync(HELLO, BROKEN, { recursive: true });
  writeFileSync(join(BROKEN, 'extension.toml'), `api = 1
id = "broken"
name = "Broken"
version = "0.1.0"

[ui]
module = "ui/hello.js"

[[slots]]
slot = "sidebar-tab"
title = "Broken"
`);
  writeFileSync(join(BROKEN, 'ui', 'hello.js'), "export function sidebarTab() { throw new Error('boom'); }\n");
  server = await startServer('ext', DIR, PORTS.ext, ['--ext', HELLO, '--ext', BROKEN]);
});

test.afterAll(async () => {
  await server?.stop();
  expect(pageErrors).toEqual([]);
});

async function open(page: Page, url: string): Promise<void> {
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  await page.goto(url);
}

test('retake ext check passes the hello extension and flags a broken one', () => {
  const ok = spawnSync(RETAKE_BIN, ['ext', 'check', HELLO], { env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME } });
  expect(ok.status, ok.stderr.toString()).toBe(0);
  expect(ok.stdout.toString()).toContain('ext ok: hello 0.1.0 (api 1)');
  // The check's broken fixture (a CSS rule out of scope, an unknown token, a missing export, ...): exit 1.
  const bad = spawnSync(RETAKE_BIN, ['ext', 'check', join(REPO, 'internal', 'ext', 'testdata', 'broken')],
    { env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME } });
  expect(bad.status).toBe(1);
  expect(bad.stdout.toString() + bad.stderr.toString()).toMatch(/problem/);
  const list = execFileSync(RETAKE_BIN, ['ext', 'list'], { env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME } }).toString();
  expect(list).toContain('scripts'); // bundled, off until enabled
});

test('the nav section opens the page, which lists the videos in the theme\'s colours', async ({ page }) => {
  await open(page, server.url);
  const sec = page.locator('[data-part="nav-ext"][data-ext="hello"]');
  await expect(sec).toBeVisible();
  await sec.locator('[data-part="nav-ext-title"]').click();
  await expect(page).toHaveURL(/#ext\/hello\//);
  const slot = page.locator('[data-ext="hello"][data-slot="page"]');
  await expect(slot.locator('li')).toHaveText(['alpha · v2']);
  // The extension's rule, in its own layer, with a token: --text-3.
  const [color, token] = await slot.locator('li').first().evaluate((li) => {
    const probe = document.createElement('span');
    probe.style.color = 'var(--text-3)';
    document.body.append(probe);
    const want = getComputedStyle(probe).color;
    probe.remove();
    return [getComputedStyle(li).color, want];
  });
  expect(color).toBe(token);
});

test('its tab follows the playhead, and a broken one stays in its slot while R still records', async ({ page }) => {
  await open(page, `${server.url}p/alpha/`);
  await page.waitForFunction(READY);
  await page.getByRole('tab', { name: 'Hello' }).click();
  const p = page.locator('[data-ext="hello"][data-slot="sidebar-tab"] [data-part="hello-time"]');
  await expect(p).toHaveText(/^Hello from alpha v2, at 0\.0 s$/);
  await page.locator('video').evaluate((v: HTMLVideoElement) => { v.currentTime = 1.5; });
  await expect(p).toHaveText(/at 1\.5 s$/);

  await page.getByRole('tab', { name: 'Broken' }).click();
  await expect(page.locator('[data-ext="broken"][data-slot="sidebar-tab"]')).toContainText("Broken didn't load: boom");
  // Review keys still reach the page: R records (focus on the tab, not a text field), R stops.
  await page.keyboard.press('r');
  await page.waitForFunction(() => window.__review.take.id !== null, null, { timeout: 10_000 });
  await page.waitForTimeout(800);
  await page.keyboard.press('r');
  // Recording on a video returns the comments column to To do.
  await expect(page.getByRole('tab', { name: /To do/ })).toHaveAttribute('aria-selected', 'true');
});
