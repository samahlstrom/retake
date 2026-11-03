// New (spec/setup.md): the first run. `retake start` with a scratch home, config and state, and a PATH with fake
// agents only (a `claude` that answers the test, a `codex` that isn't signed in; no real agent is ever run). The setup
// page shows the suggested folder and its videos, tests the agents, changes the folder with the folder list, and Open
// my videos lands on the folder's home at the same address; Settings shows the folder, tests the agent and switches to
// another folder. With no agent the page shows the makers' install lines, and Check again finds one that the login
// shell's PATH (a fake $SHELL) names. On a phone it fits the screen.
import { expect, test, type Browser, type Page } from '@playwright/test';
import { chmodSync, copyFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { LEGACY, PORTS, WORK } from '../lib/env.ts';
import { startRetake, type Server } from '../lib/server.ts';
import '../lib/home-window.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no setup');

const ROOT = join(WORK, 'setup');
const SHOTS = process.env.RETAKE_E2E_SHOTS;
const pageErrors: string[] = [];
const SYSTEM_PATH = '/usr/bin:/bin'; // ffmpeg and ffprobe; never a real agent (those live under the home folder)

/** Writes an executable script. */
function script(path: string, body: string): void {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `#!/bin/sh\n${body}`);
  chmodSync(path, 0o755);
}

/** A fake Claude Code: its test answers like `claude -p --output-format json`. */
const CLAUDE = `echo '{"type":"result","is_error":false,"result":"ready"}'\n`;
/** A fake Codex that isn't signed in. */
const CODEX = `echo 'Error: Not logged in. Run codex login.' >&2; exit 1\n`;

/** A scratch world: a home with videos in ~/Videos and ~/Movies, and its own config and state folders. */
function world(name: string): { home: string; config: string; env: Record<string, string> } {
  const home = join(ROOT, name, 'home');
  const m = mediaLayout(WORK);
  for (const rel of ['Videos/alpha/v1/alpha_v1.mp4', 'Movies/bravo/bravo_v1.mp4']) {
    mkdirSync(dirname(join(home, rel)), { recursive: true });
    copyFileSync(m.v5, join(home, rel));
  }
  const config = join(ROOT, name, 'config');
  mkdirSync(config, { recursive: true });
  return {
    home,
    config,
    env: { HOME: home, XDG_CONFIG_HOME: config, XDG_STATE_HOME: join(ROOT, name, 'state'), RETAKE_ENCODER: 'x264' },
  };
}

async function open(browser: Browser, url: string, phone = false): Promise<Page> {
  const context = phone
    ? await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 })
    : await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  await page.goto(url);
  await page.waitForFunction(() => window.__setup?.loaded, null, { timeout: 20_000, polling: 80 });
  return page;
}

