// The agent marks its comments one by one (spec/automation.md, "Marking progress" and "The live version"): a plain
// video with three typed comments, sent with Send to a fake agent that works them one at a time (retake working, its
// video so far, retake done) and waits for the test's gate between steps. The story checks that v2 is live at once,
// that the comments move to Done one at a time with the sweep and the line on the right card, that there is no
// "Agent working on" pill, that the red edge light is on while it works and off after, that the page follows each
// revision without a reload, and that a Done card's "in v2" plays v2 at that comment's moment. Shared by
// 23-progress.spec.ts (Chromium, computer and phone) and 23-progress-webkit.spec.ts (WebKit, phone).
import { expect, type Page } from '@playwright/test';
import { linkSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { mediaLayout } from './fixtures.ts';
import { AUTHOR, REPO, RETAKE_BIN, WORK } from './env.ts';
import { READY } from './review.ts';
import { startServer, type Server } from './server.ts';

/** The fake agent: each note of its task in time order, `retake working`, the test's gate, a different take of the
 * video as its "edit" (the v5 and v6 fixtures, so each revision looks different), `retake done`; then the report. */
const AGENT = `import { copyFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
const ctx = JSON.parse(readFileSync(process.env.RETAKE_CONTEXT, 'utf8'));
const retake = (...args) => execFileSync(process.env.RETAKE_BIN, args, { stdio: ['ignore', 'pipe', 'inherit'] });
const gate = async (name) => {
  const until = Date.now() + 150000;
  while (!existsSync(process.env.GATES + '/' + name) && Date.now() < until) await new Promise((r) => setTimeout(r, 100));
};
const words = ['trimming the intro', 'slowing the mask fade', 'moving the bell earlier'];
const changes = ['Trimmed the intro to 3 s', 'Slowed the mask fade to 1.2 s', 'Moved the bell 6 frames earlier'];
const cuts = process.env.CUTS.split(':');
const notes = ctx.notes.filter((n) => ctx.task.notes.includes(n.id)).sort((a, b) => a.t0 - b.t0);
for (const [i, n] of notes.entries()) {
  retake('working', String(n.id), words[i]);
  await gate('work-' + (i + 1));
  copyFileSync(cuts[i % 2], process.env.RETAKE_OUTPUT);
  retake('done', String(n.id), changes[i]);
}
await gate('report');
writeFileSync(process.env.RETAKE_REPORT, JSON.stringify({ state: 'completed', message: 'Edited all three' }));
`;

export interface Fixture {
  server: Server;
  gates: string;
}

/** A plain project `promo` (promo_v1.mp4, the v6 fixture) in its own folder, its own config with the fake agent, and
 * a server on `port`. */
export async function startProgress(name: string, port: number): Promise<Fixture> {
  const dir = join(WORK, 'progress', name);
  rmSync(dir, { recursive: true, force: true });
  const video = join(dir, 'promo');
  const config = join(dir, 'config');
  const gates = join(dir, 'gates');
  for (const d of [video, join(config, 'retake'), gates]) mkdirSync(d, { recursive: true });
  const media = mediaLayout(WORK);
  linkSync(media.v6, join(video, 'promo_v1.mp4'));
  writeFileSync(join(dir, 'fake-agent.mjs'), AGENT);
  writeFileSync(join(config, 'retake', 'config.toml'), `name = "${AUTHOR}"
open_browser = false
find_agents = false

[[agents]]
id = "fake"
name = "Fake agent"
command = ["node", "${join(dir, 'fake-agent.mjs')}"]
env = { RETAKE_BIN = "${RETAKE_BIN}", GATES = "${gates}", CUTS = "${media.v5}:${media.v6}" }
`);
  const server = await startServer(`progress-${name}`, join(video, 'promo_v1.mp4'), port, [], {
    env: { XDG_CONFIG_HOME: config, RETAKE_ENCODER: 'x264' },
  });
  return { server, gates };
}

interface Task {
  id: string;
  state: string;
  notes: number[];
  target?: string;
  result?: string;
  working?: { note: number; text?: string } | null;
  marked?: { note: number; change: string; ready?: boolean }[] | null;
}

/** Where screenshots go: .retake-work/progress-shots/<what>-<device>.png. */
export const SHOTS = process.env.RETAKE_E2E_SHOTS ?? join(REPO, '.retake-work', 'progress-shots');

/** The story, on a page already sized for its device. `device` names its screenshots. */
export async function progressStory(page: Page, f: Fixture, device: string): Promise<void> {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(`${e.name}: ${e.message}`));
  await page.goto(f.server.url);
  await page.waitForFunction(READY, null, { timeout: 30_000 });
  await page.evaluate(() => ((window as unknown as { __same: number }).__same = 1)); // gone if the page reloads
  const automation = async () =>
    (await (await page.request.get(new URL('api/automation', f.server.url).href)).json()) as {
      tasks: Task[];
      blocked?: string;
      renders: { label: string; task?: string }[];
    };
  await expect.poll(async () => (await automation()).blocked ?? '', { timeout: 20_000 }).toBe('');

  // Three typed comments on v1, at 1 s, 4 s and 7 s.
  const ids: number[] = [];
  for (const [t, text] of [
    [1, 'The intro drags'],
    [4, 'This mask needs to fade out a little slower'],
    [7, 'The bell hits a beat late here'],
  ] as const) {
    const id = await page.evaluate(
      async ([t, text]) => {
        const r = await fetch('api/notes', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ version: 'v1', t, text }),
        });
        return ((await r.json()) as { id: number }).id;
      },
      [t, text] as const,
    );
    ids.push(id);
  }
  const items = page.locator('#side .item');
  const card = (id: number) => page.locator(`#side .item[data-key="note:${id}"]`);
  await expect(items).toHaveCount(3, { timeout: 15_000 });

  // Send: one task, and v2 is live at once (its first revision is v1 itself), so the page follows it.
  await page.locator('[data-part~="send"]:visible').first().click();
  await expect
    .poll(async () => (await automation()).tasks.map((t) => `${t.state}:${t.working?.note ?? ''}`), { timeout: 60_000 })
    .toEqual([`running:${ids[0]}`]);
  await expect.poll(() => page.evaluate(() => window.__review.view.shown), { timeout: 30_000 }).toMatch(/^v2-draft\d+$/);
  const picker = page.locator('[data-part~="version-picker"]:visible').first();
  await expect(picker).toContainText('v2');
  await expect(picker).not.toContainText('draft');

  const tabs = page.locator('#side [data-part~="tabs"] [role="tab"]');
  const counts = async () => (await tabs.allTextContents()).slice(0, 2).map((s) => s.replace(/\s+/g, ' ').trim());
  const doing = page.locator('#side .item[data-doing]');
  const edge = page.locator('[data-part~="agent-edge"]');

  // Working on the first: the sweep and the line on its card only; no pill; the edge light on.
  await expect(doing).toHaveCount(1);
  await expect(doing).toHaveAttribute('data-key', `note:${ids[0]}`);
  await expect(card(ids[0]!).locator('.doing')).toHaveText('trimming the intro…');
  await expect(page.locator('#side .batch-line')).toHaveCount(0);
  await expect(page.getByText(/Agent working on/)).toHaveCount(0);
  await expect(edge).toHaveCount(1);
  await expect(edge).toBeVisible();
  expect(await counts()).toEqual(['To do 3', 'Done 0']);
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `working-${device}.png`) });

  // One at a time: each gate lets the agent finish one comment; the counts move by one, the sweep moves on, and the
  // page follows the new revision of v2 in place.
  const shownBefore = await page.evaluate(() => window.__review.view.shown);
  for (const [i, id] of ids.entries()) {
    writeFileSync(join(f.gates, `work-${i + 1}`), '');
    await expect.poll(counts, { timeout: 30_000 }).toEqual([`To do ${2 - i}`, `Done ${i + 1}`]);
    await expect(card(id)).toHaveCount(0); // it left To do
    if (i < 2) {
      await expect(doing).toHaveAttribute('data-key', `note:${ids[i + 1]}`, { timeout: 15_000 });
      await expect(doing).toHaveCount(1);
    }
    if (i === 1) {
      await expect.poll(() => page.evaluate(() => window.__review.view.shown), { timeout: 30_000 }).not.toBe(shownBefore);
      await page.waitForTimeout(400); // the card's move settles
      await page.screenshot({ path: join(SHOTS, `mid-batch-${device}.png`) });
    }
  }
  const task = (await automation()).tasks[0]!;
  expect(task.marked?.map((m) => `${m.note}:${m.ready}`)).toEqual(ids.map((id) => `${id}:true`));
  expect((await automation()).renders.filter((r) => r.task === task.id).map((r) => r.label)).toEqual([
    'v2-draft1',
    'v2-draft2',
    'v2-draft3',
    'v2-draft4',
  ]);
  await expect(edge).toHaveCount(1); // still working: the report isn't in

  // Done: each with its change and "in v2", which plays v2 at that comment's moment.
  await page.locator('#side [role="tab"]', { hasText: 'Done' }).click();
  await expect(items).toHaveCount(3);
  await expect(card(ids[1]!).locator('.change')).toHaveText('Slowed the mask fade to 1.2 s');
  const into = card(ids[1]!).locator('[data-part~="into"]');
  await expect(into).toHaveText('in v2');
  await page.evaluate(() => {
    const v = document.querySelector('video')!;
    v.pause();
    v.currentTime = 20;
  });
  await into.click();
  await expect.poll(() => page.evaluate(() => document.querySelector('video')!.currentTime), { timeout: 10_000 }).toBeCloseTo(4, 0);
  expect(await page.evaluate(() => window.__review.view.shown)).toMatch(/^v2-draft\d+$/);

  // The report: v2 is final, the edge light goes.
  writeFileSync(join(f.gates, 'report'), '');
  await expect.poll(async () => (await automation()).tasks.map((t) => `${t.state}:${t.result ?? ''}`), { timeout: 60_000 })
    .toEqual(['completed:v2']);
  await expect(edge).toHaveCount(0, { timeout: 15_000 });
  await expect.poll(() => page.evaluate(() => window.__review.view.shown), { timeout: 30_000 }).toBe('v2');
  expect(await page.evaluate(() => (window as unknown as { __same?: number }).__same)).toBe(1); // never reloaded
  const notes = (await (await page.request.get(new URL('api/notes?v=v1', f.server.url).href)).json()) as {
    items: { id: number; status: string; answered_in?: string; change?: string }[];
  };
  expect(notes.items.map((n) => `${n.status} ${n.answered_in} ${n.change}`)).toEqual([
    'answered v2 Trimmed the intro to 3 s',
    'answered v2 Slowed the mask fade to 1.2 s',
    'answered v2 Moved the bell 6 frames earlier',
  ]);
  expect(errors).toEqual([]);
}
