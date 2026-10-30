// New: the phone layout, in Chromium's mobile emulation (touch, isMobile, a coarse pointer) at 390x844 portrait and
// 844x390 landscape, driven by taps and real touch drags. The desktop layout is what the other specs run (1920x1080).
//
// For each orientation: the page fits (no horizontal overflow), the controls are visible thumb-sized tap targets,
// a drag before recording draws nothing, Record records, the pen draws only while recording, undo, scrubbing the
// timeline, the flip button (C) and its refusal past the preview's end, Done (the take validates against
// spec/take.schema.json), and the version list.
// Then iOS Safari's recorder: it records only audio/mp4, and the segment is saved as seg_01.m4a.
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { join } from 'node:path';
import { LEGACY, PORTS } from '../lib/env.ts';
import { audioDuration } from '../lib/media.ts';
import { line, loop, Review, sleep } from '../lib/review.ts';
import { validateTakeDir } from '../lib/schema.ts';
import { startServer, type Server } from '../lib/server.ts';
import { readTake } from '../lib/take.ts';
import { family, takeDirs, type Family } from '../lib/work.ts';

test.skip(LEGACY, 'new: the legacy player has no phone layout');

const recording = () => !!window.__review.seg;
const notSaving = () => window.__review.take.closed && !window.__review.take.saving;

/** The tap targets the phone layout must have (and the e2e names them by). */
const CONTROLS = ['#rec', '#done', '#flip', '#undo', '#play', '#verBtn'];

interface Phone {
  fam: Family;
  server: Server;
  context: BrowserContext;
  page: Page;
  ui: Review;
  errors: string[];
}

async function openPhone(browser: Browser, name: string, viewport: { width: number; height: number },
  init?: () => void): Promise<Phone> {
  const fam = family(name, ['v5', 'v6', 'v7']);
  const server = await startServer(name, fam.files.v6!, PORTS.mobile);
  const context = await browser.newContext({ viewport, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await context.grantPermissions(['microphone'], { origin: server.url.replace(/\/$/, '') });
  if (init) await context.addInitScript(init);
  const page = await context.newPage();
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(`${e.name}: ${e.message}`));
  return { fam, server, context, page, ui: new Review(page), errors };
}

async function closePhone(p: Phone | undefined): Promise<void> {
  await p?.context.close();
  await p?.server.stop();
}

/** Elements that stick out of the viewport sideways (anything laid out, not hidden). */
function overflowing(): string[] {
  const W = document.documentElement.clientWidth;
  const out: string[] = [];
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if (!r.width && !r.height) continue;
    if (getComputedStyle(el).visibility === 'hidden') continue;
    if (r.left < -0.5 || r.right > W + 0.5) out.push(`${el.tagName.toLowerCase()}#${el.id}.${el.className} ${r.left}..${r.right}`);
  }
  return out;
}

async function seek(page: Page, t: number): Promise<void> {
  await page.evaluate(async (to) => {
    const v = document.querySelector('video')!;
    if (!v.paused) v.pause();
    const done = new Promise((r) => v.addEventListener('seeked', r, { once: true }));
    v.currentTime = to;
    await done;
  }, t);
}

