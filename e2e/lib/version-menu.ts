// The one version menu and the home's cards while a version is live (spec/notes.md, "The version menu"; spec/home.md,
// "A version's name, never draft" and "Every card has a picture"). A home folder with a video `promo` (v1, v2 and its
// script v0), two ideas (a script with words, a script with an image) and another video; Scripts is on and a fake
// agent waits at its gates, so Send on v2 makes v3 live and keeps it live. Shared by 24-version-menu.spec.ts
// (Chromium: a computer and an iPhone 15) and 24-version-menu-webkit.spec.ts (WebKit, iPhone 15).
import { expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { linkSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { mediaLayout } from './fixtures.ts';
import { AUTHOR, REPO, RETAKE_BIN, WORK } from './env.ts';
import { READY } from './review.ts';
import { startServer, type Server } from './server.ts';

/** The fake agent: `retake working` on each note, then waits at its gate; the gates let it finish. */
const AGENT = `import { copyFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
const ctx = JSON.parse(readFileSync(process.env.RETAKE_CONTEXT, 'utf8'));
const retake = (...args) => execFileSync(process.env.RETAKE_BIN, args, { stdio: ['ignore', 'pipe', 'inherit'] });
const gate = async (name) => {
  const until = Date.now() + 300000;
  while (!existsSync(process.env.GATES + '/' + name) && Date.now() < until) await new Promise((r) => setTimeout(r, 100));
};
const notes = ctx.notes.filter((n) => ctx.task.notes.includes(n.id)).sort((a, b) => a.t0 - b.t0);
for (const [i, n] of notes.entries()) {
  retake('working', String(n.id), 'on it');
  await gate('work-' + (i + 1));
  copyFileSync(process.env.CUT, process.env.RETAKE_OUTPUT);
  retake('done', String(n.id), 'Changed it');
}
await gate('report');
writeFileSync(process.env.RETAKE_REPORT, JSON.stringify({ state: 'completed', message: 'Edited' }));
`;

/** A 4×4 PNG: an idea's first image. */
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAIAAAAmkwkpAAAAEElEQVR4nGMwDl0FRwzEcQDqExMhPff8RAAAAABJRU5ErkJggg==',
  'base64',
);

export const IDEA = ['# Non-meta promo', '', 'A promo that never shows the app reviewing itself.', '', '## Cold open'];

export interface MenuFixture {
  server: Server;
  gates: string;
}

/** The home folder, its config (Scripts on, the fake agent) and a server on `port`. */
export async function startMenuHome(name: string, port: number): Promise<MenuFixture> {
  const dir = join(WORK, 'version-menu', name);
  rmSync(dir, { recursive: true, force: true });
  const home = join(dir, 'home');
  const config = join(dir, 'config');
  const gates = join(dir, 'gates');
  const m = mediaLayout(WORK);
  const promo = join(home, 'retake', 'promo');
  for (const d of ['v0', 'v1', 'v2']) mkdirSync(join(promo, d), { recursive: true });
  for (const d of [join(home, 'retake', 'idea', 'v0'), join(home, 'retake', 'board', 'v0', 'images'),
    join(home, 'chess', 'explainer', 'v1'), join(config, 'retake'), gates]) mkdirSync(d, { recursive: true });
  linkSync(m.v5, join(promo, 'v1', 'promo_v1.mp4'));
  linkSync(m.v6, join(promo, 'v2', 'promo_v2.mp4'));
  linkSync(m.v6, join(home, 'chess', 'explainer', 'v1', 'explainer_v1.mp4'));
  writeFileSync(join(promo, 'v0', 'promo_v0.md'), '# Promo\n\nA minute on what Retake does.\n');
  writeFileSync(join(home, 'retake', 'idea', 'v0', 'idea_v0.md'), `${IDEA.join('\n')}\n`);
  writeFileSync(join(home, 'retake', 'board', 'v0', 'board_v0.md'), '# Storyboard\n\n![The first shot](images/frame.png)\n');
  writeFileSync(join(home, 'retake', 'board', 'v0', 'images', 'frame.png'), PNG);
  writeFileSync(join(dir, 'fake-agent.mjs'), AGENT);
  writeFileSync(join(config, 'retake', 'config.toml'), `name = "${AUTHOR}"
open_browser = false
find_agents = false

[[agents]]
id = "fake"
name = "Fake agent"
command = ["node", "${join(dir, 'fake-agent.mjs')}"]
env = { RETAKE_BIN = "${RETAKE_BIN}", GATES = "${gates}", CUT = "${m.v5}" }
`);
  execFileSync(RETAKE_BIN, ['ext', 'enable', 'scripts'], { env: { ...process.env, XDG_CONFIG_HOME: config } });
  const server = await startServer(`version-menu-${name}`, home, port, [], {
    env: { XDG_CONFIG_HOME: config, RETAKE_ENCODER: 'x264' },
  });
  return { server, gates };
}

/** Lets the fake agent finish, so stopping the server leaves nothing behind. */
export function release(f: MenuFixture): void {
  for (const g of ['work-1', 'work-2', 'work-3', 'report']) writeFileSync(join(f.gates, g), '');
}

export const SHOTS = process.env.RETAKE_E2E_SHOTS ?? join(REPO, '.retake-work', 'version-menu-shots');
export const PROMO = 'p/retake-promo/';

const shown = (page: Page) => page.evaluate(() => window.__review.view.shown);

/** The reviewer explicitly chooses the live cut; Send keeps them on their source. */
async function chooseLive(page: Page): Promise<void> {
  await page.locator('[data-part~="version-picker"]:visible').first().click();
  await page.locator('[data-part~="version-list"] [data-version^="v3-draft"]').click();
  await expect.poll(() => shown(page), { timeout: 30_000 }).toMatch(/^v3-draft\d+$/);
}

/** Two comments on v1, three on v2, then Send on v2: v3 is live (the agent waits at its first gate). Once per
 * fixture; later calls only open the page. */
export async function goLive(page: Page, f: MenuFixture): Promise<void> {
  await page.goto(f.server.url + PROMO);
  await page.waitForFunction(READY, null, { timeout: 30_000 });
  if (/-draft\d+$/.test((await shown(page)) ?? '')) return;
  const automation = async () =>
    (await (await page.request.get(`${f.server.url}${PROMO}api/automation`)).json()) as { blocked?: string;
      tasks: { state: string; target?: string }[] };
  if ((await automation()).tasks.some((t) => t.state === 'running')) {
    await chooseLive(page);
    return;
  }
  await expect.poll(async () => (await automation()).blocked ?? '', { timeout: 20_000 }).toBe('');
  for (const [version, t, text] of [['v1', 2, 'Old note one'], ['v1', 5, 'Old note two'], ['v2', 1, 'The intro drags'],
    ['v2', 4, 'Fade the mask slower'], ['v2', 7, 'The bell is late']] as const) {
    const status = await page.evaluate(async ([version, t, text]) => (await fetch('api/notes', { method: 'POST',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ version, t, text }) })).status,
    [version, t, text] as const);
    expect(status).toBe(201);
  }
  await expect(page.locator('#side .item')).toHaveCount(3, { timeout: 15_000 });
  await page.locator('[data-part~="send"]:visible').first().click();
  await expect.poll(async () => (await automation()).tasks.map((t) => `${t.state}:${t.target}`), { timeout: 60_000 })
    .toEqual(['running:v3']);
  await expect.poll(() => shown(page)).toBe('v2');
  await chooseLive(page);
}

