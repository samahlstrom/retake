// Milestone 2 on the page: the notes sidebar, answering the agent's questions, and image drops (spec/notes.md).
//
// The page runs against the real server (RETAKE_SERVER=new). The sidebar's content needs an agent (questions, answered
// notes), so GET /api/notes, POST /api/notes/<id>/reopen and the SSE stream GET /api/events are always mocked here
// with page.route. POST /image and GET /api/takes/... go to the server when it has them (probed below, without side
// effects), else they are mocked too, and the test checks the requests instead of the disk. POST /done always goes to
// the server (the page ignores its answer), and the test checks that the page sent it.
//
// Family: v5 (older) and v6 (newest, opened). Screenshots go to RETAKE_E2E_SHOTS when it is set.
import { expect, test, type BrowserContext, type Page, type Request, type Route } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { AUTHOR, LEGACY, PORTS, WORK } from '../lib/env.ts';
import { run } from '../lib/proc.ts';
import { line, READY, Review, sleep, type SidebarRow } from '../lib/review.ts';
import { assertValid, validateTakeDir } from '../lib/schema.ts';
import { startServer, type Server } from '../lib/server.ts';
import { family, takeDirs, type Family } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new in milestone 2: the legacy player has no sidebar or images');

const SHOTS = process.env.RETAKE_E2E_SHOTS;
const PHONE = { width: 390, height: 844 };

let fam: Family;
let server: Server;
let context: BrowserContext;
let page: Page;
let ui: Review;
let png: Buffer;
const pageErrors: string[] = [];
/** Which milestone-2 routes the server under test has (the rest are mocked). */
const has = { image: false, takes: false };

// ------------------------------------------------------------------ the mocked notes routes
type Item = Record<string, unknown> & { id: number; kind: 'note' | 'question'; status: string; t0: number };
const NEWEST: Item[] = [
  { id: 101, kind: 'question', status: 'question', t0: 4, t1: 4, by: 'agent', note: 2,
    text: 'Keep the zoom on the shop, or cut to the wide shot?', options: ['v5', 'v6'] },
  { id: 102, kind: 'question', status: 'question', t0: 12.5, t1: 12.5, by: 'agent',
    text: 'Is the music still too loud under the voice here?' },
  { id: 103, kind: 'question', status: 'retracted', t0: 20, t1: 20, by: 'agent', text: 'Answered already (hidden)' },
  { id: 7, kind: 'note', status: 'retracted', t0: 25, t1: 26, by: AUTHOR, text: 'Scrap that (hidden)' },
];
const OLDER: Item[] = [
  { id: 1, kind: 'note', status: 'answered', t0: 3, t1: 5.5, by: AUTHOR, text: 'The title comes in too late',
    change: 'Title now lands on the first beat', answered_in: 'v6' },
  { id: 2, kind: 'note', status: 'question', t0: 4, t1: 6, by: AUTHOR, text: 'That zoom feels off',
    question: 'Keep the zoom on the shop, or cut to the wide shot?', options: ['v5', 'v6'] },
  { id: 3, kind: 'note', status: 'retracted', t0: 8, t1: 9, by: AUTHOR, text: 'Scrap that (hidden)' },
  { id: 4, kind: 'note', status: 'accepted', t0: 15, t1: 17, by: AUTHOR, text: 'Cut the long pause before the reveal',
    change: 'Pause trimmed to half a second', answered_in: 'v6' },
];
const notes: Record<string, Item[]> = { v6: NEWEST, v5: OLDER };
const reopened: number[] = [];
const sse: string[] = [];
let seq = 0;
const push = (event: string, data: unknown) => sse.push(`id: ${++seq}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`);

/** Captured requests to routes the server may not have. */
const posts: { done: string[]; images: { url: string; type: string; size: number }[]; data: Record<string, unknown>[] } =
  { done: [], images: [], data: [] };
const mockedImages = new Map<string, Buffer>();

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

