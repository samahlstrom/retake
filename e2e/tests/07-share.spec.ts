// New in milestone 3a (spec/team.md): sharing a review with a link. The server allows a made-up share host
// (`--allow-host share.test`), and a proxy in front of it stands in for the tunnel: every request from the browser
// reaches the server with Host share.test, and the page's origin becomes https://share.test. Tokens come from
// `retake token create`. Nothing here reaches a real tunnel.
//
// Without a token the share host refuses the page and the API. A viewer's link plays the review with no Record, no
// pen and a "View only" hint, and the server refuses its writes too. A reviewer's link records a take, which is saved
// with the link's label as its author.
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { CONFIG_HOME, LEGACY, PORTS, RETAKE_BIN } from '../lib/env.ts';
import { FailingProxy } from '../lib/proxy.ts';
import { loop, READY, Review, sleep } from '../lib/review.ts';
import { validateTakeDir } from '../lib/schema.ts';
import { startServer, type Server } from '../lib/server.ts';
import { readTake } from '../lib/take.ts';
import { allTakeDirs, family, takeDirs, type Family } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new in milestone 3a: the legacy player has no tokens');

const HOST = 'share.test';
let fam: Family;
let server: Server;
let proxy: FailingProxy;
const pageErrors: string[] = [];

/** A token from the CLI, as the owner would make one. */
function token(role: string, label = ''): string {
  const args = ['token', 'create', fam.files.v6!, '--role', role, '--json', ...(label ? ['--label', label] : [])];
  const out = execFileSync(RETAKE_BIN, args, { env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME } });
  return (JSON.parse(out.toString()) as { token: string }).token;
}

async function newPage(browser: Browser): Promise<{ context: BrowserContext; page: Page; ui: Review }> {
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  return { context, page, ui: new Review(page) };
}

/** Opens a share link: the bounce page sets the cookie and replaces the address with the page's own. */
async function openLink(page: Page, tok: string): Promise<void> {
  await page.goto(`${proxy.url}s/${tok}`);
  await page.waitForURL(proxy.url, { timeout: 10_000 });
  await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
}

test.beforeAll(async () => {
  fam = family('share', ['v5', 'v6']);
  server = await startServer('share', fam.files.v6!, PORTS.share, ['--allow-host', HOST]);
  proxy = new FailingProxy(PORTS.shareProxy, PORTS.share, { host: HOST });
  await proxy.start();
});

test.afterAll(async () => {
  await proxy?.stop();
  await server?.stop();
});

test('without a token the share host refuses the page and the API; the local page is unchanged', async ({ browser }) => {
  const { context, page } = await newPage(browser);
  try {
    const res = await page.goto(proxy.url);
    expect(res!.status()).toBe(401);
    await expect(page.locator('body')).toHaveText(/This review needs a share link/);
    const api = await page.evaluate(async () => {
      const get = async (path: string) => (await fetch(path, { headers: { Range: 'bytes=0-9' } })).status;
      const info = await fetch('/api/info');
      return { info: [info.status, await info.json()], video: await get('/video'), events: await get('/api/history'),
        post: (await fetch('/api/session/2026-10-30_10-00-00/data', { method: 'POST', body: '{}' })).status };
    });
    expect(api).toEqual({ info: [401, { error: 'token required' }], video: 401, events: 401, post: 401 });
    const bad = await page.goto(`${proxy.url}s/rt_${'A'.repeat(43)}`);
    expect(bad!.status()).toBe(401);
    await expect(page.locator('body')).toHaveText(/expired or was revoked/);
    expect(await context.cookies()).toEqual([]);
  } finally {
    await context.close();
  }
  // Straight to the server on 127.0.0.1, no token: full access, as in milestone 1.
  const local = (await (await fetch(`${server.url}api/info`)).json()) as { role: string };
  expect(local.role).toBe('owner');
});

test('a viewer link: the review plays, with no Record, no pen and a "View only" hint', async ({ browser }) => {
  const { context, page, ui } = await newPage(browser);
  try {
    await openLink(page, token('viewer'));
    expect(page.url()).toBe(proxy.url); // the token left the address bar
    const cookie = (await context.cookies()).find((c) => c.name === 'retake_share');
    expect(cookie).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Strict', path: '/' });
    expect(await page.evaluate(() => (window.__review.info as { role?: string }).role)).toBe('viewer');

    await expect(page.locator('#rec')).toHaveCount(0);
    await expect(page.locator('#done')).toHaveCount(0);
    await expect(page.locator('.sw')).toHaveCount(0);
    await expect(page.locator('#hint')).toHaveText('View only · Space play · ←/→ 5 s');
    await expect(page.locator('#hint')).not.toHaveClass(/gone/);

    // R only says so, and a drag over the video draws nothing.
    await ui.key('r');
    await sleep(300);
    expect(await ui.state()).toMatchObject({ rec: false, take: null, toast: 'View only' });
    await ui.drag(loop(0.5, 0.5));
    await sleep(200);
    expect((await ui.state()).strokes).toBe(0);

    // It plays, through the tunnel's host.
    const t0 = (await ui.video()).t;
    await ui.key(' ');
    await ui.waitUntil((t) => document.querySelector('video')!.currentTime > t + 1, t0, 10_000);
    await ui.key(' ');

    // The server refuses a viewer's writes too: hiding Record isn't the only guard.
    const post = await page.evaluate(async () =>
      (await fetch('/api/session/2026-10-30_10-00-00/data?v=v6', { method: 'POST', body: '{}' })).status);
    expect(post).toBe(403);
  } finally {
    await context.close();
  }
  expect(allTakeDirs(fam.dir)).toEqual([]);
});

test('a reviewer link records a take, saved with the link\'s label as its author', async ({ browser }) => {
  const { context, page, ui } = await newPage(browser);
  try {
    await openLink(page, token('reviewer', 'alex'));
    expect(await page.evaluate(() => (window.__review.info as { role?: string }).role)).toBe('reviewer');
    await expect(page.locator('#rec')).toBeVisible();
    await expect(page.locator('#hint')).toHaveText(/^R record/);

    await page.evaluate(() => { document.querySelector('video')!.currentTime = 6; });
    await ui.waitFor(() => !document.querySelector('video')!.seeking, 5000);
    await ui.key('r');
    await ui.waitFor(() => !!window.__review.seg, 8000);
    await sleep(1200);
    await ui.drag(loop(0.4, 0.45));
    await sleep(800);
    await ui.key('r');
    await ui.waitFor(() => !window.__review.seg, 8000);
    await ui.click('#done');
    await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 15_000);
    await sleep(300);
  } finally {
    await context.close();
  }
  const dirs = takeDirs(join(fam.dir, 'v6', 'clip_v6.review'));
  expect(dirs).toHaveLength(1);
  const take = readTake(dirs[0]!);
  expect(take.session.author).toEqual({ name: 'alex' });
  expect(take.segments.map((s) => [s.id, s.version])).toEqual([['S1', 'v6']]);
  expect(take.strokes).toHaveLength(1);
  expect(take.audio).toHaveLength(1);
  expect(take.session.closed).toBe(true);
  validateTakeDir(dirs[0]!);
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
