// New (spec/home.md, "Settings"): the owner's settings, at the bottom of the home's folder column. `retake open
// <folder>` with its own config folder (a scratch XDG_CONFIG_HOME): two fake agents that never run, no agent found on
// PATH. The page changes the default agent, its fallback, where agents run and an extension, and config.toml says
// so; the default agent reaches the projects' editing config too. On a phone the page fits the screen.
import { expect, test, type Browser, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { AUTHOR, LEGACY, PORTS, WORK } from '../lib/env.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no settings');

const DIR = join(WORK, 'settings-folder');
const CONFIG = join(WORK, 'settings-config');
const FILE = join(CONFIG, 'retake', 'config.toml');
/** `--ui-dir` for a page built somewhere else than the binary's (RETAKE_E2E_UI_DIR), else the binary's own. */
const UI = process.env.RETAKE_E2E_UI_DIR ? ['--ui-dir', process.env.RETAKE_E2E_UI_DIR] : [];
let server: Server;
const pageErrors: string[] = [];

test.beforeAll(async () => {
  const m = mediaLayout(WORK);
  for (const rel of ['alpha/v1/alpha_v1.mp4', 'bravo/bravo_v1.mp4']) {
    mkdirSync(dirname(join(DIR, rel)), { recursive: true });
    copyFileSync(m.v5, join(DIR, rel));
  }
  mkdirSync(dirname(FILE), { recursive: true });
  // Two agents that are never run (nothing is sent here); `true` stands in for their programs.
  writeFileSync(FILE, `# the e2e's own config
name = "${AUTHOR}"
open_browser = false
find_agents = false

[[agents]]
id = "one"
name = "Agent One"
command = ["true"]

[[agents]]
id = "two"
name = "Agent Two"
command = ["true"]
`);
  server = await startServer('settings', DIR, PORTS.home, [...UI], { env: { XDG_CONFIG_HOME: CONFIG } });
});

test.afterAll(async () => {
  await server?.stop();
});

async function open(browser: Browser, phone = false): Promise<Page> {
  const context = phone
    ? await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true,
      deviceScaleFactor: 2 })
    : await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  await page.goto(server.url);
  await page.waitForFunction(() => window.__home?.loaded, null, { timeout: 20_000, polling: 80 });
  return page;
}

/** Picks an option of one of the page's dropdowns (Bits UI's Select). */
async function choose(page: Page, label: string, option: string): Promise<void> {
  await page.getByRole('combobox', { name: label }).click();
  await page.getByRole('option', { name: option, exact: true }).click();
  await expect(page.getByRole('combobox', { name: label })).toContainText(option);
}

const config = (): string => readFileSync(FILE, 'utf8');

/** A project's editing config (GET api/automation's `config`). */
async function automation(project: string): Promise<{ worker?: string; agent?: string; fallback?: string }> {
  const r = await fetch(`${server.url}p/${project}/api/automation`);
  return r.ok ? ((await r.json()) as { config: { agent: string } }).config : {};
}