/** What the open menu lists: [name, its detail, live, on screen, row height]. */
async function rows(page: Page) {
  const list = page.locator('[data-part~="version-list"]');
  await expect(list).toBeVisible();
  return list.locator('[data-version]').evaluateAll((els) => els.map((e) => ({
    name: e.querySelector('.name')?.textContent?.trim() ?? '',
    detail: e.querySelector('.d')?.textContent?.trim() ?? '',
    live: !!e.querySelector('.live'),
    on: e.getAttribute('aria-current') === 'true',
    text: (e as HTMLElement).innerText,
    height: e.getBoundingClientRect().height,
    dot: (() => {
      const d = e.querySelector('.live')?.getBoundingClientRect();
      const n = e.querySelector('.name')!.getBoundingClientRect();
      return d ? { top: d.top, bottom: d.bottom, nameTop: n.top, nameBottom: n.bottom } : null;
    })(),
  })));
}

/** The rows while v3 is live, the same on every layout. */
async function liveRows(page: Page, where: string): Promise<void> {
  const r = await rows(page);
  expect(r.map((x) => x.name), where).toEqual(['v3', 'v2', 'v1', 'Script']);
  expect(r.map((x) => x.detail), where).toEqual(['0:40', '3 to do', '2 to do', '']);
  expect(r.filter((x) => x.live).map((x) => x.name), `${where}: the live dot`).toEqual(['v3']);
  expect(r.filter((x) => x.on).map((x) => x.name), `${where}: the one on screen`).toEqual(['v3']);
  for (const x of r) expect(x.text, `${where}: ${x.name}`).not.toMatch(/draft|Latest|→|·|✓/);
  // The live dot sits on the name's line (it never wraps onto a line of its own).
  const dot = r[0]!.dot!;
  expect(dot.top, `${where}: the dot is on the name's line`).toBeGreaterThanOrEqual(dot.nameTop - 1);
  expect(dot.bottom, `${where}: the dot is on the name's line`).toBeLessThanOrEqual(dot.nameBottom + 1);
  await expect(page.locator('[data-part~="version-list"] [role="separator"]')).toHaveCount(1); // Script set apart
}

