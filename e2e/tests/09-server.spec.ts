// New in milestone 3b (spec/server.md): the team server. `retake serve` runs with the default drivers (SQLite and
// local files) in a scratch data folder. The admin and the project come from the CLI, as an admin would make them.
// The reviewer signs in through the one-time link (its page posts the code with a button), finds the project on the
// home screen, and records a take on /p/demo/: the same page as `retake open`, under a base path. The take is
// stored in the project's workspace with the account's name as its author, and Done queues its jobs.
import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { CONFIG_HOME, LEGACY, PORTS, RETAKE_BIN, WORK } from '../lib/env.ts';
import { loop, READY, Review, sleep } from '../lib/review.ts';
import { validateTakeDir } from '../lib/schema.ts';
import { startTeamServer, type Server } from '../lib/server.ts';
import { readTake } from '../lib/take.ts';
import { family, takeDirs } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new in milestone 3b: the legacy player has no team server');

const DATA = join(WORK, 'team-server');
const BASE = `http://127.0.0.1:${PORTS.teamServer}`;
const NAME = 'Team Reviewer';
let server: Server;
const pageErrors: string[] = [];

/** Runs a retake admin command against the scratch data folder and returns its JSON output. */
function admin(...args: string[]): Record<string, unknown> {
  const out = execFileSync(RETAKE_BIN, [...args, '--data', DATA, '--public-url', BASE, '--json'], {
    env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME },
  });
  return JSON.parse(out.toString()) as Record<string, unknown>;
}

test.beforeAll(async () => {
  const fam = family('team', ['v6']);
  admin('project', 'add', 'demo', '--title', 'Demo cut', '--video', fam.files.v6!);
  server = await startTeamServer('team', PORTS.teamServer, ['--data', DATA, '--public-url', BASE]);
});

test.afterAll(async () => {
  await server?.stop();
});

async function signIn(page: Page, link: string): Promise<void> {
  await page.goto(link);
  await page.getByRole('button', { name: /Sign in as/ }).click();
  await page.waitForURL(`${BASE}/`);
}

test('a sign-in link opens a session; the link works once', async ({ browser }) => {
  const user = admin('user', 'add', 'team@example.com', '--name', NAME, '--admin') as { link: string };
  const context = await browser.newContext();
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  try {
    // Opening the link alone (a mail scanner) doesn't sign in, and doesn't use the link up.
    await page.goto(user.link);
    await expect(page.getByRole('button', { name: 'Sign in as team@example.com' })).toBeVisible();
    expect(await context.cookies()).toEqual([]);
    await signIn(page, user.link);
    const cookie = (await context.cookies()).find((c) => c.name === 'retake_session');
    expect(cookie).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Lax', path: '/' });
    // The home screen (the page's home.html, as `retake open <folder>` has it) lists the project.
    const home = (await (await page.request.get(`${BASE}/api/home`)).json()) as { projects: { title: string }[] };
    expect(home.projects.map((p) => p.title)).toEqual(['Demo cut']);

    // The link is used: a second browser can't sign in with it.
    const other = await browser.newContext();
    try {
      const p2 = await other.newPage();
      await p2.goto(user.link);
      await expect(p2.locator('body')).toHaveText(/expired or was used/);
    } finally {
      await other.close();
    }
  } finally {
    await context.close();
  }
});

test('a member records a take on /p/demo/, stored with their name', async ({ browser }) => {
  const user = admin('user', 'add', 'ana@example.com', '--name', 'Ana Lima') as { link: string };
  admin('project', 'member', 'add', 'demo', 'ana@example.com', '--role', 'reviewer');
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  const ui = new Review(page);
  try {
    await signIn(page, user.link);
    await page.goto(`${BASE}/p/demo/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    const info = await page.evaluate(() => window.__review.info as { role?: string; label?: string });
    expect(info).toMatchObject({ role: 'reviewer', label: 'v1' });
    await expect(page.locator('#rec')).toBeVisible();

    await page.evaluate(() => { document.querySelector('video')!.currentTime = 4; });
    await ui.waitFor(() => !document.querySelector('video')!.seeking, 5000);
    await ui.key('r');
    await ui.waitFor(() => !!window.__review.seg, 8000);
    await sleep(1200);
    await ui.drag(loop(0.4, 0.45));
    await sleep(800);
    await ui.key('r');
    await ui.waitFor(() => !window.__review.seg, 8000);
    // Done's answer passes through untouched; the test keeps a copy.
    let answer: { ok?: boolean; jobs?: { kind: string }[] } = {};
    await page.route('**/p/demo/api/session/*/done*', async (route) => {
      const res = await route.fetch();
      answer = (await res.json()) as typeof answer;
      await route.fulfill({ response: res });
    });
    await ui.click('#done');
    await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 15_000);
    await sleep(300);

    // Done queued the take's jobs, which the server runs (without a transcriber they fail with the install hint).
    expect(answer.ok).toBe(true);
    expect(answer.jobs?.map((j) => j.kind)).toEqual(['transcribe', 'notes']);
  } finally {
    await context.close();
  }
  const dirs = takeDirs(join(DATA, 'projects', 'demo', 'media', 'demo_v1.review'));
  expect(dirs).toHaveLength(1);
  const take = readTake(dirs[0]!);
  expect(take.session.author).toEqual({ name: 'Ana Lima' });
  expect(take.segments.map((s) => [s.id, s.version])).toEqual([['S1', 'v1']]);
  expect(take.strokes).toHaveLength(1);
  expect(take.audio).toHaveLength(1);
  expect(take.session.closed).toBe(true);
  validateTakeDir(dirs[0]!);
});

test('a viewer can watch but not record; signing out ends the session', async ({ browser }) => {
  const user = admin('user', 'add', 'vic@example.com') as { link: string };
  admin('project', 'member', 'add', 'demo', 'vic@example.com', '--role', 'viewer');
  const context = await browser.newContext();
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  try {
    await signIn(page, user.link);
    await page.goto(`${BASE}/p/demo/`);
    await page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
    await expect(page.locator('#rec')).toHaveCount(0);
    const post = await page.evaluate(async () =>
      (await fetch('api/session/2026-10-30_10-00-00/data?v=v1', { method: 'POST', body: '{}' })).status);
    expect(post).toBe(403);
    await page.goto(`${BASE}/signout`);
    await page.getByRole('button', { name: 'Sign out' }).click();
    await page.waitForURL(`${BASE}/signin`);
    const res = await page.goto(`${BASE}/p/demo/api/info`);
    expect(res!.status()).toBe(401);
  } finally {
    await context.close();
  }
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
