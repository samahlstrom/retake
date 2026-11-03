// New (spec/notes.md, "Typed comments on a video", and spec/automation.md, "HTTP and UI"): draw, type or record a
// comment at any moment. `retake open <folder>` with its own config folder: a video with v5 and v6, two fake agents
// whose program is `true` (a send runs it; it reports nothing). On the review page (desktop, Chromium):
// - a typed comment with a drawing and no recording: the video pauses, the line goes with the comment, Enter saves it
//   as a note with its time, its line and a still; Esc cancels one with its lines; Z undoes its last line;
// - the pen: one button that opens the colours and the eraser, its icon the colour (the eraser while that's on);
// - recording: REC and the time, nothing in the bar moves, and the pen draws into the take;
// - the comments' header: the version menu ("v6 → v7 · 1 to do") switches versions;
// - Send says Send, never an agent; held, it lists the agents (two here) and the pick goes with that send; with one
//   agent (a second server) holding it is a plain Send;
// - a list of 30 comments scrolls inside the comments column.
// Screenshots (RETAKE_E2E_SHOTS, else .retake-work/review2-shots): idle, recording, the pen's popover, a typed comment
// with a drawing, the version menu and the home's settings.
import { expect, test, type Browser, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { AUTHOR, LEGACY, PORTS, REPO, WORK } from '../lib/env.ts';
import { line, loop, Review, sleep } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player draws only while recording');

const SHOTS = process.env.RETAKE_E2E_SHOTS ?? join(REPO, '.retake-work', 'review2-shots');

/** A folder with one video (alpha: v5 and v6) and a config folder with these agents. */
function setup(name: string, agents: string[]): { dir: string; config: string } {
  const dir = join(WORK, `${name}-folder`);
  const config = join(WORK, `${name}-config`);
  const m = mediaLayout(WORK);
  for (const [v, src] of [['v5', m.v5], ['v6', m.v6]] as const) {
    const to = join(dir, 'alpha', v, `alpha_${v}.mp4`);
    mkdirSync(dirname(to), { recursive: true });
    copyFileSync(src, to);
  }
  const file = join(config, 'retake', 'config.toml');
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, `name = "${AUTHOR}"
open_browser = false
find_agents = false
${agents.map((id) => `
[[agents]]
id = "${id}"
name = "Agent ${id[0]!.toUpperCase()}${id.slice(1)}"
command = ["true"]
`).join('')}`);
  return { dir, config };
}

let server: Server;
let page: Page;
let ui: Review;
const pageErrors: string[] = [];

interface Item { id: number; key?: string; t0: number; text: string; version?: string; status: string;
  strokes?: { color: string; points: number[][]; t: number; shape: string }[]; snapshot?: string; wants?: string }

async function items(base: string): Promise<Item[]> {
  const r = await fetch(`${base}p/alpha/api/notes?v=v6&scope=video`);
  return ((await r.json()) as { items: Item[] }).items;
}
interface Automation { workers: { agents: { id: string }[] }[]; tasks: { agent: string; notes: number[] }[] }
async function automation(base: string): Promise<Automation> {
  return (await (await fetch(`${base}p/alpha/api/automation`)).json()) as Automation;
}
const draft = () => page.evaluate(() => window.__review.draft ?? null);
const box = (sel: string) => page.locator(sel).boundingBox();

async function shot(name: string): Promise<void> {
  mkdirSync(SHOTS, { recursive: true });
  await sleep(300);
  await page.screenshot({ path: join(SHOTS, `${name}-desktop.png`) });
}

async function openReview(browser: Browser, url: string): Promise<void> {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await context.grantPermissions(['microphone'], { origin: url.replace(/\/$/, '') });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  ui = new Review(page);
  await ui.open(`${url}p/alpha/`);
  await expect(page.locator('#side')).toBeVisible();
}

test.beforeAll(async ({ browser }) => {
  const { dir, config } = setup('comment', ['one', 'two']);
  server = await startServer('comment', dir, PORTS.scenario, [], { env: { XDG_CONFIG_HOME: config } });
  await openReview(browser, server.url);
});

test.afterAll(async () => {
  await page?.context().close();
  await server?.stop();
});

