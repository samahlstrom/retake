// A video's ⋯ (spec/home.md, "A video's ⋯"; spec/notes.md, "The video's ⋯"; spec/team.md, "Sharing a video from the
// page"): a folder of two plain videos (alpha, bravo) served by `retake open <folder>` with its own config: two fake
// agents that never edit (`true`), a stand-in for cloudflared that prints a quick tunnel's address and waits (no
// tunnel is ever started), a scratch XDG_DATA_HOME so Move to Trash moves into a scratch trash (never the real one),
// and RETAKE_REVEAL so Show in folder runs a script that writes down the folder (never a file manager). Shared by
// 24-menus.spec.ts (Chromium, computer and phone) and 24-menus-webkit.spec.ts (WebKit, phone).
import { expect, type Locator, type Page } from '@playwright/test';
import { existsSync, linkSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { mediaLayout } from './fixtures.ts';
import { AUTHOR, REPO, WORK } from './env.ts';
import { startServer, type Server } from './server.ts';

/** The quick tunnel's address the stand-in prints. */
export const TUNNEL_HOST = 'menus-words-here.trycloudflare.com';

export interface MenusFixture {
  server: Server;
  dir: string;
  /** The folder of videos. */
  videos: string;
  /** The scratch XDG_DATA_HOME: the trash is its Trash/. */
  data: string;
  /** Where the RETAKE_REVEAL stand-in writes the folder it was asked to show. */
  revealed: string;
}

export async function startMenus(name: string, port: number): Promise<MenusFixture> {
  const dir = join(WORK, 'menus', name);
  rmSync(dir, { recursive: true, force: true });
  const videos = join(dir, 'Videos');
  const config = join(dir, 'config');
  const data = join(dir, 'data');
  const bin = join(dir, 'bin');
  for (const d of [join(videos, 'alpha', 'v1'), join(videos, 'bravo', 'v1'), join(config, 'retake'), data, bin]) {
    mkdirSync(d, { recursive: true });
  }
  const media = mediaLayout(WORK);
  linkSync(media.v5, join(videos, 'alpha', 'v1', 'alpha_v1.mp4'));
  linkSync(media.v6, join(videos, 'bravo', 'v1', 'bravo_v1.mp4'));
  const cloudflared = join(bin, 'cloudflared');
  writeFileSync(
    cloudflared,
    `#!/bin/sh\necho "INF |  https://${TUNNEL_HOST}  |" >&2\nexec sleep 600\n`,
    { mode: 0o755 },
  );
  const revealed = join(dir, 'revealed');
  const reveal = join(bin, 'reveal');
  writeFileSync(reveal, `#!/bin/sh\nprintf '%s' "$1" > "${revealed}"\n`, { mode: 0o755 });
  writeFileSync(join(config, 'retake', 'config.toml'), `name = "${AUTHOR}"
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

[share]
cloudflared = "${cloudflared}"
`);
  const server = await startServer(`menus-${name}`, videos, port, [], {
    env: { XDG_CONFIG_HOME: config, XDG_DATA_HOME: data, RETAKE_REVEAL: reveal },
  });
  return { server, dir, videos, data, revealed };
}

/** Where screenshots go: .retake-work/menus-shots/<what>-<device>.png. */
export const SHOTS = process.env.RETAKE_E2E_SHOTS ?? join(REPO, '.retake-work', 'menus-shots');

export async function shot(page: Page, what: string, device: string): Promise<void> {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `${what}-${device}.png`) });
}

export async function openHome(page: Page, f: MenusFixture): Promise<void> {
  await page.goto(f.server.url);
  await page.waitForFunction(() => window.__home?.loaded, null, { timeout: 20_000, polling: 80 });
}

export const card = (page: Page, name: string): Locator => page.locator(`[data-part~="video-card"]:has(a[data-name="${name}"])`);
export const cardMenu = (page: Page, name: string): Locator => card(page, name).locator('[data-part~="video-menu"]');
export const menu = (page: Page): Locator => page.locator('.vm-menu');

/** GET <server>p/<name>/api/automation as the owner. */
export async function automation(f: MenusFixture, name: string): Promise<{
  video_agent?: string;
  config: { agent: string };
  tasks: { agent: string; notes: number[] }[];
}> {
  const r = await fetch(`${f.server.url}p/${name}/api/automation`);
  return (await r.json()) as never;
}