test.describe('with agents', () => {
  let server: Server;
  let w: ReturnType<typeof world>;

  test.beforeAll(async () => {
    w = world('agents');
    const bin = join(ROOT, 'agents', 'bin');
    script(join(bin, 'claude'), CLAUDE);
    script(join(bin, 'codex'), CODEX);
    server = await startRetake('setup', ['start', '--no-open', '--port', String(PORTS.setup), '--idle-exit', '0'],
      PORTS.setup, { ...w.env, PATH: `${bin}:${SYSTEM_PATH}` });
  });
  test.afterAll(async () => {
    await server?.stop();
  });

  test('the first run: folder, agent test, where it works, then the videos at the same address', async ({ browser }) => {
    const page = await open(browser, server.url);
    try {
      const setup = page.locator('[data-part~="setup"]');
      await expect(setup.locator('h1')).toHaveText('Set up Retake');
      await expect(page.getByTestId('setup-folder')).toHaveText('~/Videos');
      await expect(setup).toContainText('1 video');

      // Both agents are offered; Test says which works.
      const agents = page.getByRole('radiogroup', { name: 'Your agent' });
      await expect(agents.getByRole('radio')).toHaveCount(2);
      const claude = agents.locator('.choice', { hasText: 'Claude Code' });
      await claude.getByRole('button', { name: 'Test' }).click();
      await expect(claude).toContainText(/Works · (<1|\d+) s/);
      const codex = agents.locator('.choice', { hasText: 'Codex' });
      await codex.getByRole('button', { name: 'Test' }).click();
      await expect(codex).toContainText('Not signed in: run codex once in Terminal and sign in.');
      await claude.getByRole('radio').check();

      // Another folder, from the folder list: up to the home, into Movies.
      await setup.getByRole('button', { name: 'Change' }).click();
      const picker = page.locator('[data-part~="folder-picker"]');
      await expect(picker.locator('.path')).toHaveText('~/Videos');
      await expect(picker.getByRole('button', { name: 'alpha' })).toBeVisible();
      await picker.getByRole('button', { name: 'Up one folder' }).click();
      await expect(picker.locator('.path')).toHaveText('~');
      await picker.getByRole('button', { name: 'Movies' }).click();
      await expect(picker.locator('.path')).toHaveText('~/Movies');
      await picker.getByRole('button', { name: 'Use this folder' }).click();
      await expect(picker).toHaveCount(0);
      await expect(page.getByTestId('setup-folder')).toHaveText('~/Movies');

      const places = page.getByRole('radiogroup', { name: 'Where agents work' });
      await places.getByRole('radio', { name: /In the background/ }).check();
      if (SHOTS) await page.screenshot({ path: join(SHOTS, 'setup-desktop.png'), fullPage: true });

      await page.getByRole('button', { name: 'Open my videos' }).click();
      await page.waitForFunction(() => window.__home?.loaded, null, { timeout: 30_000, polling: 100 });
      expect(new URL(page.url()).origin).toBe(new URL(server.url).origin);
      const names = await page.evaluate(() => window.__home.projects.map((p) => p.name));
      expect(names).toEqual(['bravo']);
      const config = readFileSync(join(w.config, 'retake', 'config.toml'), 'utf8');
      expect(config).toContain('folder = "~/Movies"');
      expect(config).toContain('default_agent = "claude"');
      expect(config).not.toContain('open_agents_in');
      expect(pageErrors).toEqual([]);
    } finally {
      await page.context().close();
    }
  });

  test('Settings: the videos folder, Test, and switching to another folder', async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();
    page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
    try {
      await page.goto(`${server.url}#settings`);
      await expect(page.getByTestId('settings-folder')).toHaveText('~/Movies');
      const agentRow = page.locator('[data-part~="settings-row"]', { hasText: 'Default agent' });
      await agentRow.getByRole('button', { name: 'Test' }).click();
      await expect(agentRow).toContainText(/Works · (<1|\d+) s/);
      if (SHOTS) await page.screenshot({ path: join(SHOTS, 'setup-settings.png') });

      const videos = page.locator('[data-part~="settings-section"]', { hasText: 'Your videos' });
      await videos.getByRole('button', { name: 'Change' }).click();
      const picker = page.locator('[data-part~="folder-picker"]');
      await picker.getByRole('button', { name: 'Up one folder' }).click();
      await picker.getByRole('button', { name: 'Videos' }).click();
      await expect(picker.locator('.path')).toHaveText('~/Videos');
      await picker.getByRole('button', { name: 'Use this folder' }).click();
      // The home switches to it, at the same address.
      await page.waitForFunction(() => window.__home?.loaded && window.__home.projects.some((p) => p.name === 'alpha'),
        null, { timeout: 30_000, polling: 100 });
      expect(readFileSync(join(w.config, 'retake', 'config.toml'), 'utf8')).toContain('folder = "~/Videos"');
      expect(pageErrors).toEqual([]);
    } finally {
      await context.close();
    }
  });
});

test.describe('with no agent yet', () => {
  let server: Server;
  let later: string;

  test.beforeAll(async () => {
    const w = world('none');
    later = join(ROOT, 'none', 'later-bin');
    mkdirSync(later, { recursive: true });
    // The login shell's PATH names a folder where an agent turns up later (installed while the page is open).
    const shell = join(ROOT, 'none', 'fakeshell');
    script(shell, `PATH='${later}:${SYSTEM_PATH}'\neval "$2"\n`);
    server = await startRetake('setup-none', ['start', '--no-open', '--port', String(PORTS.setup), '--idle-exit', '0'],
      PORTS.setup, { ...w.env, PATH: SYSTEM_PATH, SHELL: shell });
  });
  test.afterAll(async () => {
    await server?.stop();
  });

  test('shows the install lines, finds the agent with Check again, and fits a phone', async ({ browser }) => {
    const page = await open(browser, server.url, true);
    try {
      const setup = page.locator('[data-part~="setup"]');
      await expect(setup).toContainText('An agent is the AI that edits your videos');
      await expect(setup.locator('.cmd code').first()).toHaveText('curl -fsSL https://claude.ai/install.sh | bash');
      await expect(setup.getByRole('button', { name: 'Copy the Claude Code install line' })).toBeVisible();
      // Nothing scrolls sideways, and the buttons are big enough for a thumb.
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      const go = await page.getByRole('button', { name: 'Open my videos' }).boundingBox();
      expect(go?.height ?? 0).toBeGreaterThanOrEqual(44);
      if (SHOTS) await page.screenshot({ path: join(SHOTS, 'setup-phone-no-agent.png'), fullPage: true });

      script(join(later, 'claude'), CLAUDE);
      await setup.getByRole('button', { name: 'Check again' }).click();
      const agents = page.getByRole('radiogroup', { name: 'Your agent' });
      await expect(agents.getByRole('radio', { name: 'Claude Code' })).toBeChecked();
      if (SHOTS) await page.screenshot({ path: join(SHOTS, 'setup-phone.png'), fullPage: true });
      expect(pageErrors).toEqual([]);
    } finally {
      await page.context().close();
    }
  });
});
