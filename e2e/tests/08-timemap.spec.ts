// The review bridge's time map (spec/bridge.md): two versions rendered from the editor's demo timeline, v2 after a
// patch that trims 0.3 s of VO air at 3.6 s on v1. Both have render manifests, so C and the version picker land on
// the matching moment: 9.0 s on v1 is 8.7 s on v2, while 1.0 s (before the trim) stays 1.0 s. Needs a built retake-edit
// ($RETAKE_EDIT, editor/target/release, or $PATH); it skips without one.
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { LEGACY, PORTS, REPO, retakeEdit, WORK } from '../lib/env.ts';
import { run } from '../lib/proc.ts';
import { Review, sleep } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new with the review bridge: the legacy player has no time map');
const EDIT = retakeEdit();
test.skip(!EDIT, 'needs retake-edit (cd editor && cargo build --release, or set RETAKE_EDIT)');

const DEMO = join(REPO, 'editor', 'examples', 'demo');
let server: Server;
let context: BrowserContext;
let page: Page;
let ui: Review;
const pageErrors: string[] = [];

interface Segment { a: [number, number]; b: [number, number]; by: string }
const mapTime = (segs: Segment[], t: number): number => {
  const s = segs.find((x, i) => t < x.a[1] || i === segs.length - 1)!;
  return s.b[0] + ((t - s.a[0]) * (s.b[1] - s.b[0])) / (s.a[1] - s.a[0]);
};

test.beforeAll(async ({ browser }) => {
  test.setTimeout(120_000);
  const dir = join(WORK, 'timemap');
  const edit = join(dir, 'edit');
  mkdirSync(edit, { recursive: true });
  for (const f of ['timeline.json', 'vo.words.json']) copyFileSync(join(DEMO, f), join(edit, f));
  await run('bash', [join(DEMO, 'make-media.sh'), join(edit, 'media'), EDIT!], { nice: true, env: { DEMO_SIZE: '320x180' } });
  const tl = join(edit, 'timeline.json');
  const render = (out: string) => run(EDIT!, ['render', tl, '-o', out, '--scale', '0.25', '--json'], { nice: true });
  await render(join(dir, 'v1', 'demo_v1.mp4'));
  writeFileSync(join(edit, 'trim.json'), JSON.stringify({
    base: 0, answers: [], summary: "Trim 0.3 s of air after 'generated.'",
    ops: [{ op: 'set', id: 'a', path: 'out', value: 3.8 }],
  }));
  await run(EDIT!, ['patch', 'apply', tl, join(edit, 'trim.json')]);
  await render(join(dir, 'v2', 'demo_v2.mp4'));
  server = await startServer('timemap', join(dir, 'v2', 'demo_v2.mp4'), PORTS.timemap);
  context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  ui = new Review(page);
});

test.afterAll(async () => {
  await context?.close();
  await server?.stop();
});

test('the server maps v1 onto v2 and back from the render manifests', async () => {
  const get = async (q: string) => (await fetch(new URL(`api/timemap?${q}`, server.url))).json() as
    Promise<{ from: string; to: string; segments: Segment[] }>;
  const there = await get('from=v1&to=v2');
  expect(there.segments.length).toBeGreaterThan(1);
  expect(mapTime(there.segments, 1)).toBeCloseTo(1, 3);
  expect(mapTime(there.segments, 9)).toBeCloseTo(8.7, 3);
  expect(mapTime(there.segments, 13)).toBeCloseTo(12.7, 3);
  const back = await get('from=v2&to=v1');
  expect(mapTime(back.segments, 8.7)).toBeCloseTo(9, 3);
  expect((await fetch(new URL('api/timemap?from=v1&to=v9', server.url))).status).toBe(404);
});

/** Seeks the shown version to t and waits for the frame. */
async function seek(t: number): Promise<void> {
  await page.evaluate((x) => { document.querySelector('video')!.currentTime = x; }, t);
  await ui.waitFor(() => !document.querySelector('video')!.seeking, 5000);
}

async function press(key: string) {
  await ui.key(key);
  await ui.settle();
  await sleep(200);
  return ui.state();
}

test('C flips to the matching moment, and back', async () => {
  await ui.open(server.url);
  expect(await page.evaluate(() => window.__review.view)).toMatchObject({ base: 'v2', shown: 'v2' });
  await seek(8.7);
  const flipped = await press('c');
  expect(flipped).toMatchObject({ shown: 'v1', base: 'v2', flipped: true });
  expect(flipped.t).toBeCloseTo(9, 1); // the same moment: 0.3 s later on v1, not the same clock time
  expect(Math.abs(flipped.t - 9)).toBeLessThan(0.05);
  const back = await press('c');
  expect(back).toMatchObject({ shown: 'v2', flipped: false });
  expect(Math.abs(back.t - 8.7)).toBeLessThan(0.05);
  // Before the trim nothing moved: the same time.
  await seek(1);
  const early = await press('c');
  expect(early.shown).toBe('v1');
  expect(Math.abs(early.t - 1)).toBeLessThan(0.05);
  await press('c');
});

test('[ and ] are not keys; the version picker lands on the matching moment too', async () => {
  await seek(12.7);
  for (const key of ['[', ']']) expect(await press(key)).toMatchObject({ base: 'v2', shown: 'v2', flipped: false });
  const pick = async (label: string) => {
    await ui.click('#verBtn');
    await ui.click(`#verList [data-ver="${label}"]`);
    await ui.settle();
    await sleep(200);
    return ui.state();
  };
  const prev = await pick('v1');
  expect(prev).toMatchObject({ base: 'v1', shown: 'v1', flipped: false });
  expect(Math.abs(prev.t - 13)).toBeLessThan(0.05);
  const next = await pick('v2');
  expect(next).toMatchObject({ base: 'v2', shown: 'v2' });
  expect(Math.abs(next.t - 12.7)).toBeLessThan(0.05);
  expect(pageErrors).toEqual([]);
});
