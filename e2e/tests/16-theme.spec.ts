// New (spec/theme.md): the look belongs to the user and lives outside the install. `retake open <folder>` serves a
// folder of two projects, first with no theme (the built-in look, untouched), then with each example theme from
// examples/themes/: from the default folder (~/.config/retake/theme, here a scratch XDG_CONFIG_HOME) and with
// --theme. A token and a part override apply, the name and logo show, the canvas follows the tokens, a broken theme
// never breaks the page, `retake theme check` flags a token this version doesn't have, and a freshly built binary
// (an update) keeps the user's theme without touching it. RETAKE_E2E_THEME_SHOTS=<dir> saves desktop and phone
// screenshots of the home and the review page for each look.
import { expect, test, type Browser, type Page } from '@playwright/test';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFileSync, cpSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, utimesSync,
  writeFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { LEGACY, PORTS, REPO, RETAKE_BIN, WORK } from '../lib/env.ts';
import { READY } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no themes');

const DIR = join(WORK, 'theme-folder');
/** The user's config folder for these servers: the theme lives in <it>/retake/theme, outside any install. */
const CONFIG = join(WORK, 'theme-config');
const THEME = join(CONFIG, 'retake', 'theme');
const EXAMPLES = join(REPO, 'examples', 'themes');
const SHOTS = process.env.RETAKE_E2E_THEME_SHOTS;
let server: Server | undefined;
const pageErrors: string[] = [];

function put(src: string, rel: string, minutes: number): void {
  const dst = join(DIR, rel);
  mkdirSync(dirname(dst), { recursive: true });
  copyFileSync(src, dst);
  const t = new Date(Date.UTC(2026, 9, 1, 12, minutes));
  utimesSync(dst, t, t);
}

test.beforeAll(() => {
  const m = mediaLayout(WORK);
  put(m.v5, 'alpha/v1/alpha_v1.mp4', 0);
  put(m.v6, 'alpha/v2/alpha_v2.mp4', 180);
  put(m.v7preview, 'bravo/bravo_v1.mp4', 120);
  mkdirSync(CONFIG, { recursive: true });
});

test.afterAll(async () => {
  await server?.stop();
  rmSync(CONFIG, { recursive: true, force: true });
});

/** (Re)starts the folder's server with the scratch config folder, and more flags. */
async function serve(extra: string[] = [], bin = RETAKE_BIN): Promise<Server> {
  await server?.stop();
  server = await startServer('theme', DIR, PORTS.theme, ['--verbose', ...extra],
    { bin, env: { XDG_CONFIG_HOME: CONFIG } });
  return server;
}

/** Puts an example theme where retake open looks by default. */
function useTheme(name: string | null): void {
  rmSync(THEME, { recursive: true, force: true });
  if (name) cpSync(join(EXAMPLES, name), THEME, { recursive: true });
}

async function newPage(browser: Browser, phone = false): Promise<Page> {
  const context = phone
    ? await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true,
      deviceScaleFactor: 2 })
    : await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  return page;
}

/** The home with its thumbnails loaded. */
async function openHome(page: Page, url: string): Promise<void> {
  await page.goto(url);
  await page.waitForFunction(() => {
    const imgs = [...document.querySelectorAll<HTMLImageElement>('a.card .thumb img')];
    return imgs.length === 2 && imgs.every((i) => i.complete && i.naturalWidth > 0);
  }, null, { timeout: 20_000, polling: 100 });
  await page.evaluate(() => document.fonts.ready);
}

async function openReview(page: Page, url: string): Promise<void> {
  await page.goto(`${url}p/alpha/`);
  await page.waitForFunction(READY, null, { timeout: 20_000, polling: 100 });
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(300); // a frame of the timeline in the theme's colours
}

/** Saves the four screenshots of one look (home and review page, desktop and phone) when RETAKE_E2E_THEME_SHOTS is set. */
async function shots(browser: Browser, url: string, look: string): Promise<void> {
  if (!SHOTS) return;
  mkdirSync(SHOTS, { recursive: true });
  for (const phone of [false, true]) {
    const page = await newPage(browser, phone);
    try {
      const size = phone ? 'phone' : 'desktop';
      await openHome(page, url);
      await page.mouse.move(0, 0);
      await page.screenshot({ path: join(SHOTS, `${look}-home-${size}.png`), animations: 'disabled' });
      await openReview(page, url);
      await page.waitForTimeout(6000); // the agent's status settles (the page asks every 5 s)
      await page.mouse.move(0, 0);
      await page.screenshot({ path: join(SHOTS, `${look}-review-${size}.png`), animations: 'disabled' });
    } finally {
      await page.context().close();
    }
  }
}

/** The theme's files and their hashes: nothing in Retake may write there. */
function snapshot(dir: string): Record<string, string> {
  const out: Record<string, string> = {};
  const walk = (d: string) => {
    for (const name of readdirSync(d)) {
      const p = join(d, name);
      if (statSync(p).isDirectory()) walk(p);
      else out[relative(dir, p)] = createHash('sha256').update(readFileSync(p)).digest('hex');
    }
  };
  walk(dir);
  return out;
}

