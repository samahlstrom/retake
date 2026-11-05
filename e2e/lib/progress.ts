// The agent marks its comments one by one (spec/automation.md, "Marking progress", "While the agent works" and "The
// live version"): a plain video with fifteen typed comments, sent with Send to a fake agent that waits for the test's
// gate before its first mark, then works three of them out of time order (retake working, its video so far, retake
// done), a gate between steps, and leaves the other twelve to its report. The story checks that v2 is live at once
// and lists the batch under "From v1" (a comment on v2 itself apart, under "On v2"); that before the first mark the
// only sign is one quiet "The agent is on it · 15 comments" line, with no sweep; that then the comment the agent is
// on is first in To do with the Working dot, the sweep and its line, while the rest of the batch read quietly; that
// they move to Done one at a time; that there is no "Agent working on" pill and no light around the video; that the
// page stays on v1 until it deliberately chooses v2, then follows v2's own revisions without a reload; and that a
// Done card's "in v2" plays v2 at that comment's moment.
// Shared by 23-progress.spec.ts (Chromium, computer and phone) and 23-progress-webkit.spec.ts (WebKit, phone).
import { expect, type Page } from '@playwright/test';
import { linkSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { mediaLayout } from './fixtures.ts';
import { AUTHOR, REPO, RETAKE_BIN, WORK } from './env.ts';
import { READY } from './review.ts';
import { startServer, type Server } from './server.ts';

/** The order the fake agent works the notes in, as indexes into them in time order: the fifth first, so the comment
 * it's on has to move up to be first. */
const ORDER = [4, 0, 1];
/** What the agent says it's doing on each, and what it changed. */
const WORDS = ['drawing the okay hand in ink', 'trimming the intro', 'slowing the mask fade'];
const CHANGES = ['Drew the okay hand in the ink style', 'Trimmed the intro to 3 s', 'Slowed the mask fade to 1.2 s'];

/** The fake agent: the test's `start` gate; then for the notes of ORDER, `retake working`, the test's gate, a
 * different take of the video as its "edit" (the v5 and v6 fixtures, so each revision looks different), `retake
 * done`; then the report, which finishes the rest. */
const AGENT = `import { copyFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
const ctx = JSON.parse(readFileSync(process.env.RETAKE_CONTEXT, 'utf8'));
const retake = (...args) => execFileSync(process.env.RETAKE_BIN, args, { stdio: ['ignore', 'pipe', 'inherit'] });
const gate = async (name) => {
  const until = Date.now() + 150000;
  while (!existsSync(process.env.GATES + '/' + name) && Date.now() < until) await new Promise((r) => setTimeout(r, 100));
};
const words = ${JSON.stringify(WORDS)};
const changes = ${JSON.stringify(CHANGES)};
const cuts = process.env.CUTS.split(':');
const notes = ctx.notes.filter((n) => ctx.task.notes.includes(n.id)).sort((a, b) => a.t0 - b.t0);
await gate('start');
for (const [i, at] of ${JSON.stringify(ORDER)}.entries()) {
  const n = notes[at];
  retake('working', String(n.id), words[i]);
  await gate('work-' + (i + 1));
  copyFileSync(cuts[i % 2], process.env.RETAKE_OUTPUT);
  retake('done', String(n.id), changes[i]);
}
await gate('report');
writeFileSync(process.env.RETAKE_REPORT, JSON.stringify({ state: 'completed', message: 'Edited the rest' }));
`;

/** Fifteen comments a person might leave on a 40 s promo, in time order. */
const NOTES: [number, string][] = [
  [1, 'The intro drags'],
  [3.5, 'This mask needs to fade out a little slower'],
  [6, 'The bell hits a beat late here'],
  [8.5, 'Logo is too small, make it bigger and keep it centered'],
  [11, 'Okay hand symbol in the style of the ink scenes'],
  [13.5, 'Cut the pause before the line'],
  [16, 'Music is too loud under the voice'],
  [18.5, 'Hold this shot a second longer'],
  [21, 'Color is too warm compared to the shot before'],
  [23.5, 'Title comes in too late'],
  [26, 'Swap these two shots'],
  [28.5, 'The zoom feels jumpy, ease it in'],
  [31, 'Add a beat of silence before the drop'],
  [33.5, 'Lower third typo: "recieve"'],
  [36, 'End card should stay up two seconds'],
];

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
  const comment = (version: string, t: number, text: string) =>
    page.evaluate(
      async ([version, t, text]) => {
        const r = await fetch('api/notes', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ version, t, text }),
        });
        if (!r.ok) throw new Error(`POST api/notes: ${r.status} ${await r.text()}`);
        return ((await r.json()) as { id: number }).id;
      },
      [version, t, text] as const,
    );

  // Fifteen typed comments on v1.
  const ids: number[] = [];
  for (const [t, text] of NOTES) ids.push(await comment('v1', t, text));
  const items = page.locator('#side .item');
  const card = (id: number) => page.locator(`#side .item[data-key="note:${id}"]`);
  const keys = async () => (await items.evaluateAll((els) => els.map((e) => e.getAttribute('data-key'))));
  await expect(items).toHaveCount(15, { timeout: 15_000 });

  // Send: one task and v2 appears at once, but the reviewer stays on v1 until deliberately choosing v2. The agent
  // hasn't marked anything yet (it waits for the `start` gate).
  await page.locator('[data-part~="send"]:visible').first().click();
  await expect
    .poll(async () => (await automation()).tasks.map((t) => `${t.state}:${t.working?.note ?? ''}`), { timeout: 60_000 })
    .toEqual(['running:']);
  await expect.poll(() => page.evaluate(() => window.__review.view.shown), { timeout: 30_000 }).toBe('v1');
  const picker = page.locator('[data-part~="version-picker"]:visible').first();
  await expect(picker).toContainText('v1');
  await picker.click();
  await page.locator('[data-part~="version-list"] [data-version^="v2"]').click();
  await expect.poll(() => page.evaluate(() => window.__review.view.shown), { timeout: 30_000 }).toMatch(/^v2-draft\d+$/);

  const tabs = page.locator('#side [data-part~="tabs"] [role="tab"]');
  const counts = async () => (await tabs.allTextContents()).slice(0, 2).map((s) => s.replace(/\s+/g, ' ').trim());
  const doing = page.locator('#side .item[data-doing]');
  const onIt = page.locator('#side [data-part~="agent-on-it"]');
  const groups = page.locator('#side [data-part~="comment-group"]');
  const quiet = page.locator('#side .item .status.quiet');
  const noPill = async () => {
    await expect(page.locator('#side .batch-line')).toHaveCount(0);
    await expect(page.getByText(/Agent working on/)).toHaveCount(0);
    await expect(page.locator('#agentEdge, [data-part~="agent-edge"]')).toHaveCount(0); // no light around the video
  };

  // Before the first mark: one quiet line at the top, no sweep, every card of the batch quiet ("→ v2", no dot), all
  // of them under "From v1" on the deliberately chosen v2.
  await expect(onIt).toHaveText('The agent is on it · 15 comments');
  await expect(doing).toHaveCount(0);
  await expect(quiet).toHaveCount(15);
  await expect(page.locator('#side .item .status .dot')).toHaveCount(0);
  await expect(card(ids[0]!).locator('[data-part~="into"]')).toHaveText('→ v2');
  expect(await groups.allTextContents()).toEqual(['From v1']);
  expect(await page.locator('#side ol > li').first().getAttribute('data-part')).toBe('agent-on-it');
  await noPill();
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `on-it-${device}.png`) });

  // A comment on the live v2 itself goes into v3, apart from the batch, under "On v2".
  const shown = (await page.evaluate(() => window.__review.view.shown))!;
  const own = await comment(shown, 2, 'Logo needs to be bigger');
  await expect(card(own)).toHaveCount(1, { timeout: 15_000 });
  expect(await groups.allTextContents()).toEqual(['From v1', 'On v2']);
  await expect(card(own).locator('[data-part~="status"]')).toHaveText('Open');
  await expect(card(own).locator('[data-part~="into"]')).toHaveText('→ v3');
  expect((await keys()).at(-1)).toBe(`note:${own}`);
  expect(await counts()).toEqual(['To do 16', 'Done 0']);

  // The agent starts on the fifth comment: the line goes, and that comment is first, with the dot, the sweep and its
  // words; the rest of the batch stay quiet, in time order.
  writeFileSync(join(f.gates, 'start'), '');
  await expect(onIt).toHaveCount(0, { timeout: 15_000 });
  const on = ids[ORDER[0]!]!;
  await expect(doing).toHaveCount(1);
  await expect(doing).toHaveAttribute('data-key', `note:${on}`);
  await expect(card(on).locator('.doing')).toHaveText(`${WORDS[0]}…`);
  await expect(card(on).locator('[data-part~="status"]')).toHaveText('Working');
  await expect(card(on).locator('.status .dot')).toHaveCount(1);
  await expect(quiet).toHaveCount(14);
  await expect.poll(keys).toEqual([on, ...ids.filter((id) => id !== on), own].map((id) => `note:${id}`));
  await noPill();
  await page.waitForTimeout(400); // the card's move settles
  await page.screenshot({ path: join(SHOTS, `working-${device}.png`) });

  // One at a time: each gate lets the agent finish one comment; the counts move by one, the next one it's on moves
  // to the top, and the explicitly chosen v2 follows its own revision in place.
  const shownBefore = await page.evaluate(() => window.__review.view.shown);
  for (const [i, at] of ORDER.entries()) {
    writeFileSync(join(f.gates, `work-${i + 1}`), '');
    await expect.poll(counts, { timeout: 30_000 }).toEqual([`To do ${15 - i}`, `Done ${i + 1}`]);
    await expect(card(ids[at]!)).toHaveCount(0); // it left To do
    const next = ORDER[i + 1];
    if (next != null) {
      await expect(doing).toHaveAttribute('data-key', `note:${ids[next]}`, { timeout: 15_000 });
      await expect(doing).toHaveCount(1);
      await expect.poll(async () => (await keys())[0]).toBe(`note:${ids[next]}`);
    }
    if (i === 1) {
      await expect.poll(() => page.evaluate(() => window.__review.view.shown), { timeout: 30_000 }).not.toBe(shownBefore);
      await page.waitForTimeout(400); // the card's move settles
      await page.screenshot({ path: join(SHOTS, `mid-batch-${device}.png`) });
    }
  }
  const task = (await automation()).tasks[0]!;
  expect(task.marked?.map((m) => `${m.note}:${m.ready}`)).toEqual(ORDER.map((at) => `${ids[at]}:true`));
  expect((await automation()).renders.filter((r) => r.task === task.id).map((r) => r.label)).toEqual([
    'v2-draft1',
    'v2-draft2',
    'v2-draft3',
    'v2-draft4',
  ]);
  // Nothing is on now (the agent waits for the report's gate): nothing sweeps, and the line doesn't come back.
  await expect(doing).toHaveCount(0);
  await expect(onIt).toHaveCount(0);
  await expect(quiet).toHaveCount(12);

  // Done: each with its change and "in v2", which plays v2 at that comment's moment.
  await page.locator('#side [role="tab"]', { hasText: 'Done' }).click();
  await expect(items).toHaveCount(3);
  expect(await groups.allTextContents()).toEqual(['From v1']);
  for (const [i, at] of ORDER.entries()) await expect(card(ids[at]!).locator('.change')).toHaveText(CHANGES[i]!);
  const into = card(ids[1]!).locator('[data-part~="into"]');
  await expect(into).toHaveText('in v2');
  await page.evaluate(() => {
    const v = document.querySelector('video')!;
    v.pause();
    v.currentTime = 20;
  });
  await into.click();
  await expect
    .poll(() => page.evaluate(() => document.querySelector('video')!.currentTime), { timeout: 10_000 })
    .toBeCloseTo(NOTES[1]![0], 0);
  expect(await page.evaluate(() => window.__review.view.shown)).toMatch(/^v2-draft\d+$/);

  // The report: v2 is final and finishes the twelve the agent never marked.
  writeFileSync(join(f.gates, 'report'), '');
  await expect.poll(async () => (await automation()).tasks.map((t) => `${t.state}:${t.result ?? ''}`), { timeout: 60_000 })
    .toEqual(['completed:v2']);
  await expect.poll(() => page.evaluate(() => window.__review.view.shown), { timeout: 30_000 }).toBe('v2');
  // v2 isn't live any more: its own comment alone, no labels.
  await page.locator('#side [role="tab"]', { hasText: 'To do' }).click();
  await expect.poll(keys, { timeout: 15_000 }).toEqual([`note:${own}`]);
  await expect(groups).toHaveCount(0);
  await noPill();
  expect(await page.evaluate(() => (window as unknown as { __same?: number }).__same)).toBe(1); // never reloaded
  const notes = (await (await page.request.get(new URL('api/notes?v=v1', f.server.url).href)).json()) as {
    items: { id: number; status: string; answered_in?: string; change?: string }[];
  };
  const byId = new Map(notes.items.map((n) => [n.id, n]));
  expect(ids.map((id) => `${byId.get(id)?.status} ${byId.get(id)?.answered_in}`)).toEqual(ids.map(() => 'answered v2'));
  for (const [i, at] of ORDER.entries()) expect(byId.get(ids[at]!)?.change).toBe(CHANGES[i]);
  expect(errors).toEqual([]);
}