/** Exactly one version menu on screen, where `where` says; none in the comments header. */
async function oneMenu(page: Page, inside: string, what: string): Promise<void> {
  await expect(page.locator('[data-part~="version-picker"]:visible'), `${what}: one version menu`).toHaveCount(1);
  await expect(page.locator(`${inside} [data-part~="version-picker"]`), `${what}: in ${inside}`).toBeVisible();
  await expect(page.locator('#side header [data-part~="version-picker"]'), `${what}: none in the comments`)
    .toHaveCount(0);
  await expect(page.locator('[data-part~="version-picker"]:visible')).toHaveText('v3');
  await expect(page.locator('[data-part~="version-picker"]:visible')).toHaveClass(/\blive\b/);
}

/** A computer: the bar's menu, the comments header in one row, the page's title. */
export async function desktopMenu(page: Page, f: MenuFixture): Promise<void> {
  await goLive(page, f);
  await page.reload(); // opened on the live v3 (its title is the opened version's)
  await page.waitForFunction(READY, null, { timeout: 30_000 });
  await expect.poll(() => shown(page), { timeout: 30_000 }).toMatch(/^v3-draft\d+$/);
  await oneMenu(page, '#bar', 'computer');
  await expect(page.locator('#side header [role="tab"]').first()).toHaveText('To do 3');
  await expect(page.locator('#sideToggle')).toHaveCount(0); // the column is open
  await expect(page).toHaveTitle('Review · promo_v3');
  await page.locator('#verBtn').click();
  await liveRows(page, 'computer');
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, 'menu-desktop.png') });
  await page.locator('[data-part~="version-list"] [data-version="v1"]').click();
  await expect.poll(() => shown(page)).toBe('v1');
  await expect(page.locator('#side .item')).toHaveCount(2);
  await page.locator('#verBtn').click();
  await page.locator('[data-part~="version-list"] [data-version="v3"]').click();
  await expect.poll(() => shown(page)).toMatch(/^v3-draft\d+$/);
}

/** A phone in portrait: the menu is in the controls row, its rows 44 px and inside the screen, no bubble; in landscape
 * it is in the bottom bar and the bubble opens the comments, whose header has none. */