for (const o of [{ name: 'portrait', width: 390, height: 844 }, { name: 'landscape', width: 844, height: 390 }]) {
  test.describe(`phone ${o.name} ${o.width}x${o.height}`, () => {
    test.describe.configure({ mode: 'serial' });
    let p: Phone;

    test.beforeAll(async ({ browser }) => {
      p = await openPhone(browser, `mobile-${o.name}`, { width: o.width, height: o.height });
    });
    test.afterAll(async () => {
      await closePhone(p);
    });

    test('the phone layout fits the screen: no horizontal overflow, nothing scrolls', async () => {
      await p.ui.open(p.server.url);
      await sleep(400);
      const page = await p.page.evaluate(() => ({
        layout: document.documentElement.dataset.layout,
        phone: document.documentElement.hasAttribute('data-phone'),
        bar: !!document.querySelector('#bar'),
        inner: [innerWidth, innerHeight],
        scroll: [document.documentElement.scrollWidth, document.documentElement.scrollHeight],
        playsinline: document.querySelector('video')!.hasAttribute('playsinline'),
      }));
      expect(page).toMatchObject({ layout: o.name, phone: true, bar: false, inner: [o.width, o.height],
        playsinline: true });
      expect(page.scroll[0], 'no horizontal scroll').toBeLessThanOrEqual(o.width);
      expect(page.scroll[1], 'no vertical scroll').toBeLessThanOrEqual(o.height);
      expect(await p.page.evaluate(overflowing)).toEqual([]);
    });

    test('Record, Done, flip, undo, play and the version chip are visible thumb-sized targets', async () => {
      for (const sel of [...CONTROLS, '.sw[data-color=red]', '.sw[data-color=white]']) {
        const el = p.page.locator(sel);
        await expect(el, sel).toBeVisible();
        const b = (await el.boundingBox())!;
        expect(b.width, `${sel} width`).toBeGreaterThanOrEqual(44);
        expect(b.height, `${sel} height`).toBeGreaterThanOrEqual(44);
        expect(b.x, `${sel} left`).toBeGreaterThanOrEqual(0);
        expect(b.y, `${sel} top`).toBeGreaterThanOrEqual(0);
        expect(b.x + b.width, `${sel} right`).toBeLessThanOrEqual(o.width);
        expect(b.y + b.height, `${sel} bottom`).toBeLessThanOrEqual(o.height);
      }
      // The timeline: a tall hit area, and wide (not a stub next to the other controls).
      const tl = (await p.page.locator('#timeline').boundingBox())!;
      expect(tl.height).toBeGreaterThanOrEqual(44);
      expect(tl.width).toBeGreaterThan(0.45 * o.width);
      // The video: full width in portrait; the full height above the bar in landscape.
      const ink = (await p.page.locator('#ink').boundingBox())!;
      if (o.name === 'portrait') expect(ink.width).toBeGreaterThan(o.width - 2); // the stage's height is whole px
      else expect(ink.height).toBeGreaterThan(0.8 * o.height);
    });

    test('a touch hint until the first record; a drag before recording draws nothing', async () => {
      const hint = p.page.locator('#hint');
      await expect(hint).toHaveText('Tap ● to record · draw while recording · ⇄ compare');
      await expect(hint).not.toHaveClass(/gone/);
      await p.ui.touchDrag(loop(0.5, 0.5, 0.08));
      await sleep(300);
      const s = await p.ui.state();
      expect(s).toMatchObject({ take: null, strokes: 0, rec: false, toast: 'Tap ● to record before drawing' });
      // The drag neither scrolled nor zoomed the page.
      expect(await p.page.evaluate(() => [scrollX, scrollY, visualViewport?.scale ?? 1])).toEqual([0, 0, 1]);
    });

    test('tapping Record records; the pen draws while recording; undo removes the last stroke', async () => {
      await seek(p.page, 4);
      const frame = await p.page.locator('#ink').boundingBox();
      await p.ui.tap('#rec');
      await p.ui.waitFor(recording, 8000);
      await sleep(300);
      expect(await p.page.locator('#ink').boundingBox(), 'the video does not move when recording starts').toEqual(frame);
      await expect(p.page.locator('#hint')).toHaveClass(/gone/);
      await expect(p.page.locator('#recLabel')).toHaveText(/^REC \d:\d\d$/);
      await expect(p.page.locator('#recKey')).toHaveText('S1');
      await p.ui.tap('.sw[data-color=green]');
      expect(await p.page.evaluate(() => document.querySelector('.sw.on')?.getAttribute('data-color'))).toBe('green');
      await p.ui.touchDrag(loop(0.3, 0.45));
      await p.ui.tap('.sw[data-color=yellow]');
      await p.ui.touchDrag(line([0.55, 0.3], [0.8, 0.35]));
      await sleep(200);
      const strokes = () => p.page.evaluate(() => window.__review.take.strokes.map((s) => {
        const x = s as unknown as { color: string; seg: string; undone?: boolean; points: unknown[] };
        return [x.color, x.seg, !!x.undone, x.points.length > 5];
      }));
      expect(await strokes()).toEqual([['green', 'S1', false, true], ['yellow', 'S1', false, true]]);
      await p.ui.tap('#undo');
      expect(await strokes()).toEqual([['green', 'S1', false, true], ['yellow', 'S1', true, true]]);
      expect((await p.ui.state()).strokes).toBe(1);
      expect(await p.page.evaluate(() => [scrollX, scrollY, visualViewport?.scale ?? 1])).toEqual([0, 0, 1]);
    });

    test('a thumb drag on the timeline scrubs', async () => {
      await p.ui.touchDrag(line([0.1, 0.5], [0.75, 0.5], 16), '#timeline');
      await sleep(300);
      const t = (await p.ui.video()).t;
      // The track has 8 px of padding at each end, so 75% of the box is a little past 75% of the video.
      expect(t).toBeGreaterThan(0.7 * 40);
      expect(t).toBeLessThan(0.82 * 40);
    });

    test('flip: the comparison version at the same time; refused past the preview\'s end, like C', async () => {
      await seek(p.page, 5);
      await expect(p.page.locator('#flip')).toHaveText('v6⇄v7');
      await expect(p.page.locator('#flip')).not.toHaveAttribute('aria-disabled', 'true');
      const before = await p.ui.state();
      await p.ui.tap('#flip');
      await p.ui.settle();
      await sleep(200);
      const after = await p.ui.state();
      expect(after).toMatchObject({ shown: 'v7', base: 'v6', flipped: true, paused: true });
      expect(Math.abs(after.t - before.t)).toBeLessThan(0.04);
      await expect(p.page.locator('#flip')).toHaveClass(/flipped/);
      await expect(p.page.locator('#verBtn')).toHaveText('v7');
      await p.ui.tap('#flip');
      await p.ui.settle();
      expect(await p.ui.state()).toMatchObject({ shown: 'v6', flipped: false });
      // Past the v7 preview's end (0:10) C is refused: the button shows it, and a tap gets C's toast.
      await seek(p.page, 12);
      await expect(p.page.locator('#flip')).toHaveAttribute('aria-disabled', 'true');
      await p.ui.tap('#flip', { force: true }); // it only looks disabled: a finger can still tap it
      await sleep(300);
      const refused = await p.ui.state();
      expect(refused).toMatchObject({ shown: 'v6', flipped: false });
      expect(refused.toast).toBe('v7 only covers 0:00–0:10');
    });

    test('Done saves the take; its files validate against spec/take.schema.json', async () => {
      await p.ui.tap('#rec'); // stop
      await p.ui.waitFor(() => !window.__review.seg, 5000);
      await p.ui.tap('#done');
      await p.ui.waitFor(notSaving, 10_000);
      await expect(p.page.locator('#done')).toHaveText('Saved');
      expect((await p.ui.state()).toast).toBe('Saved. Tap ● to start a new take.');
      const dirs = takeDirs(join(p.fam.dir, 'v6', 'clip_v6.review'));
      expect(dirs).toHaveLength(1);
      validateTakeDir(dirs[0]!);
      const take = readTake(dirs[0]!);
      expect(take.segments.map((s) => s.id)).toEqual(['S1']);
      expect(take.strokes.map((s) => [s.id, s.color, s.seg])).toEqual([[1, 'green', 'S1']]);
      const seg = take.segments[0]!;
      expect(seg.file).toBe(`seg_01.${/mp4/.test(seg.mime) ? 'm4a' : /ogg/.test(seg.mime) ? 'ogg' : 'webm'}`);
      expect(take.audio).toEqual([seg.file]);
      expect(await audioDuration(join(dirs[0]!, seg.file))).toBeGreaterThan(1);
    });

    test('the version list opens by tap and switches the version', async () => {
      await p.ui.tap('#verBtn');
      const list = p.page.locator('#verList');
      await expect(list).toBeVisible();
      const items = list.locator('button');
      await expect(items).toHaveCount(3);
      for (const b of await items.all()) expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(44);
      const box = (await list.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(o.width);
      expect(box.y).toBeGreaterThanOrEqual(0);
      expect(box.y + box.height).toBeLessThanOrEqual(o.height);
      await seek(p.page, 3);
      await p.ui.tap('#verList [data-ver=v5]');
      await p.ui.settle();
      await sleep(200);
      expect(await p.ui.state()).toMatchObject({ base: 'v5', shown: 'v5', flipped: false });
      await expect(list).toBeHidden();
      await expect(p.page.locator('#flip')).toHaveText('v5⇄v6');
    });

    test('no uncaught page errors', async () => {
      expect(p.errors).toEqual([]);
    });
  });
}

test.describe('iOS Safari\'s recorder: audio/mp4 only', () => {
  test.describe.configure({ mode: 'serial' });
  let p: Phone;

  test.beforeAll(async ({ browser }) => {
    // Safari records AAC in mp4 and nothing else, and a recorder may report its type only once it has started.
    // (Patched on the prototype: the test runner's transpiler would turn a subclass's #private field into a helper
    // the page doesn't have.)
    p = await openPhone(browser, 'mobile-mp4', { width: 390, height: 844 }, () => {
      const MR = window.MediaRecorder;
      const isType = MR.isTypeSupported.bind(MR);
      const mimeType = Object.getOwnPropertyDescriptor(MR.prototype, 'mimeType')!.get!;
      const start = MR.prototype.start;
      const started = new WeakSet<MediaRecorder>();
      MR.isTypeSupported = (m: string) => /^audio\/mp4/.test(m) && isType(m);
      Object.defineProperty(MR.prototype, 'mimeType', {
        configurable: true,
        get(this: MediaRecorder) { return started.has(this) ? mimeType.call(this) : ''; },
      });
      MR.prototype.start = function (this: MediaRecorder, timeslice?: number) {
        started.add(this);
        start.call(this, timeslice);
      };
    });
  });
  test.afterAll(async () => {
    await closePhone(p);
  });

  test('the segment is mp4 audio, saved as seg_01.m4a, and the take validates', async () => {
    await p.ui.open(p.server.url);
    await p.ui.tap('#rec');
    await p.ui.waitFor(recording, 8000);
    const live = await p.page.evaluate(() => {
      const s = window.__review.seg as unknown as { file: string; mime: string };
      return { file: s.file, mime: s.mime };
    });
    expect(live.mime).toMatch(/^audio\/mp4/);
    expect(live.file).toBe('seg_01.m4a');
    await p.ui.touchDrag(loop(0.5, 0.5));
    await sleep(2500);
    await p.ui.tap('#done');
    await p.ui.waitFor(notSaving, 10_000);
    const dirs = takeDirs(join(p.fam.dir, 'v6', 'clip_v6.review'));
    expect(dirs).toHaveLength(1);
    validateTakeDir(dirs[0]!);
    const take = readTake(dirs[0]!);
    expect(take.segments.map((s) => [s.file, s.mime])).toEqual([['seg_01.m4a', live.mime]]);
    expect(take.audio).toEqual(['seg_01.m4a']);
    expect(await audioDuration(join(dirs[0]!, 'seg_01.m4a'))).toBeGreaterThan(2);
    expect(p.errors).toEqual([]);
  });
});