async function mockRoutes(p: Page): Promise<void> {
  await p.route((u) => u.pathname === '/api/notes', (route) => {
    const v = new URL(route.request().url()).searchParams.get('v') ?? '';
    const items = notes[v];
    return items ? json(route, 200, { version: v, newest: v === 'v6', scope:'video', items }) : json(route, 400, { error: 'bad version' });
  });
  await p.route((u) => /^\/api\/notes\/\d+\/reopen$/.test(u.pathname), (route) => {
    reopened.push(Number(new URL(route.request().url()).pathname.split('/')[3]));
    return json(route, 200, { ok: true });
  });
  // SSE: each connection gets the queued events, then ends; the page's EventSource reconnects after `retry`.
  await p.route((u) => u.pathname === '/api/events', (route) => route.fulfill({
    status: 200,
    headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-store' },
    body: `retry: 400\n\n${sse.splice(0).join('')}`,
  }));
  if (!has.image) {
    await p.route((u) => /^\/api\/session\/[^/]+\/image$/.test(u.pathname), (route) => {
      const u = new URL(route.request().url());
      mockedImages.set(`${u.pathname.split('/')[3]}/${u.searchParams.get('name')}`, route.request().postDataBuffer()!);
      return json(route, 200, { ok: true });
    });
  }
  if (!has.takes) {
    await p.route((u) => u.pathname.startsWith('/api/takes/'), (route) =>
      route.fulfill({ status: 200, contentType: 'image/png', body: png }));
  }
}

function capture(req: Request): void {
  const u = new URL(req.url());
  if (req.method() !== 'POST') return;
  if (/\/done$/.test(u.pathname)) posts.done.push(u.pathname + u.search);
  if (/\/image$/.test(u.pathname)) {
    posts.images.push({ url: u.pathname + u.search, type: req.headers()['content-type'] ?? '',
      size: req.postDataBuffer()?.length ?? 0 });
  }
  if (/\/data$/.test(u.pathname)) posts.data.push(JSON.parse(req.postData() ?? '{}') as Record<string, unknown>);
}

/** Probe a route with a request it must refuse (a bad name, a take that isn't there): a server that has the route
 * refuses it some other way, one without says 404 "not found". Nothing is written either way. */
async function hasRoute(path: string, method: 'GET' | 'POST'): Promise<boolean> {
  const r = await fetch(new URL(path, server.url), { method, body: method === 'POST' ? '' : undefined });
  const j = (await r.json().catch(() => ({}))) as { error?: string };
  return !(r.status === 404 && j.error === 'not found');
}

// ------------------------------------------------------------------ page helpers
const recording = () => !!window.__review.seg;
const sidebar = () => page.evaluate(() => window.__review.sidebar!);
const rowsOf = async (): Promise<SidebarRow[]> => (await sidebar()).rows;
const item = (key: string) => page.locator(`#side li.item[data-key="${key}"]`);
const box = async (sel: string) => (await page.locator(sel).boundingBox())!;

async function shot(name: string): Promise<void> {
  const body = await page.screenshot({ type: 'png' });
  await test.info().attach(name, { body, contentType: 'image/png' });
  if (SHOTS) {
    mkdirSync(SHOTS, { recursive: true });
    writeFileSync(join(SHOTS, name), body);
  }
}

/** Drop the test PNG onto the video at (x, y), normalized to the frame, as a file dragged in from the desktop. */
async function dropImage(x: number, y: number): Promise<void> {
  await page.evaluate(({ b64, x, y }) => {
    const file = new File([Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))], 'shot.png', { type: 'image/png' });
    const dt = new DataTransfer();
    dt.items.add(file);
    const r = document.querySelector('#ink')!.getBoundingClientRect();
    const at = { clientX: r.left + x * r.width, clientY: r.top + y * r.height };
    const target = document.elementFromPoint(at.clientX, at.clientY) ?? document.body;
    for (const type of ['dragenter', 'dragover', 'drop']) {
      target.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt, ...at }));
    }
  }, { b64: png.toString('base64'), x, y });
}

/** Ctrl+V with the test PNG on the clipboard. */
async function pasteImage(): Promise<void> {
  await page.evaluate((b64) => {
    const dt = new DataTransfer();
    dt.items.add(new File([Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))], 'paste.png', { type: 'image/png' }));
    document.dispatchEvent(new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData: dt }));
  }, png.toString('base64'));
}

async function seek(t: number): Promise<void> {
  await page.evaluate((t) => { document.querySelector('video')!.currentTime = t; }, t);
  await ui.waitFor(() => !document.querySelector('video')!.seeking, 5000);
}

const toast = () => page.evaluate(() => {
  const el = document.querySelector('#toast')!;
  return el.classList.contains('show') ? el.textContent ?? '' : '';
});