/** The scratch trash's folders. */
export const inTrash = (f: MenusFixture, name: string): boolean => existsSync(join(f.data, 'Trash', 'files', name));
export const revealedFolder = (f: MenusFixture): string =>
  existsSync(f.revealed) ? readFileSync(f.revealed, 'utf8') : '';

/** Share… on a card: a link for `who` that can comment, from a tunnel the server starts. Returns the link. */
export async function shareFromCard(page: Page, name: string, who: string, device: string): Promise<string> {
  await cardMenu(page, name).click();
  await menu(page).getByRole('menuitem', { name: 'Share…' }).click();
  const sheet = page.locator('[data-part~="share-sheet"]');
  await expect(sheet).toBeVisible();
  await expect(sheet.getByRole('radio', { name: 'Can comment' })).toHaveAttribute('aria-checked', 'true');
  await expect(sheet.getByRole('radio', { name: '7 days' })).toHaveAttribute('aria-checked', 'true');
  await sheet.getByLabel('Who it’s for').fill(who);
  await sheet.getByRole('button', { name: 'Create link' }).click();
  const field = sheet.getByLabel('The link');
  await expect(field).toHaveValue(new RegExp(`^https://${TUNNEL_HOST.replaceAll('.', '\\.')}/s/rt_[A-Za-z0-9_-]{43}$`), {
    timeout: 20_000,
  });
  await expect(sheet.locator('.vm-links li')).toHaveCount(1);
  await expect(sheet.locator('.vm-links li').first().locator('.vm-who')).toHaveText(who);
  await expect(sheet.locator('.vm-links li').first().locator('.vm-detail')).toHaveText(/^Can comment · until \w{3} \d+$/);
  await shot(page, 'share-sheet', device);
  return await field.inputValue();
}

/** Move to Trash… on a card, its confirm, and Undo. */
export async function trashAndUndo(page: Page, f: MenusFixture, name: string, device: string): Promise<void> {
  await cardMenu(page, name).click();
  await menu(page).getByRole('menuitem', { name: 'Move to Trash…' }).click();
  const confirm = page.locator('[data-part~="trash-confirm"]');
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText(`Move ${name} to the Trash?`);
  await expect(confirm.getByTestId('trash-path')).toHaveText(new RegExp(`Videos/${name}$`));
  await expect(confirm.getByTestId('trash-sum')).toHaveText(/^1 version · [\d.]+ (KB|MB)$/);
  await expect(confirm.getByRole('button', { name: 'Cancel' })).toBeFocused();
  await shot(page, 'trash-confirm', device);
  await confirm.getByRole('button', { name: 'Move to Trash' }).click();
  await expect(confirm).toBeHidden();
  await expect(card(page, name)).toHaveCount(0);
  expect(inTrash(f, name)).toBe(true);
  expect(existsSync(join(f.videos, name))).toBe(false);
  const undo = page.locator('[data-part~="undo-toast"]');
  await expect(undo).toContainText('Moved to Trash');
  await shot(page, 'undo', device);
  await undo.getByRole('button', { name: 'Undo' }).click();
  await expect(card(page, name)).toHaveCount(1);
  expect(existsSync(join(f.videos, name, 'v1', `${name}_v1.mp4`))).toBe(true);
  expect(inTrash(f, name)).toBe(false);
}

/** Agent for this video ▸ on a card: the default is checked through "Use my default", then Agent Two for this video. */
export async function pickAgentOnCard(page: Page, f: MenusFixture, name: string, device: string): Promise<void> {
  await expect.poll(async () => (await automation(f, name)).config.agent, { timeout: 20_000 }).toBe('one');
  await cardMenu(page, name).click();
  await menu(page).getByRole('menuitem', { name: 'Agent for this video' }).click();
  const list = page.locator('.vm-agents');
  await expect(list.getByRole('menuitem', { name: /Use my default \(Agent One\)/ })).toContainText('✓');
  await expect(list.getByRole('menuitem', { name: 'Agent Two' })).not.toContainText('✓');
  await shot(page, 'agent-menu', device);
  await list.getByRole('menuitem', { name: 'Agent Two' }).click();
  await expect.poll(async () => (await automation(f, name)).video_agent ?? '').toBe('two');
  expect((await automation(f, name)).config.agent).toBe('one'); // the default is untouched
}