test('idle: a calm bar (play, time, timeline, version, pen, Record, Send, ?) that never names an agent', async () => {
  await expect.poll(async () => (await automation(server.url)).workers[0]?.agents.length ?? 0, { timeout: 20_000 }).toBe(2);
  for (const sel of ['#play', '#time', '#timeline', '#verBtn', '#pen', '#rec', '#done', '#keys']) {
    await expect(page.locator(`#bar ${sel}`), sel).toBeVisible();
  }
  await expect(page.locator('#done')).toHaveText('Send');
  await expect(page.locator('#bar')).not.toContainText('Agent');
  await expect(page.locator('#bar [data-part~="agent-status"], #agent')).toHaveCount(0);
  await expect(page.locator('#rec')).toHaveAttribute('aria-label', 'Record voice (R)');
  await expect(page.locator('#rec .mic .recdot')).toBeVisible();
  // The comments' header: one row, To do · Done, search and hide; no bubble and no version (the bar has the one menu).
  await expect(page.locator('#side header')).not.toContainText('Comments');
  await expect(page.locator('#side header .seg button')).toHaveText(['To do 0', 'Done 0']);
  await expect(page.locator('#side [data-part~="version-picker"]')).toHaveCount(0);
  await expect(page.locator('#bar [data-part~="version-picker"]')).toHaveText('v6');
  await expect(page.locator('#side [data-part~="comment-line"] textarea')).toHaveAttribute('placeholder',
    /^Comment at 0:00 · goes into v7…$/);
  await shot('idle');
});

test('the pen: one button opens the colours and the eraser; its icon is the colour, or the eraser', async () => {
  await expect(page.locator('#pen')).toHaveAttribute('data-color', 'red');
  await page.locator('#pen').click();
  const pop = page.locator('.pen-pop');
  await expect(pop).toBeVisible();
  await expect(pop.locator('.sw[data-color]')).toHaveCount(4);
  await expect(pop.locator('#eraser')).toBeVisible();
  await shot('pen-popover');
  await pop.locator('.sw[data-color=yellow]').click();
  await expect(pop).toHaveCount(0); // a pick closes it
  await expect(page.locator('#pen')).toHaveAttribute('data-color', 'yellow');
  await ui.key('e');
  await expect(page.locator('#pen')).toHaveAttribute('data-color', 'eraser');
  await expect(page.locator('#pen')).toHaveAttribute('aria-label', /^Eraser/);
  await ui.pen('green');
  await expect(page.locator('#pen')).toHaveAttribute('data-color', 'green');
  await ui.key('1');
  await expect(page.locator('#pen')).toHaveAttribute('data-color', 'red');
  // No colour dots in the bar itself.
  await expect(page.locator('#bar .sw')).toHaveCount(0);
});

test('a typed comment with a drawing, no recording: the video pauses, Enter saves its time, line and still', async () => {
  // While playing, a line on the video pauses it and starts a comment there; no take, no recording.
  await ui.key(' ');
  await sleep(1500);
  await ui.drag(loop(0.4, 0.45, 0.06));
  const s = await ui.state();
  expect(s).toMatchObject({ paused: true, rec: false, take: null, strokes: 0 });
  const d = (await draft())!;
  expect(d).toMatchObject({ version: 'v6', lines: 1, text: '' });
  expect(d.t).toBeGreaterThan(1);
  const box1 = page.locator('#side [data-part~="comment-line"]');
  await expect(box1.locator('.under')).toContainText('1 drawing');
  // Z undoes its last line; a second line, then words.
  await ui.drag(line([0.6, 0.3], [0.8, 0.35]));
  expect((await draft())!.lines).toBe(2);
  await ui.key('z');
  expect((await draft())!.lines).toBe(1);
  // The playhead moves freely: the comment stays at its line's moment.
  await page.locator('#timeline').click({ position: { x: 300, y: 20 } });
  await sleep(300);
  expect((await draft())!.t).toBeCloseTo(d.t, 2);
  await page.evaluate(async (t) => {
    const v = document.querySelector('video')!;
    const done = new Promise((r) => v.addEventListener('seeked', r, { once: true }));
    v.currentTime = t;
    await done;
  }, d.t);
  const text = box1.locator('textarea');
  await text.click();
  await text.pressSequentially('Hold this shot longer');
  await expect(box1).toHaveClass(/pending/);
  await shot('typed-comment');
  await text.press('Enter');
  await expect.poll(async () => (await items(server.url)).length, { timeout: 10_000 }).toBe(1);
  const [it] = await items(server.url);
  expect(it).toMatchObject({ key: 'doc/1', text: 'Hold this shot longer', version: 'v6', status: 'open', wants: 'version' });
  expect(it!.t0).toBeCloseTo(d.t, 2);
  expect(it!.strokes).toHaveLength(1);
  expect(it!.strokes![0]).toMatchObject({ color: 'red' });
  expect(it!.snapshot, 'a still of its moment').toBeTruthy();
  const still = await fetch(new URL(it!.snapshot!, `${server.url}p/alpha/`));
  expect(still.status).toBe(200);
  expect(still.headers.get('content-type')).toBe('image/jpeg');
  expect(await draft()).toBeNull();
  expect((await ui.state()).take, 'no take: nothing was recorded').toBeNull();
  // It's a comment like any other: its card, its drawing, where its edit goes.
  const card = page.locator('#side [data-part~="comment"]');
  await expect(card).toHaveCount(1);
  await expect(card).toContainText('Hold this shot longer');
  await expect(card.locator('[data-part~="status"]')).toHaveText('Open');
  await expect(card.locator('[data-part~="into"]')).toHaveText('→ v7');
  await expect(card.locator('[data-part~="sketch"] path')).toHaveCount(1);
  await expect(page.locator('#side .tabs .seg button').first()).toHaveText('To do 1');
});

