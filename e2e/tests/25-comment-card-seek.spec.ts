// Clicking a comment is navigation. Card controls remain controls, including on a phone where a tap must not enter
// editing mode. The server and media come from the existing generated-comment fixture.
import { devices, expect, test, type BrowserContext, type Page } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { addComments, editServer } from '../lib/edit.ts';
import { READY } from '../lib/review.ts';
import type { Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: comment-card seeking is the current player');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium only; phone Safari has its matching edit suite');

let server: Server;
let ids: number[];

const state = (page: Page) =>
  page.locator('video').evaluate((video: HTMLVideoElement) => ({
    paused: video.paused,
    time: video.currentTime,
  }));

async function open(page: Page): Promise<void> {
  await page.goto(server.url);
  await page.waitForFunction(READY, null, { timeout: 20_000 });
}

async function playAt(page: Page, time: number): Promise<void> {
  await page.locator('video').evaluate((video: HTMLVideoElement, t: number) => {
    video.currentTime = t;
    return video.play();
  }, time);
  await expect.poll(() => state(page).then((s) => s.paused)).toBe(false);
}

test.beforeAll(async () => {
  server = await editServer('comment-card-seek', PORTS.keyboard);
  ids = await addComments(server.url, [
    [7, 'Keep this reaction shot'],
    [12, 'Raise the music here'],
  ]);
});
test.afterAll(async () => {
  await server?.stop();
});

test('desktop card body and text seek and pause, while its menu, editing, text selection, and delete keep their own jobs', async ({ browser }) => {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();
  try {
    await open(page);
    const first = page.locator(`#side .item[data-key="note:${ids[0]}"]`);
    const second = page.locator(`#side .item[data-key="note:${ids[1]}"]`);

    await playAt(page, 1);
    await first.click({ position: { x: 18, y: 18 } });
    expect(await state(page)).toMatchObject({
      paused: true,
      time: expect.closeTo(7, 1),
    });

    await playAt(page, 2);
    await second.locator('.text').click();
    expect(await state(page)).toMatchObject({
      paused: true,
      time: expect.closeTo(12, 1),
    });

    await page.locator('video').evaluate((video: HTMLVideoElement) => {
      video.pause();
      video.currentTime = 2;
    });
    await first.hover();
    await first.getByRole('button', { name: 'More for this comment' }).click();
    expect((await state(page)).time).toBeCloseTo(2, 1);
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    const edit = first.locator('[data-part="comment-edit"]');
    await expect(edit).toBeFocused();
    await edit.click();
    expect((await state(page)).time).toBeCloseTo(2, 1);

    await page.keyboard.press('Escape');
    const text = first.locator('.text');
    const box = (await text.boundingBox())!;
    await page.mouse.move(box.x + 3, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + Math.min(60, box.width - 3), box.y + box.height / 2, { steps: 4 });
    await page.mouse.up();
    expect((await state(page)).time).toBeCloseTo(2, 1);

    await first.hover();
    await first.getByRole('button', { name: 'More for this comment' }).click();
    await page.getByRole('menuitem', { name: 'Delete' }).click();
    await expect(first).toHaveCount(0);
    await expect(second).toBeVisible();
  } finally {
    await context.close();
  }
});

test('a phone tap seeks without editing, and the comment menu remains reachable', async ({ browser }) => {
  const { defaultBrowserType: _, ...phone } = devices['iPhone 15'];
  const context: BrowserContext = await browser.newContext({ ...phone, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  try {
    await open(page);
    const card = page.locator(`#side .item[data-key="note:${ids[1]}"]`);
    await playAt(page, 1);
    await card.locator('.text').tap();
    expect(await state(page)).toMatchObject({
      paused: true,
      time: expect.closeTo(12, 1),
    });
    const composer = page.locator('#side [data-part~="comment-line"] textarea');
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (
              window as unknown as {
                __review: { viewport: { compose: boolean } };
              }
            ).__review.viewport.compose,
        ),
      )
      .toBe(false);
    await expect(composer).not.toBeFocused();
    await expect(composer).toHaveValue('');
    await expect(card.locator('[data-part="comment-edit"]')).toHaveCount(0);
    // The fixed Feedback tab must not cover a comment's menu on a narrow screen.
    await card.getByRole('button', { name: 'More for this comment' }).tap();
    await page.getByRole('menuitem', { name: 'Edit' }).tap();
    await expect(composer).toHaveAccessibleName('Edit the comment at 0:12');
    await expect(composer).toBeFocused();
    await expect(composer).toHaveValue('Raise the music here');
  } finally {
    await context.close();
  }
});
