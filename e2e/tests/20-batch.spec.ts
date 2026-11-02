// A Send is one batch (spec/automation.md, "Batches"): three comments recorded on a plain video (a fake transcriber, a
// command engine that prints fixed words), then Send, go to one agent run (a fake agent that logs each launch and
// copies the newest version as its edit) and make exactly one new version. While it works, the comments column says
// "Agent working on 3 comments" as one line. A screenshot of that goes to .retake-work/batch-shots/.
import { expect, test } from '@playwright/test';
import { linkSync, mkdirSync, readdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { AUTHOR, LEGACY, PORTS, REPO, WORK } from '../lib/env.ts';
import { startServer, type Server } from '../lib/server.ts';
import { READY } from '../lib/review.ts';

test.skip(LEGACY, 'new: the legacy player has no agent');

const DIR = join(WORK, 'batch');
const VIDEO = join(DIR, 'promo');
const CONFIG = join(DIR, 'config');
const GATE = join(DIR, 'gate');
const LAUNCHES = join(DIR, 'launches.jsonl');
const SHOTS = join(REPO, '.retake-work', 'batch-shots');

/** The fake transcriber: the same few words, whatever the audio. */
const TRANSCRIBER = `const words = 'Make this part shorter please'.split(' ');
process.stdout.write(JSON.stringify({ words: words.map((w, i) => ({ w: ' ' + w, s: i * 0.25, e: i * 0.25 + 0.25, p: 0.9 })) }));
`;

/** The fake agent: logs its launch with the task's notes, waits for the test's gate, then writes the newest version
 * as its edit (a copy) and reports. */
const AGENT = `import { appendFileSync, copyFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
const ctx = JSON.parse(readFileSync(process.env.RETAKE_CONTEXT, 'utf8'));
appendFileSync(${JSON.stringify(LAUNCHES)}, JSON.stringify({ task: ctx.task.id, notes: ctx.task.notes }) + '\\n');
const until = Date.now() + 60000;
while (!existsSync(${JSON.stringify(GATE)}) && Date.now() < until) await new Promise((r) => setTimeout(r, 100));
copyFileSync(ctx.family.at(-1).path, process.env.RETAKE_OUTPUT);
writeFileSync(process.env.RETAKE_REPORT, JSON.stringify({ state: 'completed', message: 'Shortened all three parts',
  notes: Object.fromEntries(ctx.task.notes.map((id) => [String(id), 'Shortened part #' + id])) }));
`;

let server: Server;

test.beforeAll(async () => {
  mkdirSync(VIDEO, { recursive: true });
  mkdirSync(join(CONFIG, 'retake'), { recursive: true });
  linkSync(mediaLayout(WORK).v6, join(VIDEO, 'promo_v1.mp4'));
  writeFileSync(join(DIR, 'fake-words.mjs'), TRANSCRIBER);
  writeFileSync(join(DIR, 'fake-agent.mjs'), AGENT);
  writeFileSync(join(CONFIG, 'retake', 'config.toml'), `name = "${AUTHOR}"
open_browser = false
find_agents = false

[transcribe]
engine = "command"
command = "node ${join(DIR, 'fake-words.mjs')}"

[[agents]]
id = "fake"
name = "Fake agent"
command = ["node", "${join(DIR, 'fake-agent.mjs')}"]
`);
  server = await startServer('batch', join(VIDEO, 'promo_v1.mp4'), PORTS.batch, [], { env: { XDG_CONFIG_HOME: CONFIG } });
});
test.afterAll(async () => {
  await server?.stop();
});

interface Task { id: string; notes: number[]; state: string; result?: string; attempt: number }

test('Send with three comments: one task, one agent run, one new version', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(server.url);
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  const automation = async () =>
    (await (await page.request.get(new URL('api/automation', server.url).href)).json()) as { tasks: Task[]; blocked?: string };
  await expect.poll(async () => (await automation()).blocked ?? '', { timeout: 20_000 }).toBe('');

  // Three comments at three moments, each its own recording: R talks, R stops, and its words come back as a comment.
  const items = page.locator('#side .item');
  for (const [i, at] of [1, 4, 7].entries()) {
    await page.locator('video').evaluate((v: HTMLVideoElement, t: number) => { v.pause(); v.currentTime = t; }, at);
    await page.keyboard.press('r');
    await page.waitForTimeout(1800);
    await page.keyboard.press('r');
    await expect(items).toHaveCount(i + 1, { timeout: 30_000 });
  }
  await expect(page.locator('#done')).toBeEnabled();
  await page.locator('#done').click();

  // One task with all three, one launch; the column says so as one line while it works.
  await expect.poll(async () => (await automation()).tasks.map((t) => `${t.state}:${t.notes.length}`), { timeout: 60_000 })
    .toEqual(['running:3']);
  await expect(page.locator('#side .batch-line')).toHaveText('Agent working on 3 comments', { timeout: 15_000 });
  for (const row of await items.all()) await expect(row.locator('.status')).toHaveText('Working');
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, 'working.png') });
  writeFileSync(GATE, '');

  await expect.poll(async () => (await automation()).tasks.map((t) => `${t.state}:${t.result ?? ''}`), { timeout: 60_000 })
    .toEqual(['completed:v2']);
  const launches = readFileSync(LAUNCHES, 'utf8').trim().split('\n');
  expect(launches).toHaveLength(1);
  expect((JSON.parse(launches[0]!) as { notes: number[] }).notes).toHaveLength(3);
  // Exactly one new version: promo_v2.mp4, and no v3.
  expect(readdirSync(VIDEO).filter((f) => f.endsWith('.mp4')).sort()).toEqual(['promo_v1.mp4', 'promo_v2.mp4']);
  expect(existsSync(join(VIDEO, 'promo_v3.mp4'))).toBe(false);
  const notes = (await (await page.request.get(new URL('api/notes?v=v1', server.url).href)).json()) as {
    items: { id: number; status: string; answered_in?: string; change?: string }[];
  };
  expect(notes.items.map((n) => `${n.status} ${n.answered_in}`)).toEqual(Array(3).fill('answered v2'));
  expect(notes.items.map((n) => n.change)).toEqual(notes.items.map((n) => `Shortened part #${n.id}`));
  await page.screenshot({ path: join(SHOTS, 'done.png') });
  expect(errors).toEqual([]);
});
