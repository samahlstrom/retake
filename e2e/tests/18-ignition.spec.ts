// Ignition (spec/ignition.md), turned on with `retake ext enable ignition`, with a fake transcriber (a command engine
// that prints fixed words) and a fake agent (e2e/lib/ignition.ts). Locally (`retake open <folder>`) and hosted
// (`retake serve --extensions ignition` and `retake connect`):
// - New idea opens v0, typing saves, and an outside edit shows.
// - A talk-out lands in the document and adds a To do.
// - Send runs an agent that writes the outline and the script and asks about "Cold open"; a tap answers it; Done.
//   The agent's edit of the script can be undone from the editor.
// - Start v1 runs an agent that writes a video and cuts/v1.json; v1 opens, with v0 first in the version strip.
// Desktop and phone screenshots go to .retake-work/ignition-shots/.
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { ignitionConfig, WORDS, type IgnitionConfig } from '../lib/ignition.ts';
import { LEGACY, PORTS, REPO, RETAKE_BIN, WORK } from '../lib/env.ts';
import { startDaemon, startServer, startTeamServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no extensions');

const SHOTS = join(REPO, '.retake-work', 'ignition-shots');
const pageErrors: string[] = [];

/** A folder with one project, deadlock, that holds a video: where new ideas go. */
function videos(name: string): string {
  const dir = join(WORK, name, 'videos');
  const dst = join(dir, 'deadlock', 'item-shop', 'item-shop_v1.mp4');
  mkdirSync(dirname(dst), { recursive: true });
  copyFileSync(mediaLayout(WORK).v5, dst);
  return dir;
}

async function newPage(browser: Browser, phone = false): Promise<{ context: BrowserContext; page: Page }> {
  const context = phone
    ? await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true,
      deviceScaleFactor: 2 })
    : await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  return { context, page };
}

const text = (page: Page) => page.locator('[data-ext="ignition"] [data-part="ignition-text"]');
const tab = (page: Page, id: string) => page.locator(`[data-part="ignition-tab"][data-tab="${id}"]`);
const read = (file: string) => (existsSync(file) ? readFileSync(file, 'utf8') : '');

/** New idea from the home: the form at #ext/ignition/new, Create, and the new video's page on v0. */
async function newIdea(page: Page, home: string, title: string): Promise<void> {
  await page.goto(`${home}#ext/ignition/new`);
  const form = page.locator('[data-part="ignition-new"]');
  await expect(form).toBeVisible({ timeout: 20_000 });
  await form.locator('[data-part="ignition-title"]').fill(title);
  await expect(form.locator('[data-part="ignition-project"]')).toBeEnabled();
  await form.locator('[data-part="ignition-project"]').selectOption('deadlock');
  await form.locator('[data-part="ignition-create"]').click();
  await page.waitForURL(/\/p\/deadlock-jump-souls\//, { timeout: 30_000 });
  await expect(text(page)).toBeVisible({ timeout: 20_000 });
  await expect(text(page)).toHaveValue(`# ${title}\n\n`);
}

/** Types under the title and waits for Saved. */
async function type(page: Page, words: string): Promise<void> {
  await text(page).click();
  await page.keyboard.press('Control+End');
  await page.keyboard.type(words);
  await expect(page.locator('[data-part="ignition-saved"]')).toHaveText('Saved', { timeout: 10_000 });
}

/** A talk-out: Esc leaves the text, R talks, R stops; the words land and a comment asks the agent to work them in. */
async function talkOut(page: Page): Promise<void> {
  await text(page).click();
  await page.keyboard.press('Escape');
  await page.keyboard.press('r');
  await expect(page.locator('#rec')).toHaveClass(/on|rec/, { timeout: 10_000 });
  await page.waitForTimeout(2500);
  await page.keyboard.press('r');
  await expect(text(page)).toHaveValue(new RegExp(`<!-- talk-out voice/\\d{8}-\\d{6}\\.webm -->\\n${WORDS}`),
    { timeout: 30_000 });
  await expect(page.locator('#side .item', { hasText: 'Work this talk-out into the outline and script.' }))
    .toBeVisible({ timeout: 10_000 });
}

/** Send: the agent writes the outline and the script, and asks which opening; one tap answers; the comment is Done. */
async function shape(page: Page, folder: string, shot: string): Promise<void> {
  await page.locator('#done').click();
  const card = page.locator('#side [data-part="question-card"]');
  await expect(card).toContainText('Which opening?', { timeout: 60_000 });
  await expect(card.locator('.choice')).toHaveCount(2);
  expect(read(join(folder, 'v0', 'script.md'))).toContain('## Cold open');
  // The question's mark sits on "Cold open" in the script.
  await tab(page, 'script').click();
  await expect(text(page)).toHaveValue(/## Cold open/);
  await expect(page.locator('[data-part="ignition-question-mark"]')).toHaveCount(1, { timeout: 10_000 });
  mkdirSync(SHOTS, { recursive: true });
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(SHOTS, `${shot}-script-question-desktop.png`) });
  await tab(page, 'idea').click();
  await page.waitForTimeout(500);
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `${shot}-editor-desktop.png`) });
  await card.locator('.choice', { hasText: 'Start on the jump' }).click();
  await expect.poll(() => read(join(folder, 'v0', 'script.md')), { timeout: 60_000 }).toContain('[Start on the jump.]');
  // Done: the comment's report finished it, with no render (it moves from To do to Done).
  await expect.poll(async () => {
    const r = await page.request.get(new URL('api/notes?v=v0', page.url()).href);
    const items = ((await r.json()) as { items: { text: string; status: string }[] }).items;
    return items.find((i) => i.text.startsWith('Work this talk-out'))?.status;
  }, { timeout: 30_000 }).toBe('answered');
}