test('the owner changes the agent, where it runs and an extension, and config.toml keeps them', async ({ browser }) => {
  const page = await open(browser);
  try {
    // The projects' editing workers (in this process) start with the first agent.
    for (const p of ['alpha', 'bravo']) await expect.poll(() => automation(p), { timeout: 20_000 }).toMatchObject({ agent: 'one' });
    const link = page.locator('[data-part~="nav-settings"]');
    await expect(link).toBeVisible();
    await link.click();
    await expect(page).toHaveURL(/#settings$/);
    await expect(page.locator('[data-part~="settings"] h1')).toHaveText('Settings');
    await expect(page.locator('[data-part~="settings"] .sub')).toContainText('config.toml');
    await expect(page.getByRole('combobox', { name: 'Default agent' })).toContainText('Agent One');

    await choose(page, 'Default agent', 'Agent Two');
    await expect(page.locator('.saved')).toHaveText('Saved');
    await choose(page, 'If its usage runs out', 'Switch to Agent One');
    await choose(page, 'Where agents run', 'In tmux');
    await expect.poll(config).toContain('default_agent = "two"');
    const text = config();
    expect(text).toContain('fallback_agent = "one"');
    expect(text).toContain('open_agents_in = "tmux"');
    expect(text.startsWith('# the e2e\'s own config\n')).toBe(true); // the rest of the file stays as it was
    expect(text).toContain('[[agents]]\nid = "one"');

    // Every project edits with it now.
    for (const p of ['alpha', 'bravo']) expect(await automation(p), p).toMatchObject({ agent: 'two', fallback: 'one' });
    if (process.env.RETAKE_E2E_SHOTS) await page.screenshot({ path: join(process.env.RETAKE_E2E_SHOTS, 'settings-desktop.png') });

    // An extension: on in config.toml, and on for real after a restart.
    const scripts = page.locator('[data-part~="settings-row"][data-ext="scripts"]');
    await expect(scripts).toBeVisible();
    const sw = scripts.getByRole('switch');
    await expect(sw).toHaveAttribute('aria-checked', 'false');
    await sw.click();
    await expect(sw).toHaveAttribute('aria-checked', 'true');
    await expect(scripts).toContainText('Restart retake to turn it on.');
    await expect.poll(config).toContain('enabled = ["scripts"]');
    await sw.click();
    await expect(sw).toHaveAttribute('aria-checked', 'false');
    await expect.poll(config).toContain('enabled = []');

    // The theme folder is said, with how to make one.
    await expect(page.locator('[data-part~="settings-section"]', { hasText: 'Theme' })).toContainText('retake theme init');

    // Back in the home: the cards again, and a reload keeps the settings.
    await page.locator('[data-part~="nav-all"]').click();
    await expect(page.locator('[data-part~="settings"]')).toHaveCount(0);
    await page.goto(`${server.url}#settings`);
    await expect(page.getByRole('combobox', { name: 'Default agent' })).toContainText('Agent Two');
    await expect(page.getByRole('combobox', { name: 'Where agents run' })).toContainText('In tmux');
    expect(pageErrors).toEqual([]);
  } finally {
    await page.context().close();
  }
});

test('a refused change says why and writes nothing', async () => {
  const before = config();
  const r = await fetch(`${server.url}api/settings`, { method: 'POST', body: '{"open_in": "screen"}' });
  expect(r.status).toBe(400);
  expect((await r.json() as { error: string }).error).toMatch(/background, herdr or tmux/);
  const other = await fetch(`${server.url}api/settings`, { method: 'POST', body: '{"agent": "one"}',
    headers: { Origin: 'http://evil.example' } });
  expect(other.status).toBe(403);
  expect(config()).toBe(before);
});

test('on a phone the settings fit the screen, from the drawer', async ({ browser }) => {
  const page = await open(browser, true);
  try {
    await page.locator('[data-part~="nav-toggle"]').tap();
    const link = page.locator('[data-part~="nav-settings"]');
    await expect(link).toBeVisible();
    const box = (await link.boundingBox())!;
    expect(box.height).toBeGreaterThanOrEqual(40);
    await link.tap();
    await expect(page.locator('[data-part~="settings"] h1')).toHaveText('Settings');
    await expect(page.locator('[data-part~="nav"]')).toBeHidden(); // the drawer closed
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    for (const label of ['Default agent', 'If its usage runs out', 'Where agents run']) {
      const b = (await page.getByRole('combobox', { name: label }).boundingBox())!;
      expect(b.x + b.width, label).toBeLessThanOrEqual(390);
      expect(b.height, label).toBeGreaterThanOrEqual(40);
    }
    const sw = (await page.locator('[data-part~="settings-switch"]').first().boundingBox())!;
    expect(sw.x + sw.width).toBeLessThanOrEqual(390);
    if (process.env.RETAKE_E2E_SHOTS) await page.screenshot({ path: join(process.env.RETAKE_E2E_SHOTS, 'settings-phone.png'), fullPage: true });
    expect(pageErrors).toEqual([]);
  } finally {
    await page.context().close();
  }
});
