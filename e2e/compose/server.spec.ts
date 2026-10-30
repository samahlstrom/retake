// The team server in Docker Compose (retake + Postgres + MinIO), end to end. deploy/smoke.sh starts the stack, makes
// the admin, runs this with SMOKE_URL and SMOKE_LINK, then checks the bucket and tears the stack down.
//
// The admin signs in through the link in a headless browser, creates a project and an agent token over the JSON
// routes, and the editor's machine (this host's bin/retake) pushes a generated video with `retake push --remote`.
// The admin then opens the project, records a take (Chromium's fake mic) and presses Done. The take's jobs must run
// in the server: transcription either works, or fails with the install hint; the notes job follows it.
import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { RETAKE_BIN } from '../lib/env.ts';
import { loop, READY, Review, sleep } from '../lib/review.ts';

const BASE = process.env.SMOKE_URL ?? '';
const LINK = process.env.SMOKE_LINK ?? '';
const WORK = process.env.SMOKE_WORK ?? '';

test('sign in, push a version, record a take, run its jobs', async ({ page }) => {
  test.skip(!BASE || !LINK || !WORK, 'run by deploy/smoke.sh');
  mkdirSync(WORK, { recursive: true });
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(`${e.name}: ${e.message}`));

  // Sign in through the one-time link.
  await page.goto(LINK);
  await page.getByRole('button', { name: /Sign in as/ }).click();
  await page.waitForURL(`${BASE}/`);

  // A project and an agent token, over the admin routes, with the browser's session. (The server's own pages
  // forbid scripts from fetching, so this goes through Playwright's request API, which sends the cookies.)
  const post = (path: string, data: unknown) =>
    page.context().request.post(BASE + path, { data, headers: { Origin: BASE } });
  expect((await post('/api/projects', { name: 'demo', title: 'Smoke test' })).status()).toBe(201);
  const madeToken = await post('/api/projects/demo/tokens', { role: 'agent', label: 'editor' });
  expect(madeToken.status()).toBe(201);
  const tok = ((await madeToken.json()) as { token: string }).token;

  // The editor's machine pushes a generated video.
  const clip = join(WORK, 'smoke_cut.mp4');
  execFileSync('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-threads', '2', '-f', 'lavfi', '-i',
    'testsrc2=s=640x360:r=25:d=8', '-f', 'lavfi', '-i', 'sine=f=440:sample_rate=48000:d=8', '-c:v', 'libx264',
    '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-shortest', '-movflags', '+faststart', clip]);
  const cliEnv = { ...process.env, XDG_CONFIG_HOME: join(WORK, 'config'), XDG_STATE_HOME: join(WORK, 'state') };
  execFileSync(RETAKE_BIN, ['remote', 'add', 'smoke', `${BASE}/p/demo`, '--token', tok], { env: cliEnv });
  const pushed = JSON.parse(execFileSync(RETAKE_BIN, ['push', '--remote', 'smoke', clip, '--json'],
    { env: cliEnv }).toString()) as { version: { label: string; sha256: string } };
  expect(pushed.version.label).toBe('v1');

  // Record a take on the project's page.
  const ui = new Review(page);
  await page.goto(`${BASE}/p/demo/`);
  await page.waitForFunction(READY, null, { timeout: 30_000, polling: 100 });
  expect(await page.evaluate(() => (window.__review.info as { role?: string }).role)).toBe('owner');
  await page.evaluate(() => { document.querySelector('video')!.currentTime = 2; });
  await ui.waitFor(() => !document.querySelector('video')!.seeking, 5000);
  await ui.key('r');
  await ui.waitFor(() => !!window.__review.seg, 10_000);
  await sleep(1500);
  await ui.drag(loop(0.5, 0.5));
  await sleep(1500);
  await ui.key('r');
  await ui.waitFor(() => !window.__review.seg, 10_000);
  await ui.click('#done');
  await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 20_000);
  const take = await page.evaluate(() => window.__review.take.id as string);

  // The take's jobs run in the server: wait until none is pending (all done) or one failed.
  type Job = { kind: string; state: string; error?: string; hint?: string };
  let jobs: Job[] = [];
  const deadline = Date.now() + 480_000;
  for (;;) {
    jobs = await page.evaluate(async () =>
      ((await (await fetch('api/notes?v=v1')).json()) as { jobs: Job[] }).jobs);
    if (jobs.length === 0 || jobs.some((j) => j.state === 'failed')) break;
    if (Date.now() > deadline) throw new Error(`jobs still pending: ${JSON.stringify(jobs)}`);
    await sleep(2000);
  }
  // Settle: a failed transcription fails its notes job right after.
  await sleep(3000);
  jobs = await page.evaluate(async () => ((await (await fetch('api/notes?v=v1')).json()) as { jobs: Job[] }).jobs);
  const failedTranscribe = jobs.find((j) => j.kind === 'transcribe' && j.state === 'failed');
  if (failedTranscribe) expect(failedTranscribe.hint ?? '').not.toBe('');
  writeFileSync(join(WORK, 'result.json'), JSON.stringify({ take, version: pushed.version, jobs }, null, 2));
  expect(errors).toEqual([]);
});