test('Esc cancels a comment with its lines; a drawing alone saves with Enter', async () => {
  await ui.drag(loop(0.7, 0.6));
  expect((await draft())!.lines).toBe(1);
  await ui.key('Escape');
  expect(await draft()).toBeNull();
  await ui.drag(loop(0.2, 0.3));
  await ui.key('Enter'); // the box takes the focus
  await expect(page.locator('#side [data-part~="comment-line"] textarea')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect.poll(async () => (await items(server.url)).length).toBe(2);
  const drawing = (await items(server.url)).find((x) => x.key === 'doc/2')!;
  expect(drawing).toMatchObject({ text: '' });
  expect(drawing.strokes).toHaveLength(1);
  await expect(page.locator('#side [data-part~="comment"]', { hasText: 'Drawing' })).toHaveCount(1);
});

test('recording: REC and the time, nothing in the bar moves, and the pen draws into the take', async () => {
  const parts = ['#timeline', '#verBtn', '#pen', '#rec', '#done', '#keys'];
  const before = await Promise.all(parts.map(box));
  await ui.key('r');
  await ui.waitFor(() => !!window.__review.seg, 8000);
  await expect(page.locator('#recLabel')).toHaveText(/^REC \d:\d\d$/);
  await expect(page.locator('#rec')).toHaveAttribute('aria-label', 'Stop recording (R)');
  await expect(page.locator('#bar')).toHaveClass(/recording/);
  expect(await Promise.all(parts.map(box)), 'no layout shift').toEqual(before);
  await ui.drag(loop(0.5, 0.5));
  const s = await ui.state();
  expect(s.strokes).toBe(1);
  expect(await draft(), 'recording: the line is the take’s').toBeNull();
  await shot('recording');
  await ui.key('r');
  await ui.waitFor(() => !window.__review.seg, 5000);
  expect(await Promise.all(parts.map(box))).toEqual(before);
});

test('the version menu: one row per version, one detail each, and a pick switches the comments', async () => {
  const menu = page.locator('#bar [data-part~="version-picker"]');
  await expect(menu).toHaveText('v6');
  await menu.click();
  const list = page.locator('[data-part~="version-list"]');
  await expect(list).toBeVisible();
  await expect(list.locator('[data-version]')).toHaveCount(2);
  await expect(list.locator('[data-version="v6"] .name')).toHaveText('v6'); // never "→ v7" or "Latest"
  await expect(list.locator('[data-version="v6"] .d')).toHaveText(/^\d+ to do$/);
  await expect(list.locator('[data-version="v6"]')).toHaveAttribute('aria-current', 'true');
  await expect(list.locator('[data-version="v5"] .d')).toHaveText('0:40');
  await shot('version-menu');
  await list.locator('[data-version="v5"]').click();
  await expect.poll(async () => (await ui.state()).shown).toBe('v5');
  await expect(menu).toHaveText('v5');
  await expect(page.locator('#side [data-part~="comment-line"] textarea')).toHaveAttribute('placeholder', /goes into v6…$/);
  await expect(page.locator('#side .tabs .seg button').first()).toHaveText('To do 0');
  await menu.click();
  await page.locator('[data-part~="version-list"] [data-version="v6"]').click();
  await expect.poll(async () => (await ui.state()).shown).toBe('v6');
});

test('Send held with two agents lists them; the pick goes with that send', async () => {
  const send = page.locator('#done');
  await expect(send).toHaveText('Send');
  await expect(send).toHaveAttribute('aria-label', /^Send \d+ comments to the agent$/);
  await expect(send).toHaveAttribute('title', /Hold to choose the agent/);
  const b = (await send.boundingBox())!;
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
  await page.mouse.down();
  await sleep(800);
  await page.mouse.up();
  const picker = page.locator('[data-part~="agent-picker"]');
  await expect(picker).toBeVisible();
  await expect(picker.locator('[data-agent]')).toHaveText([/Agent One/, /Agent Two/]);
  expect((await automation(server.url)).tasks, 'holding it sent nothing').toHaveLength(0);
  await shot('send-picker');
  await picker.locator('[data-agent="two"]').click();
  await expect.poll(async () => (await automation(server.url)).tasks.length, { timeout: 10_000 }).toBeGreaterThan(0);
  const tasks = (await automation(server.url)).tasks;
  expect(new Set(tasks.map((t) => t.agent))).toEqual(new Set(['two']));
  await expect(page.locator('#toast')).toContainText('Sent to v7.');
  expect(pageErrors).toEqual([]);
});

test('Send held with one agent is a plain Send', async ({ browser }) => {
  const { dir, config } = setup('comment1', ['solo']);
  const one = await startServer('comment1', dir, PORTS.crash, [], { env: { XDG_CONFIG_HOME: config } });
  const main = page;
  try {
    await openReview(browser, one.url);
    await expect.poll(async () => (await automation(one.url)).workers[0]?.agents.length ?? 0, { timeout: 20_000 }).toBe(1);
    const r = await fetch(`${one.url}p/alpha/api/notes`, { method: 'POST', body: JSON.stringify({ version: 'v6', t: 3, text: 'Brighter' }) });
    expect(r.status).toBe(201);
    await expect(page.locator('#done')).toHaveAttribute('aria-label', 'Send 1 comment to the agent', { timeout: 10_000 });
    await expect(page.locator('#done')).not.toHaveAttribute('title', /Hold/);
    const b = (await page.locator('#done').boundingBox())!;
    await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
    await page.mouse.down();
    await sleep(800);
    await page.mouse.up();
    await expect(page.locator('[data-part~="agent-picker"]')).toHaveCount(0);
    await expect.poll(async () => (await automation(one.url)).tasks.map((t) => t.agent), { timeout: 10_000 }).toEqual(['solo']);
    expect(pageErrors).toEqual([]);
  } finally {
    await page.context().close();
    page = main;
    await one.stop();
  }
});

test('a long list scrolls inside the comments column', async ({ browser }) => {
  const { dir, config } = setup('comment-long', ['solo']);
  const long = await startServer('comment-long', dir, PORTS.crash, [], { env: { XDG_CONFIG_HOME: config } });
  const main = page;
  try {
    for (let i = 0; i < 30; i++) {
      const r = await fetch(`${long.url}p/alpha/api/notes`, { method: 'POST', body: JSON.stringify({ version: 'v6', t: 1 + i, text: `Comment ${i + 1}` }) });
      expect(r.status).toBe(201);
    }
    await openReview(browser, long.url);
    await expect(page.locator('#side .item')).toHaveCount(30, { timeout: 10_000 });
    const side = (await box('#side'))!;
    const vp = await page.locator('#side .vp').evaluate((el) => ({ scroll: el.scrollHeight, client: el.clientHeight }));
    expect(vp.client, 'the list is no taller than the column').toBeLessThan(side.height);
    expect(vp.scroll, 'and scrolls').toBeGreaterThan(vp.client);
    const last = page.locator('#side .item').last();
    await last.scrollIntoViewIfNeeded();
    await expect(last).toBeInViewport();
    const lb = (await last.boundingBox())!;
    expect(lb.y + lb.height, 'the last comment shows inside the column').toBeLessThanOrEqual(side.y + side.height + 0.5);
    expect(pageErrors).toEqual([]);
  } finally {
    await page.context().close();
    page = main;
    await long.stop();
  }
});

test('the home: Settings in the left column (screenshot)', async () => {
  await page.goto(`${server.url}#settings`);
  await page.waitForFunction(() => window.__home?.loaded, null, { timeout: 20_000, polling: 80 });
  await expect(page.locator('[data-part~="settings"] h1')).toHaveText('Settings');
  await shot('settings');
});