/** What the page shows of the brand theme: its token, a part's override, the name, the logo and the font. */
async function expectBrand(page: Page, url: string): Promise<void> {
  await openHome(page, url);
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--accent').trim()))
    .toBe('#2dd4bf');
  await expect(page).toHaveTitle('theme-folder · Northwind Review');
  const brand = page.locator('[data-part~="brand"]');
  await expect(brand).toHaveText('Northwind Review'); // a theme's name replaces the wordmark, red period and all
  await expect(brand.locator('[data-part~="wordmark-dot"]')).toHaveCount(0);
  expect(await brand.evaluate((el) => getComputedStyle(el).color)).toBe('rgb(45, 212, 191)'); // the part's rule
  const logo = brand.locator('[data-part~="logo"]');
  await expect(logo).toHaveAttribute('src', 'theme/logo.svg');
  expect(await logo.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBe(true);
  expect(await page.locator('link[rel="icon"]').getAttribute('href')).toBe('theme/favicon.svg');
  expect(await page.evaluate(() => document.fonts.check('13px "Northwind Serif"'))).toBe(true);
  expect(await page.locator('h1').evaluate((el) => getComputedStyle(el).fontFamily)).toContain('Northwind Serif');

  await openReview(page, url);
  await expect(page).toHaveTitle(/^Review · alpha_v2(\.mp4)? · Northwind Review$/);
  const send = page.locator('[data-part~="send"]');
  expect(await send.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe('rgb(45, 212, 191)'); // --accent
  const bar = page.locator('[data-part~="bar"]');
  expect(await bar.evaluate((el) => getComputedStyle(el).borderTopColor)).toContain('0.45'); // the part's rule
  expect(await bar.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe('rgb(15, 24, 40)'); // --surface
}

test('without a theme folder the page is served exactly as built', async ({ browser }) => {
  useTheme(null);
  const s = await serve();
  for (const path of ['', 'p/alpha/']) {
    const html = await (await fetch(s.url + path)).text();
    expect(html).not.toContain('theme/theme.css');
    expect(html).not.toContain('data-theme');
  }
  expect((await fetch(`${s.url}theme/theme.css`)).status).toBe(404);
  await shots(browser, s.url, 'default');
});

test('the built-in look comes from the tokens, and every answer says the API version', async ({ browser }) => {
  const s = server!;
  const page = await newPage(browser);
  try {
    await openReview(page, s.url);
    const tokens = await page.evaluate(() => {
      const css = getComputedStyle(document.documentElement);
      return ['--bg', '--accent', '--record', '--agent'].map((t) => css.getPropertyValue(t).trim());
    });
    expect(tokens).toEqual(['#09090a', '#ececef', '#ff453a', '#a693fa']);
    expect(await page.locator('meta[name="retake-theme-api"]').getAttribute('content')).toBe('1');
    await expect(page).toHaveTitle(/^Review · alpha_v2(\.mp4)?$/);
    // The built-in wordmark on the home: Retake and a period in the record red; the title stays plain.
    await openHome(page, s.url);
    await expect(page.locator('[data-part~="brand"]')).toHaveText('Retake.');
    expect(await page.locator('[data-part~="wordmark-dot"]').evaluate((el) => getComputedStyle(el).color))
      .toBe('rgb(255, 69, 58)');
    await expect(page).toHaveTitle('theme-folder · Retake');
    const r = await fetch(`${s.url}p/alpha/api/info`);
    expect(r.headers.get('x-retake-api-version')).toBe('1');
  } finally {
    await page.context().close();
  }
});

test('a theme in ~/.config/retake/theme applies with no flag: tokens, parts, name, logo and font', async ({ browser }) => {
  useTheme('brand');
  const s = await serve();
  const css = await fetch(`${s.url}theme/theme.css`);
  expect(css.status).toBe(200);
  expect(css.headers.get('content-type')).toBe('text/css; charset=utf-8');
  expect(css.headers.get('content-security-policy')).toContain('sandbox');
  expect(css.headers.get('x-content-type-options')).toBe('nosniff');
  expect((await fetch(`${s.url}theme/fonts/NotoSerif-Regular.woff2`)).headers.get('content-type')).toBe('font/woff2');
  expect((await fetch(`${s.url}p/alpha/theme/logo.svg`)).headers.get('content-type')).toBe('image/svg+xml');
  expect(s.logTail()).toContain('retake: theme ');
  const page = await newPage(browser);
  try {
    await expectBrand(page, s.url);
  } finally {
    await page.context().close();
  }
  await shots(browser, s.url, 'brand');
});

test('--theme picks another folder: the light theme, down to the timeline canvas', async ({ browser }) => {
  useTheme('brand'); // the default folder is there, but the flag wins
  const s = await serve(['--theme', join(EXAMPLES, 'light')]);
  const page = await newPage(browser);
  try {
    await openReview(page, s.url);
    expect(await page.locator('html').getAttribute('data-theme')).toBe('light');
    await expect(page).toHaveTitle(/^Review · alpha_v2(\.mp4)? · Retake Light$/);
    expect(await page.locator('[data-part~="bar"]').evaluate((el) => getComputedStyle(el).backgroundColor))
      .toBe('rgb(255, 255, 255)');
    expect(await page.locator('[data-part~="stage"]').evaluate((el) => getComputedStyle(el).backgroundColor))
      .toBe('rgb(17, 17, 20)'); // a part's rule over the token
    // The timeline is drawn on a canvas: its track is the theme's (dark ink at 14%), not the built-in white.
    const track = await page.locator('#tl').evaluate((c: HTMLCanvasElement) => {
      const g = c.getContext('2d')!;
      const [r, gr, b, a] = g.getImageData(Math.round(c.width * 0.9), Math.round(c.height / 2), 1, 1).data;
      return { rgb: r! + gr! + b!, a: a! };
    });
    expect(track.a).toBeGreaterThan(0);
    expect(track.rgb).toBeLessThan(150);
  } finally {
    await page.context().close();
  }
  await shots(browser, s.url, 'light');
});

test('a broken theme never breaks the page; the server and retake theme check say what is wrong', async ({ browser }) => {
  rmSync(THEME, { recursive: true, force: true });
  mkdirSync(THEME, { recursive: true });
  writeFileSync(join(THEME, 'theme.toml'), 'name = "unterminated\nlogo = [');
  writeFileSync(join(THEME, 'theme.css'), ':root { --accent: ; --gone-token: 2px } }}} @@@ [data-part~="nope"] {');
  writeFileSync(join(THEME, 'steal.js'), 'alert(1)');
  const s = await serve();
  expect((await fetch(`${s.url}theme/steal.js`)).status).toBe(404);
  const before = pageErrors.length;
  const page = await newPage(browser);
  try {
    await openReview(page, s.url);
    await expect(page.locator('[data-part~="record"]')).toBeVisible();
    await expect(page.locator('[data-part~="send"]')).toBeVisible();
    await openHome(page, s.url);
    await expect(page.locator('[data-part~="video-card"]')).toHaveCount(2);
  } finally {
    await page.context().close();
  }
  expect(pageErrors.slice(before)).toEqual([]);
  expect(s.logTail()).toContain('retake: theme: theme.toml is ignored');
  const check = spawnSync(RETAKE_BIN, ['theme', 'check', THEME], { encoding: 'utf8' });
  expect(check.status).toBe(1);
  expect(check.stdout).toContain('--gone-token isn\'t a token');
  expect(check.stdout).toContain('steal.js is never served');
  expect(check.stdout).toContain('theme.toml is ignored');
});

test('retake theme check flags a token this version does not have, and passes the examples', () => {
  const dir = join(WORK, 'theme-old');
  rmSync(dir, { recursive: true, force: true });
  cpSync(join(EXAMPLES, 'light'), dir, { recursive: true });
  for (const example of ['light', 'brand']) {
    const ok = spawnSync(RETAKE_BIN, ['theme', 'check', join(EXAMPLES, example)], { encoding: 'utf8' });
    expect(ok.status, ok.stdout + ok.stderr).toBe(0);
  }
  // A theme written for an older version, with a token that was since renamed.
  writeFileSync(join(dir, 'theme.css'), `${readFileSync(join(dir, 'theme.css'), 'utf8')}\n:root { --hair: #ddd; }\n`);
  const r = spawnSync(RETAKE_BIN, ['theme', 'check', dir, '--json'], { encoding: 'utf8' });
  expect(r.status).toBe(1);
  const report = JSON.parse(r.stdout) as { problems: { kind: string; name: string }[] };
  expect(report.problems).toEqual([expect.objectContaining({ kind: 'unknown-token', name: '--hair' })]);
});

test('updating retake keeps the look: a freshly built binary serves the same theme and never writes to it',
  async ({ browser }) => {
    test.setTimeout(300_000);
    useTheme('brand');
    const before = snapshot(THEME);
    const fresh = join(WORK, 'update', 'retake');
    mkdirSync(dirname(fresh), { recursive: true });
    try {
      execFileSync('nice', ['-n', '19', 'go', 'build', '-tags', 'webdist', '-trimpath', '-o', fresh, './cmd/retake'],
        { cwd: REPO, stdio: 'pipe' });
    } catch (e) {
      test.skip(true, `can't build a fresh binary here (${(e as Error).message.split('\n')[0]})`);
    }
    expect(existsSync(fresh)).toBe(true);
    const s = await serve([], fresh); // the "updated" install: another binary, another path, same user
    const page = await newPage(browser);
    try {
      await expectBrand(page, s.url);
    } finally {
      await page.context().close();
    }
    await s.stop();
    expect(snapshot(THEME)).toEqual(before);
    rmSync(dirname(fresh), { recursive: true, force: true });
  });