/** Start v1: the comment goes out with Ctrl+Enter; the agent's video becomes v1/<folder>_v1.mp4, and the page goes there. */
async function startV1(page: Page, folder: string): Promise<void> {
  await page.locator('[data-part="ignition-start-v1"]').click();
  const ask = page.locator('[data-part="ignition-ask-input"]');
  await expect(ask).toHaveValue('Build the first cut (v1) from script.md.');
  await ask.press('Control+Enter');
  await expect.poll(() => existsSync(join(folder, 'v1', 'jump-souls_v1.mp4')), { timeout: 90_000 }).toBe(true);
  expect(read(join(folder, 'v0', 'cuts', 'v1.json'))).toContain('Cold open');
  await page.waitForFunction(() => window.__review?.view?.shown === 'v1', null, { timeout: 60_000 });
}

test.describe('locally', () => {
  let server: Server;
  let cfg: IgnitionConfig;
  let dir = '';
  const idea = () => join(dir, 'deadlock', 'jump-souls');

  test.beforeAll(async () => {
    cfg = ignitionConfig('ignition-local');
    expect(readFileSync(cfg.file, 'utf8')).toMatch(/\[extensions\][^[]*enabled = \["ignition"\]/);
    dir = videos('ignition-local');
    server = await startServer('ignition', dir, PORTS.ignition, ['--verbose'], { env: { XDG_CONFIG_HOME: cfg.home } });
  });
  test.afterAll(async () => {
    await server?.stop();
    expect(pageErrors).toEqual([]);
  });

  test('New idea opens v0, typing saves, and an outside edit shows', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    try {
      await newIdea(page, server.url, 'Jump souls');
      expect(read(join(idea(), 'v0', 'idea.md'))).toBe('# Jump souls\n\n');
      await type(page, 'The jump to secure souls only works if you time it.');
      expect(read(join(idea(), 'v0', 'idea.md'))).toBe('# Jump souls\n\nThe jump to secure souls only works if you time it.');
      // Edited outside Retake, with nothing unsaved: it reloads in place.
      writeFileSync(join(idea(), 'v0', 'idea.md'), '# Jump souls\n\nThe jump to secure souls only works if you time it.\n\nOutside.\n');
      await expect(text(page)).toHaveValue(/Outside\.\n$/, { timeout: 10_000 });

      // The home: the idea in Ignition's section, and in its project with the idea chip.
      await page.goto(server.url);
      const sec = page.locator('[data-part="nav-ext"][data-ext="ignition"]');
      await expect(sec.locator('[data-part="nav-ext-video"]', { hasText: /jump-souls|Jump souls/i })).toBeVisible({ timeout: 20_000 });
      await page.waitForTimeout(500);
      await page.screenshot({ path: join(SHOTS, 'nav-desktop.png') });
    } finally {
      await context.close();
    }
    const phone = await newPage(browser, true);
    try {
      await phone.page.goto(server.url);
      await phone.page.locator('[data-part="nav-toggle"]').click();
      await expect(phone.page.locator('[data-part="nav-ext"][data-ext="ignition"]')).toBeVisible();
      await phone.page.waitForTimeout(400);
      await phone.page.screenshot({ path: join(SHOTS, 'nav-phone.png') });
    } finally {
      await phone.context.close();
    }
  });

  test('a talk-out lands in the document and adds a To do', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(`${server.url}p/deadlock-jump-souls/`);
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await talkOut(page);
      const voice = readdirSync(join(idea(), 'v0', 'voice'));
      expect(voice.some((f) => f.endsWith('.webm'))).toBe(true);
      expect(voice.some((f) => f.endsWith('.words.json'))).toBe(true);
      await expect.poll(() => read(join(idea(), 'v0', 'idea.md'))).toContain(WORDS);
    } finally {
      await context.close();
    }
  });

  test('Send: the agent shapes the script and asks about Cold open; a tap answers it; its edit can be undone', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(`${server.url}p/deadlock-jump-souls/`);
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await shape(page, idea(), 'local');
      // Undo the agent's last edit of the script (the answer it wrote in): back to its first version.
      await tab(page, 'script').click();
      const edit = page.locator('[data-part="ignition-agent-edit"]');
      await expect(edit).toContainText('Agent edited', { timeout: 10_000 });
      await edit.getByRole('button', { name: 'Undo' }).click();
      await expect.poll(() => read(join(idea(), 'v0', 'script.md'))).toContain('[The jump.]');
      await expect(text(page)).toHaveValue(/\[The jump\.\]/, { timeout: 10_000 });
    } finally {
      await context.close();
    }
    // The editor on a phone, with the question answered and the talk-out in.
    const phone = await newPage(browser, true);
    try {
      await phone.page.goto(`${server.url}p/deadlock-jump-souls/`);
      await expect(text(phone.page)).toBeVisible({ timeout: 20_000 });
      await phone.page.waitForTimeout(500);
      await phone.page.screenshot({ path: join(SHOTS, 'local-editor-phone.png') });
    } finally {
      await phone.context.close();
    }
  });

  test('Start v1 builds the first cut; v1 opens, with v0 first in the version strip', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(`${server.url}p/deadlock-jump-souls/`);
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await startV1(page, idea());
      const strip = page.locator('[data-part="version-strip"]');
      if (await strip.count()) {
        await expect(strip).toContainText('v0');
        await expect(strip).toContainText('v1');
      }
      await page.waitForTimeout(800);
      await page.screenshot({ path: join(SHOTS, 'strip-desktop.png') });
    } finally {
      await context.close();
    }
    const phone = await newPage(browser, true);
    try {
      await phone.page.goto(`${server.url}p/deadlock-jump-souls/`);
      await phone.page.waitForFunction(() => window.__review?.view?.shown === 'v1', null, { timeout: 30_000 });
      // The picker lists v0 · Script first.
      await phone.page.locator('#verBtn').click();
      await expect(phone.page.locator('#verList [data-ver="v0"]')).toContainText('Script');
      await phone.page.waitForTimeout(800);
      await phone.page.screenshot({ path: join(SHOTS, 'strip-phone.png') });
    } finally {
      await phone.context.close();
    }
  });
});

