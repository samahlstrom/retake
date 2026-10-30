// The eraser (new): E or the eraser button makes the pen an eraser. A swipe over a line on screen removes that whole
// line, recording or not; Z brings back the last erased line; a pen color switches back to the pen. A line of the take
// in progress is left out of its strokes.json (events.json says erase / unerase); a line of an earlier take is kept in
// the version's erased.json, so it stays gone after a reload while the old take's files stay as they were.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { line, Review, sleep } from '../lib/review.ts';
import { validateTakeDir } from '../lib/schema.ts';
import { startServer, type Server } from '../lib/server.ts';
import { family, takeDirs, type Family } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no eraser');

let fam: Family;
let server: Server;
let context: BrowserContext;
let page: Page;
let ui: Review;
const pageErrors: string[] = [];

/** This take's strokes: id, and whether erased or undone. */
const strokes = () => page.evaluate(() => window.__review.take.strokes.map((s) => ({ id: s.id, undone: !!s.undone })));
const live = async () => (await strokes()).filter((s) => !s.undone).map((s) => s.id);

test.beforeAll(async ({ browser }) => {
  fam = family('eraser', ['v6']);
  server = await startServer('eraser', fam.files.v6!, PORTS.eraser);
  context = await browser.newContext({
    viewport: { width: 1920, height: 1080 },
  });
  page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  ui = new Review(page);
  await ui.open(server.url);
});

test.afterAll(async () => {
  await context?.close();
  await server?.stop();
});

test('the eraser removes the line it crosses, recording or not; Z brings it back', async () => {
  await page.evaluate(() => {
    const v = document.querySelector('video')!;
    v.pause();
    v.currentTime = 5;
  });
  await sleep(300);
  await ui.key('r');
  await ui.waitFor(() => !!window.__review.seg, 8000);
  await ui.drag(line([0.2, 0.3], [0.5, 0.3])); // id 1
  await ui.drag(line([0.2, 0.6], [0.5, 0.6])); // id 2
  expect(await live()).toEqual([1, 2]);

  // E, then a swipe down through line 1 only.
  await ui.key('e');
  expect(await page.evaluate(() => document.querySelector('#ink')!.classList.contains('erasing'))).toBe(true);
  await ui.drag(line([0.35, 0.22], [0.35, 0.4]));
  expect(await live()).toEqual([2]);

  // Z brings line 1 back.
  await ui.key('z');
  expect(await live()).toEqual([1, 2]);

  // The eraser button works like E; a swipe through both lines takes both.
  await ui.click('#eraser'); // off
  await ui.click('#eraser'); // on
  expect(await page.locator('#eraser').getAttribute('aria-pressed')).toBe('true');
  await ui.drag(line([0.3, 0.2], [0.3, 0.7]));
  expect(await live()).toEqual([]);
  await ui.key('z'); // brings back the later erase first
  await ui.key('z');
  expect((await live()).sort()).toEqual([1, 2]);

  // A pen color switches back to the pen: a drag draws again.
  await ui.key('2');
  expect(await page.locator('#eraser').getAttribute('aria-pressed')).toBe('false');
  await ui.drag(line([0.6, 0.3], [0.8, 0.3])); // id 3
  expect(await live()).toEqual([1, 2, 3]);

  // Erase line 1 for good, then stop recording: the eraser still erases (it only ever removes), and Z still undoes.
  await ui.key('e');
  await ui.drag(line([0.35, 0.22], [0.35, 0.4]));
  expect(await live()).toEqual([2, 3]);
  await ui.key('r');
  await ui.waitFor(() => !window.__review.seg, 8000);
  await ui.drag(line([0.7, 0.2], [0.7, 0.4])); // over line 3, not recording
  expect(await live()).toEqual([2]);
  await ui.key('z');
  expect(await live()).toEqual([2, 3]);
});

test('Done saves only the remaining lines, and the events tell the story', async () => {
  await ui.click('#done');
  await ui.waitFor(() => window.__review.take.closed && !window.__review.take.saving, 15_000);
  const dirs = takeDirs(join(fam.dir, 'v6', 'clip_v6.review'));
  expect(dirs).toHaveLength(1);
  validateTakeDir(dirs[0]!);
  const saved = JSON.parse(readFileSync(join(dirs[0]!, 'strokes.json'), 'utf8')) as { id: number }[];
  expect(saved.map((s) => s.id)).toEqual([2, 3]);
  const events = JSON.parse(readFileSync(join(dirs[0]!, 'events.json'), 'utf8')) as { event: string; id?: number }[];
  expect(events.filter((e) => e.event === 'erase' || e.event === 'unerase').map((e) => `${e.event} ${e.id}`)).toEqual([
    'erase 1',
    'unerase 1',
    'erase 1',
    'erase 2',
    'unerase 2',
    'unerase 1',
    'erase 1',
    'erase 3',
    'unerase 3',
  ]);
});

test("an earlier take's line: erased without recording, it stays gone after a reload; its take is untouched", async () => {
  const reviewDir = join(fam.dir, 'v6', 'clip_v6.review');
  const takeDir = takeDirs(reviewDir)[0]!;
  const strokesBefore = readFileSync(join(takeDir, 'strokes.json'), 'utf8');
  const erasedFile = join(reviewDir, 'erased.json');
  const erasedText = () => {
    try {
      return readFileSync(erasedFile, 'utf8');
    } catch {
      return '';
    }
  };
  const histIds = () =>
    page.evaluate(() =>
      window.__review.hist.strokes
        .map((s) => s as { id?: number; undone?: boolean })
        .filter((s) => !s.undone)
        .map((s) => s.id),
    );
  const reopen = async () => {
    await page.reload();
    await ui.waitFor(() => !!window.__review && window.__review.info.label === 'v6', 20_000);
    await page.evaluate(() => {
      const v = document.querySelector('video')!;
      v.pause();
      v.currentTime = 5;
    });
    await sleep(400);
  };
  await reopen();
  expect((await histIds()).sort()).toEqual([2, 3]);
  expect(await page.evaluate(() => !!window.__review.seg)).toBe(false); // not recording

  // E, a swipe over line 2 (y 0.6): it goes, and the server keeps the erase.
  await ui.key('e');
  await ui.drag(line([0.3, 0.5], [0.3, 0.7]));
  expect(await histIds()).toEqual([3]);
  await expect.poll(erasedText).toContain('"id": 2');

  // Z brings it back on the server too; erase it again, then reload: still gone.
  await ui.key('z');
  expect((await histIds()).sort()).toEqual([2, 3]);
  await expect.poll(erasedText).not.toContain('"id": 2');
  await ui.drag(line([0.3, 0.5], [0.3, 0.7]));
  await expect.poll(erasedText).toContain('"id": 2');
  await reopen();
  expect(await histIds()).toEqual([3]);

  // The old take's own files are as they were, and erasing made no new take.
  expect(readFileSync(join(takeDir, 'strokes.json'), 'utf8')).toBe(strokesBefore);
  expect(takeDirs(reviewDir)).toHaveLength(1);
});

test('no uncaught page errors', async () => {
  expect(pageErrors).toEqual([]);
});