test.beforeAll(async ({ browser }) => {
  fam = family('sidebar', ['v5', 'v6']);
  const dir = join(WORK, 'sidebar-media');
  mkdirSync(dir, { recursive: true });
  const pngPath = join(dir, 'shot.png');
  // A generated test card, nobody's footage: 480x270.
  await run('ffmpeg', ['-v', 'error', '-nostdin', '-y', '-f', 'lavfi', '-i', 'testsrc2=s=480x270:d=1', '-frames:v', '1',
    pngPath]);
  png = readFileSync(pngPath);
  server = await startServer('sidebar', fam.files.v6!, PORTS.sidebar);
  has.image = await hasRoute('api/session/2000-01-01_00-00-00/image?v=v6&name=probe.txt', 'POST');
  has.takes = await hasRoute('api/takes/2000-01-01_00-00-00/img_01.png?v=v6', 'GET');
  test.info().annotations.push({ type: 'server routes', description: JSON.stringify(has) });
  context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  await context.grantPermissions(['microphone'], { origin: server.url.replace(/\/$/, '') });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  page.on('request', capture);
  await mockRoutes(page);
  ui = new Review(page);
});

test.afterAll(async () => {
  await context?.close();
  await server?.stop();
});

// ------------------------------------------------------------------ newest version
test('open questions have readable comments, timecodes and reply controls', async () => {
  await ui.open(server.url);
  await ui.waitFor(() => window.__review.sidebar!.panel, 8000);
  const sb = await sidebar();
  expect(sb).toMatchObject({ panel: true, toggle: false, phone: false });
  expect(sb.rows.map((r) => [r.key, r.time, r.mark, r.answer])).toEqual([
    ['question:101', '0:04', 'question', 101], ['question:102', '0:12', 'question', 102]]);
  await expect(page.locator('#side li.item')).toHaveCount(2);
  // The header: a speech bubble (no word), the version, search and hide (spec/notes.md, "The comments header").
  await expect(page.locator('#side header .cicon svg')).toBeVisible();
  await expect(page.locator('#side header [data-part~="version-menu"]')).toHaveText('v6');
  await expect(item('question:101').locator('.text')).toHaveText(/Keep the zoom on the shop/);
  await expect(item('question:101').locator('.time')).toHaveText('0:04');
  await expect(item('question:101').locator('.chip')).toHaveCount(0);
  await expect(item('question:101').locator('.status')).toHaveText('Question');
  await expect(item('question:101').locator('.answer')).toHaveText('Answer out loud');
  await expect(page.locator('#side')).not.toContainText('hidden');
  // A right-hand column next to the video; the bar stays full width under both.
  const side = await box('#side'), stage = await box('#stage'), bar = await box('#bar');
  expect(side.width).toBeCloseTo(360, 0);
  expect(stage.width + side.width).toBeCloseTo(1920, 0);
  expect(side.x).toBeCloseTo(stage.x + stage.width, 0);
  expect(bar.width).toBeCloseTo(1920, 0);
  await seek(4);
  await sleep(300);
  await shot('sidebar-newest-1920.png');
});

test('click an item: the video jumps to its moment, and the row under the playhead is marked', async () => {
  await item('question:102').locator('.jump').click();
  await ui.waitFor(() => Math.abs(document.querySelector('video')!.currentTime - 12.5) < 0.05, 5000);
  await ui.waitFor(() => window.__review.sidebar!.active === 'question:102', 3000);
  await expect(item('question:102')).toHaveClass(/active/);
});

// ------------------------------------------------------------------ older version
test('To do and Done split the comments, each with one status', async () => {
  notes.v6 = [...OLDER, ...NEWEST]; push('notes', {});
  await expect(page.getByRole('tab', { name: 'To do 3', exact: true })).toBeVisible();
  await expect(page.locator('#side .item')).toHaveCount(3);
  await expect(item('note:2').locator('.status')).toHaveText('Question');
  await expect(item('note:2').locator('.asked')).toContainText('Keep the zoom');
  await page.getByRole('tab', { name: 'Done 2', exact: true }).click();
  await expect(page.locator('#side .item')).toHaveCount(2);
  await expect(item('note:1').locator('.status')).toHaveText('Done');
  await expect(item('note:1').locator('.change')).toHaveText('Title now lands on the first beat');
  expect((await ui.state()).shown).toBe('v6');
  await expect(page.locator('#side')).not.toContainText('hidden');
});