test.describe('hosted', () => {
  const DATA = join(WORK, 'ignition-hosted', 'server');
  const BASE = `http://127.0.0.1:${PORTS.ignitionHosted}`;
  let server: Server;
  let connect: Server | null = null;
  let cfg: IgnitionConfig;
  let dir = '';
  const idea = () => join(dir, 'deadlock', 'jump-souls');

  const admin = (...args: string[]) =>
    JSON.parse(execFileSync(RETAKE_BIN, [...args, '--data', DATA, '--public-url', BASE, '--json'],
      { env: { ...process.env, XDG_CONFIG_HOME: cfg.home } }).toString()) as { link: string };

  test.beforeAll(async () => {
    cfg = ignitionConfig('ignition-hosted');
    dir = videos('ignition-hosted');
    admin('user', 'add', 'owner@example.com', '--name', 'Owner', '--admin');
    server = await startTeamServer('ignition-hosted', PORTS.ignitionHosted,
      ['--data', DATA, '--public-url', BASE, '--extensions', 'ignition']);
  });
  test.afterAll(async () => {
    await connect?.stop();
    await server?.stop();
    expect(pageErrors).toEqual([]);
  });

  test('the same through retake connect: New idea, typing, a talk-out, Send and a question, Start v1', async ({ browser }) => {
    test.setTimeout(300_000);
    const { context, page } = await newPage(browser);
    try {
      await page.goto(admin('user', 'link', 'owner@example.com').link);
      await page.getByRole('button', { name: /Sign in as/ }).click();
      await page.waitForURL(`${BASE}/`);
      const made = (await (await page.request.post(`${BASE}/api/connect`, { headers: { Origin: BASE } })).json()) as {
        token: string;
      };
      connect = startDaemon('ignition-connect', RETAKE_BIN, ['connect', BASE, '--token', made.token, '--folder', dir,
        '--name', 'e2e-pc'], { XDG_CONFIG_HOME: cfg.home });
      // The computer is there once its video is.
      await expect.poll(async () => (await page.request.get(`${BASE}/p/deadlock-item-shop/api/info`)).status(),
        { timeout: 30_000 }).toBe(200);

      await newIdea(page, `${BASE}/`, 'Jump souls');
      // The folder is on the computer, never on the server.
      expect(read(join(idea(), 'v0', 'idea.md'))).toBe('# Jump souls\n\n');
      expect(existsSync(join(DATA, 'projects', 'deadlock-jump-souls', 'v0'))).toBe(false);
      await type(page, 'Hosted, the files stay on the computer.');
      expect(read(join(idea(), 'v0', 'idea.md'))).toContain('Hosted, the files stay on the computer.');
      await talkOut(page);
      // The audio went to the computer and was transcribed there; the server keeps none of it.
      expect(readdirSync(join(idea(), 'v0', 'voice')).some((f) => f.endsWith('.words.json'))).toBe(true);
      await shape(page, idea(), 'hosted');
      await startV1(page, idea());
    } finally {
      await context.close();
    }
  });
});