export async function phoneMenu(page: Page, f: MenuFixture, device: string): Promise<void> {
  await goLive(page, f);
  expect(await page.evaluate(() => document.documentElement.dataset.layout)).toBe('portrait');
  await oneMenu(page, '#pbar', `${device} portrait`);
  await expect(page.locator('#sideToggle')).toHaveCount(0);
  await expect(page.locator('#side header svg')).toHaveCount(1); // search: no speech bubble
  const header = (await page.locator('#side header').boundingBox())!;
  expect(header.height, `${device}: the comments header is one row`).toBeLessThanOrEqual(56.5);
  await page.locator('#pbar [data-part~="version-picker"]').tap();
  await liveRows(page, `${device} portrait`);
  const width = page.viewportSize()!.width;
  const box = (await page.locator('[data-part~="version-list"]').boundingBox())!;
  expect(box.x, `${device}: the menu's left`).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width, `${device}: the menu's right`).toBeLessThanOrEqual(width);
  expect(box.width, `${device}: a roomy menu`).toBeGreaterThanOrEqual(239.5);
  for (const x of await rows(page)) expect(x.height, `${device}: ${x.name}'s row`).toBeGreaterThanOrEqual(44);
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `menu-${device}.png`) });
  await page.locator('[data-part~="version-list"] [data-version="v1"]').tap();
  await expect.poll(() => shown(page)).toBe('v1');
  await expect(page.locator('#side .item')).toHaveCount(2);
  await expect(page.locator('#pbar [data-part~="version-picker"]')).toHaveText('v1');

  // Landscape (Safari's iPhone 15 landscape viewport, under 760 px wide): the bottom bar's menu; the bubble opens the
  // comments over the video, as tall as it, whose header has no version.
  const { width: w, height: h } = page.viewportSize()!;
  await page.setViewportSize({ width: 734, height: 343 });
  await expect.poll(() => page.evaluate(() => document.documentElement.dataset.layout)).toBe('landscape');
  await expect(page.locator('[data-part~="version-picker"]:visible')).toHaveCount(1);
  await expect(page.locator('#pbar.landscape [data-part~="version-picker"]')).toBeVisible();
  await page.locator('#sideToggle').tap();
  await expect(page.locator('#side')).toBeVisible();
  await expect(page.locator('#side header [data-part~="version-picker"]')).toHaveCount(0);
  await expect(page.locator('#sideToggle .n')).toHaveCount(0); // To do says the count while they're open
  const over = (await page.locator('#side').boundingBox())!, stage = (await page.locator('#stage').boundingBox())!;
  expect(over.height, `${device}: the comments are as tall as the video`).toBeGreaterThanOrEqual(stage.height - 1);
  await expect(page.locator('#side .item').first()).toBeInViewport();
  await page.screenshot({ path: join(SHOTS, `landscape-${device}.png`) });
  await page.setViewportSize({ width: w, height: h });
}

/** The home: the live video reads v3 with its dot (never "draft"), every card has a picture, an idea its document. */
export async function homeCards(page: Page, f: MenuFixture, device: string): Promise<void> {
  await page.goto(`${f.server.url}#/retake`);
  await page.waitForFunction(() => window.__home?.loaded, null, { timeout: 20_000 });
  const promo = page.locator('a.card[data-name="retake-promo"]');
  await expect(promo.locator('.ver')).toHaveText('v3');
  await expect(promo.locator('.ver .live')).toHaveCount(1);
  await expect(page.locator('body')).not.toContainText('draft');
  // A frame for every video: the live revision's own poster (made through the relay), and the other video's.
  const card = (await page.evaluate(() => window.__home.projects.find((p) => p.name === 'retake-promo')))!;
  expect(card.version).toMatch(/^v3-draft\d+$/);
  const poster = await page.request.get(`${f.server.url}${PROMO}api/poster?v=${card.version}`);
  expect(poster.status()).toBe(200);
  expect(poster.headers()['content-type']).toBe('image/jpeg');
  // The tree names it v3 too (its rows are there, in the column or the phone's drawer).
  await expect(page.locator('[data-part="nav-video"][data-name="retake-promo"] .ver')).toHaveText('v3');
  const loaded = (names: string[]) =>
    page.waitForFunction((names) => names.every((n) => {
      const i = document.querySelector<HTMLImageElement>(`a.card[data-name="${n}"] .thumb img`);
      return !!i && i.complete && i.naturalWidth > 0;
    }), names, { timeout: 30_000, polling: 100 });
  await expect(page.locator('a.card')).toHaveCount(3);
  await loaded(['retake-promo', 'retake-board']); // the board idea: its document's image
  // An idea without an image: its title and first lines.
  const idea = page.locator('a.card[data-name="retake-idea"] [data-part="idea-preview"]');
  await expect(idea.locator('b')).toHaveText('Non-meta promo');
  await expect(idea.locator('span').first()).toHaveText('A promo that never shows the app reviewing itself.');
  await expect(idea.locator('span').nth(1)).toHaveText('Cold open');
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `home-${device}.png`), fullPage: true });
  // The other folder's video has its frame too.
  await page.goto(`${f.server.url}#/chess`);
  await expect(page.locator('a.card')).toHaveCount(1, { timeout: 20_000 });
  await loaded(['chess-explainer']);
}