test('Reopen is in the comment\'s ⋯ (an accepted one has none) and posts to the notes route', async () => {
  await item('note:4').hover();
  await item('note:4').getByRole('button', { name: 'More for this comment' }).click();
  await expect(page.getByRole('menuitem', { name: 'Reopen' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await item('note:1').hover();
  await item('note:1').getByRole('button', { name: 'More for this comment' }).click();
  await page.getByRole('menuitem', { name: 'Reopen' }).click();
  await expect.poll(() => reopened).toEqual([1]);
});

test('resolved comment timecodes seek on the current video', async () => {
  await item('note:1').locator('.jump').click();
  await ui.waitFor(() => Math.abs(document.querySelector('video')!.currentTime - 3) < 0.1, 5000);
  expect((await ui.state()).shown).toBe('v6');
  notes.v6 = NEWEST; push('notes', {});
  await page.getByRole('tab', { name: /^To do/ }).click();
  await expect(page.locator('#side .item')).toHaveCount(2);
});

test('N hides the sidebar (the page looks as before), remembered across a reload; N brings it back', async () => {
  // Leave the filter's keyboard focus before using a player shortcut.
  await item('question:102').locator('.jump').click();
  await ui.key('n');
  await ui.waitFor(() => !window.__review.sidebar!.panel, 3000);
  await expect(page.locator('#side')).toBeHidden();
  await expect(page.locator('#sideToggle')).toBeVisible();
  // The speech bubble and the to-do count, as on a phone.
  await expect(page.locator('#sideToggle')).toHaveText('2');
  await expect(page.locator('#sideToggle')).toHaveAttribute('aria-label', 'Show comments: 2 to do');
  expect((await box('#stage')).width).toBeCloseTo(1920, 0);
  await page.reload();
  await ui.waitFor(READY, 20_000);
  await ui.waitFor(() => window.__review.notes!.available, 8000);
  expect(await sidebar()).toMatchObject({ panel: false, toggle: true });
  await page.locator('#sideToggle').click();
  await ui.waitFor(() => window.__review.sidebar!.panel, 3000);
  await ui.key('n');
  await ui.waitFor(() => !window.__review.sidebar!.panel, 3000);
  await ui.key('n');
  await ui.waitFor(() => window.__review.sidebar!.panel, 3000);
  expect(await page.evaluate(() => localStorage.getItem('retake.sidebar'))).toBe('open');
});

// ------------------------------------------------------------------ images and answers
test('not recording: an image drop or paste is refused with the pen\'s kind of hint, and nothing starts', async () => {
  await dropImage(0.5, 0.5);
  await expect.poll(toast).toBe('Press R to record before adding images');
  await pasteImage();
  await sleep(200);
  const t = await page.evaluate(() => ({ id: window.__review.take.id, images: window.__review.take.images!.length }));
  expect(t).toEqual({ id: null, images: 0 });
  expect(page.url()).toBe(server.url); // the browser did not open the dropped file
  expect(posts.images).toEqual([]);
});

test('Answer: jumps to the question and records, exactly like R, with answer_to on the segment', async () => {
  await seek(30);
  await item('question:101').locator('.answer').click();
  await ui.waitFor(recording, 8000);
  const s = await page.evaluate(() => {
    const seg = window.__review.seg as { id: string; answer_to?: number };
    return { seg: { id: seg.id, answer_to: seg.answer_to }, t: document.querySelector('video')!.currentTime };
  });
  expect(s.seg).toMatchObject({ id: 'S1', answer_to: 101 });
  expect(Math.abs(s.t - 4)).toBeLessThan(0.1);
  await expect(item('question:101').locator('.answer')).toHaveText('Stop answering');
  await expect(page.locator('#bar')).toHaveClass(/recording/);
});

test('while recording: a drop lands at its point and time, a paste too; Z undoes the last stroke or image', async () => {
  await dropImage(0.7, 0.3);
  await ui.waitFor(() => window.__review.take.images!.length === 1, 5000);
  const [img] = await page.evaluate(() => window.__review.take.images!.map((x) => ({ ...x, blob: null, stored: null })));
  expect(img).toMatchObject({ id: 1, file: 'img_01.png', mime: 'image/png' });
  expect(Math.abs(img!.video_t - 4)).toBeLessThan(0.1);
  const [x, y, w, h] = img!.box;
  expect(x + w / 2).toBeCloseTo(0.7, 2);
  expect(y + h / 2).toBeCloseTo(0.3, 2);
  // 480x270 on a frame narrower than 1600 px: capped at 30% of the frame's width, same aspect.
  const ink = await box('#ink');
  expect(w).toBeCloseTo(Math.min(480 / ink.width, 0.3), 2);
  expect((h * ink.height) / (w * ink.width)).toBeCloseTo(270 / 480, 2);
  await expect(page.locator('#images .card')).toHaveCount(1);
  const card = await box('#images .card');
  expect(card.x - ink.x).toBeCloseTo(x * ink.width, 0);
  expect(card.y - ink.y).toBeCloseTo(y * ink.height, 0);
  await expect(page.locator('#images .card .grip')).toHaveCount(1);
  await expect.poll(toast, { timeout: 5000 }).toBe(''); // the earlier refusal has faded
  await shot('image-recording-1920.png');

  // Drag the corner: the card grows from its top-left corner, keeping its aspect ratio.
  const grip = await box('#images .card .grip');
  await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
  await page.mouse.down();
  await page.mouse.move(grip.x + 120, grip.y + 60, { steps: 6 });
  await page.mouse.up();
  const b2 = await page.evaluate(() => window.__review.take.images![0]!.box);
  expect(b2[0]).toBeCloseTo(x, 4);
  expect(b2[1]).toBeCloseTo(y, 4);
  expect(b2[2]).toBeGreaterThan(w * 1.1);
  expect(b2[3] / b2[2]).toBeCloseTo(h / w, 2);

  // A stroke, then a pasted image (where the pointer last was over the video): Z takes the image first.
  await ui.drag(line([0.15, 0.6], [0.4, 0.6]));
  await page.mouse.move(ink.x + 0.25 * ink.width, ink.y + 0.75 * ink.height);
  await pasteImage();
  await ui.waitFor(() => window.__review.take.images!.length === 2, 5000);
  const ids = await page.evaluate(() => ({ strokes: window.__review.take.strokes.map((s) => (s as { id: number }).id),
    images: window.__review.take.images!.map((i) => [i.id, i.file]) }));
  expect(ids).toEqual({ strokes: [2], images: [[1, 'img_01.png'], [3, 'img_02.png']] });
  const pasted = await page.evaluate(() => window.__review.take.images![1]!.box);
  expect(pasted[0] + pasted[2] / 2).toBeCloseTo(0.25, 2);
  await ui.key('z');
  const after1 = await page.evaluate(() => ({ s: window.__review.take.strokes.map((s) => !!s.undone),
    i: window.__review.take.images!.map((x) => !!x.undone) }));
  expect(after1).toEqual({ s: [false], i: [false, true] });
  await ui.key('z');
  const after2 = await page.evaluate(() => ({ s: window.__review.take.strokes.map((s) => !!s.undone),
    i: window.__review.take.images!.map((x) => !!x.undone) }));
  expect(after2).toEqual({ s: [true], i: [false, true] });
  await expect(page.locator('#images .card')).toHaveCount(1);
  await ui.key('r');
  await ui.waitFor(() => !window.__review.seg, 5000);
});

test('Done: the take is saved with its image (images.json), answer_to and closed, then POST /done', async () => {
  await ui.click('#done');
  await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 15_000);
  const take = await page.evaluate(() => window.__review.take.id!);
  const sv = await page.evaluate(() => window.__review.serialize(window.__review.take)) as {
    segments: { answer_to?: number }[]; images: { id: number; file: string }[]; session: { closed?: boolean } };
  assertValid('dataBody', sv, 'serialize(take) at Done');
  expect(sv.segments.map((s) => s.answer_to)).toEqual([101]);
  expect(sv.images.map((i) => [i.id, i.file])).toEqual([[1, 'img_01.png']]);
  expect(sv.session.closed).toBe(true);
  await expect.poll(() => posts.done.length).toBe(1);
  expect(posts.done[0]).toBe(`/api/session/${take}/done?v=v6`);
  // The dropped image went up once, as image/png, and the last JSON lists only it (the paste was undone; it is only
  // sent if an autosave caught it before Z).
  const sent = posts.images.filter((p) => p.url.includes(take));
  expect(sent.filter((p) => p.url.endsWith('name=img_01.png')).map((p) => [p.url, p.type, p.size])).toEqual([
    [`/api/session/${take}/image?v=v6&name=img_01.png`, 'image/png', png.length]]);
  expect(sent.every((p) => p.type === 'image/png' && p.size === png.length)).toBe(true);
  const last = posts.data.at(-1)!;
  expect((last.images as { file: string }[]).map((i) => i.file)).toEqual(['img_01.png']);

  const [dir] = takeDirs(join(fam.dir, 'v6', 'clip_v6.review'));
  expect(dir).toBeTruthy();
  const files = validateTakeDir(dir!);
  expect((files.segments as { answer_to?: number }[]).map((s) => s.answer_to)).toEqual([101]);
  expect((files.session as { closed?: boolean }).closed).toBe(true);
  if (has.image) {
    expect(readFileSync(join(dir!, 'img_01.png')).equals(png)).toBe(true);
    expect(existsSync(join(dir!, 'images.json')), 'images.json').toBe(true);
    expect((files.images as { file: string }[]).map((i) => i.file)).toEqual(['img_01.png']);
  } else {
    expect(mockedImages.get(`${take}/img_01.png`)?.equals(png)).toBe(true);
    test.info().annotations.push({ type: 'mocked', description: 'the server has no image route yet: checked the '
      + 'request and the /data body instead of img_01.png and images.json on disk' });
  }
});

test('live updates: job progress, a failure with the server\'s hint, then the new note', async () => {
  push('job', { id: 11, kind: 'transcribe', state: 'running' });
  await expect(page.locator('#side .job')).toHaveText('Transcribing your take…', { timeout: 8000 });
  await shot('sidebar-transcribing-1920.png');
  push('job', { id: 11, kind: 'transcribe', state: 'failed', error: "Transcription isn't set up",
    hint: 'Install whisper.cpp, or set [transcribe] command in config.toml.' });
  await expect(page.locator('#side .job')).toContainText("Transcription isn't set up", { timeout: 8000 });
  await expect(page.locator('#side .job .hint')).toHaveText('Install whisper.cpp, or set [transcribe] command in config.toml.');
  push('job', { id: 12, kind: 'transcribe', state: 'done' });
  await expect(page.locator('#side .job')).toHaveCount(0, { timeout: 8000 });
  notes.v6 = [...NEWEST, { id: 8, kind: 'note', status: 'open', t0: 4.1, t1: 7, by: AUTHOR, text: 'Wide shot, definitely.' }];
  push('notes', {});
  await expect(item('note:8')).toBeVisible({ timeout: 8000 });
  await expect(item('note:8').locator('.text')).toHaveText('Wide shot, definitely.');
});

test('history: an earlier take\'s image shows near its time, like a stroke', async () => {
  // The take's image comes back from the server's history when it lists images (spec/notes.md); until then, add one.
  const historyRoute = (u: URL) => u.pathname === '/api/info' || u.pathname === '/api/history';
  await page.route(historyRoute, async (route) => {
    const r = await route.fetch();
    const info = (await r.json()) as { history: unknown[] };
    const earlier = { id: '2000-01-01_00-00-00', inherited_from: null, voice: { bin: 0.25, bins: [] }, strokes: [],
      images: [{ url: '/api/takes/2000-01-01_00-00-00/img_01.png?v=v6', video_t: 20, box: [0.1, 0.1, 0.3, 0.16875] }] };
    info.history.push(earlier);
    return json(route, 200, info);
  });
  await page.reload();
  await ui.waitFor(READY, 20_000);
  await seek(20.2);
  await expect(page.locator('#images .card')).toHaveCount(1);
  await expect(page.locator('#images .card .grip')).toHaveCount(0); // not this take's: no resizing
  const op = await page.locator('#images .card img').first().evaluate((el) => Number(getComputedStyle(el).opacity));
  expect(op).toBeCloseTo(0.85, 1); // earlier annotations remain readable while not recording
  await seek(25);
  await expect(page.locator('#images .card')).toHaveCount(0);
  await page.unroute(historyRoute);
});

test('a drawing comment shows its strokes over the still of its moment', async () => {
  // An earlier take with two strokes at 30 s, and its note, words-less, whose snapshot the server serves.
  const take = '2000-01-02_00-00-00';
  const ring = Array.from({ length: 33 }, (_, i): [number, number] =>
    [0.62 + 0.1 * Math.cos((i / 32) * 2 * Math.PI), 0.42 + 0.18 * Math.sin((i / 32) * 2 * Math.PI)]);
  const historyRoute = (u: URL) => u.pathname === '/api/info' || u.pathname === '/api/history';
  await page.route(historyRoute, async (route) => {
    const r = await route.fetch();
    const info = (await r.json()) as { history: unknown[] };
    info.history.push({ id: take, inherited_from: null, voice: { bin: 0.25, bins: [] }, images: [], strokes: [
      { id: 1, video_t: 30.2, color: 'red', points: ring },
      { id: 2, video_t: 30.6, color: 'yellow', points: [[0.2, 0.78], [0.48, 0.78]] }] });
    return json(route, 200, info);
  });
  const snapshot = '/api/notes/9/snapshot?m=1';
  const snapRoute = (u: URL) => u.pathname === '/api/notes/9/snapshot';
  await page.route(snapRoute, (route) => route.fulfill({ status: 200, contentType: 'image/png', body: png }));
  const before = notes.v6!;
  notes.v6 = [...before, { id: 9, kind: 'note', status: 'open', t0: 30, t1: 31, by: AUTHOR, text: '', take,
    version: 'v6', drawings: ['red', 'yellow'], snapshot }];
  await page.reload();
  await ui.waitFor(READY, 20_000);
  const card = item('note:9');
  await expect(card.locator('.text')).toHaveText('Drawing', { timeout: 8000 });
  await expect(card.locator('.sketch path')).toHaveCount(2);
  const frame = card.locator('.sketch image').first();
  await expect(frame).toHaveAttribute('href', snapshot);
  // The frame is under the lines, and it loaded.
  expect(await card.locator('.sketch svg > *').first().evaluate((el) => el.tagName)).toBe('image');
  await expect.poll(() => page.evaluate(() => performance.getEntriesByType('resource')
    .some((e) => e.name.includes('/api/notes/9/snapshot')))).toBe(true);
  await card.scrollIntoViewIfNeeded();
  await sleep(300);
  await shot('sidebar-drawing-1920.png');
  if (SHOTS) writeFileSync(join(SHOTS, 'sidebar-drawing-card.png'), await card.screenshot());
  notes.v6 = before;
  await page.unroute(snapRoute);
  await page.unroute(historyRoute);
});

// ------------------------------------------------------------------ phone
test('phone: the list always fills the space under the controls; the bubble goes to it', async () => {
  await page.setViewportSize(PHONE);
  await page.reload();
  await ui.waitFor(READY, 20_000);
  await ui.waitFor(() => window.__review.notes!.available, 8000);
  // The phone layout (portrait): the list sits under the phone controls (#pbar) down to Record, which stays on screen
  // below it; nothing is left empty between them.
  expect(await page.evaluate(() => document.documentElement.dataset.layout)).toBe('portrait');
  expect(await sidebar()).toMatchObject({ phone: true, panel: true, toggle: true });
  await expect(page.locator('#side')).toBeVisible();
  // (polled: the stage takes the video's shape once it has loaded)
  await expect.poll(async () => {
    const side = await box('#side'), pbar = await box('#pbar'), rec = await box('#precord');
    return Math.max(Math.abs(side.y - pbar.y - pbar.height), Math.abs(rec.y - side.y - side.height));
  }, { message: 'no gap above or below the comments' }).toBeLessThan(1.5);
  const side = await box('#side'), rec = await box('#precord');
  expect(side.width).toBeCloseTo(PHONE.width, 0);
  expect(rec.y + rec.height).toBeLessThanOrEqual(PHONE.height + 1);
  // The bubble doesn't hide it: it shows To do.
  await page.locator('#side .tabs button', { hasText: 'Done' }).click();
  await page.locator('#sideToggle').click();
  expect(await sidebar()).toMatchObject({ panel: true });
  await expect(page.locator('#side .tabs button', { hasText: 'To do' })).toHaveAttribute('aria-selected', 'true');
  await seek(4);
  await sleep(300);
  await shot('sidebar-newest-phone.png');
  expect((await ui.state()).shown).toBe('v6');
  await ui.key('r');
  await ui.waitFor(recording, 8000);
  await dropImage(0.5, 0.45);
  await ui.waitFor(() => window.__review.take.images!.length === 1, 5000);
  await sleep(300);
  await shot('image-recording-phone.png');
  await ui.key('r');
  await page.locator('#done').click();
  await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 15_000);
});

test('every take still matches spec/take.schema.json', async () => {
  const dirs = takeDirs(join(fam.dir, 'v6', 'clip_v6.review'));
  expect(dirs).toHaveLength(2);
  for (const d of dirs) validateTakeDir(d);
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
