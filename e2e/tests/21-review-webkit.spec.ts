// The review page's controls on phones in WebKit, iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT,
// lib/webkit.ts), on the iPhone 13 and iPhone 15 viewports: play, time, the timeline, the version, the pen, undo,
// Record (a mic) and Send, all at least 40 px, inside the screen, nothing sideways; the pen's popover; a typed comment
// with a drawing and no recording (spec/notes.md, "Typed comments on a video"); the comments' version menu;
// recording (a stand-in microphone: the container has none), with nothing moving; the home's Settings. In portrait the
// comments always fill the space between the controls and Record and Send (no dead space), with 0 comments and with 8
// (the list scrolls inside it), and the speech bubble goes to them (spec/notes.md, "On a phone").
// Screenshots go to .retake-work/review2-shots/<what>-<device>.png. Under Chromium the file skips.
import { devices, expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { AUTHOR, LEGACY, PORTS, REPO, WORK } from '../lib/env.ts';
import { loop } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player draws only while recording');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

const SHOTS = process.env.RETAKE_E2E_SHOTS ?? join(REPO, '.retake-work', 'review2-shots');
const PHONES = ['iPhone 13', 'iPhone 15'] as const;
const slug = (d: string) => d.toLowerCase().replace(/\s+/g, '');
const errors: string[] = [];

/** A microphone for the page: the container has none, and its WebKit has no audio encoder to record with. A quiet
 * tone through Web Audio as the MediaStream, and a recorder that hands out small mp4 chunks (this file checks the
 * layout while recording; the Chromium specs and 06-mobile's mp4 test check real recordings). */
function fakeMic(): void {
  const getUserMedia = async (): Promise<MediaStream> => {
    const ac = new AudioContext();
    const osc = ac.createOscillator();
    const gain = ac.createGain();
    gain.gain.value = 0.2;
    const dst = ac.createMediaStreamDestination();
    osc.connect(gain).connect(dst);
    osc.start();
    return dst.stream;
  };
  if (navigator.mediaDevices) navigator.mediaDevices.getUserMedia = getUserMedia;
  else Object.defineProperty(navigator, 'mediaDevices', { value: { getUserMedia }, configurable: true });
  {
    type Handler = ((e: { data?: Blob }) => void) | null;
    class FakeRecorder {
      static isTypeSupported(m: string): boolean { return m.startsWith('audio/mp4'); }
      mimeType = 'audio/mp4';
      state = 'inactive';
      onstart: Handler = null;
      onstop: Handler = null;
      onerror: Handler = null;
      ondataavailable: Handler = null;
      timer: ReturnType<typeof setInterval> | undefined;
      start(ms = 1000): void {
        this.state = 'recording';
        setTimeout(() => this.onstart?.({}));
        this.timer = setInterval(() => this.ondataavailable?.({ data: new Blob([new Uint8Array(64)], { type: 'audio/mp4' }) }), ms);
      }
      stop(): void {
        clearInterval(this.timer);
        this.state = 'inactive';
        setTimeout(() => {
          this.ondataavailable?.({ data: new Blob([new Uint8Array(16)], { type: 'audio/mp4' }) });
          this.onstop?.({});
        });
      }
    }
    (window as unknown as { MediaRecorder: unknown }).MediaRecorder = FakeRecorder;
  }
}

async function phonePage(browser: Browser, device: (typeof PHONES)[number]): Promise<{ context: BrowserContext; page: Page }> {
  const { defaultBrowserType: _, ...d } = devices[device];
  const context = await browser.newContext(d);
  const page = await context.newPage();
  page.on('pageerror', (e) => errors.push(`${device}: ${e.name}: ${e.message}`));
  return { context, page };
}

async function shot(page: Page, what: string, device: string): Promise<void> {
  mkdirSync(SHOTS, { recursive: true });
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(SHOTS, `${what}-${slug(device)}.png`) });
}

/** Nothing reaches past the screen's edges, and the page doesn't scroll sideways. */
async function fitsWidth(page: Page, where: string): Promise<void> {
  const out = await page.evaluate(() => {
    const w = innerWidth;
    const bad: string[] = [];
    if (document.documentElement.scrollWidth > w) bad.push(`html is ${document.documentElement.scrollWidth} wide`);
    if (visualViewport && Math.abs(visualViewport.width - w) > 1) bad.push(`visual viewport ${visualViewport.width}`);
    for (const el of document.querySelectorAll<HTMLElement>('body *')) {
      const s = getComputedStyle(el);
      if (s.display === 'none' || s.visibility === 'hidden') continue;
      const b = el.getBoundingClientRect();
      if (!b.width || !b.height || (b.right <= w + 1 && b.left >= -1)) continue;
      let ok = false;
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const o = getComputedStyle(p).overflowX;
        if (o !== 'auto' && o !== 'scroll') continue; // a box that only clips doesn't count
        const pb = p.getBoundingClientRect();
        if (pb.right <= w + 1 && pb.left >= -1) ok = true;
        break;
      }
      if (!ok) bad.push(`${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''}.${[...el.classList].join('.')} ${Math.round(b.left)}..${Math.round(b.right)}`);
    }
    return bad.slice(0, 10);
  });
  expect(out, `${where}: nothing past the screen's edge`).toEqual([]);
}

/** Each control is on screen (inside innerHeight and the visual viewport) and at least 40 px both ways. */
async function targets(page: Page, where: string, sels: string[]): Promise<void> {
  const view = await page.evaluate(() => ({ w: innerWidth, h: innerHeight, vv: visualViewport!.offsetTop + visualViewport!.height }));
  for (const sel of sels) {
    const el = page.locator(sel).first();
    await expect(el, `${where}: ${sel}`).toBeVisible();
    const b = (await el.boundingBox())!;
    expect(Math.min(b.width, b.height), `${where}: ${sel} is big enough to tap`).toBeGreaterThanOrEqual(40);
    expect(b.x, `${where}: ${sel} left`).toBeGreaterThanOrEqual(-0.5);
    expect(b.y, `${where}: ${sel} top`).toBeGreaterThanOrEqual(-0.5);
    expect(b.x + b.width, `${where}: ${sel} right`).toBeLessThanOrEqual(view.w + 0.5);
    expect(b.y + b.height, `${where}: ${sel} inside innerHeight`).toBeLessThanOrEqual(view.h + 0.5);
    expect(b.y + b.height, `${where}: ${sel} inside the visual viewport`).toBeLessThanOrEqual(view.vv + 0.5);
  }
}

/** A stroke with the mouse over the video, normalized to the pen's canvas. */
async function draw(page: Page, points: [number, number][]): Promise<void> {
  const b = (await page.locator('#ink').boundingBox())!;
  const P = points.map(([x, y]) => [b.x + x * b.width, b.y + y * b.height] as const);
  await page.mouse.move(P[0]![0], P[0]![1]);
  await page.mouse.down();
  for (const [x, y] of P.slice(1)) await page.mouse.move(x, y);
  await page.mouse.up();
}

const BAR = ['#play', '#verBtn', '#pen', '#undo', '#rec', '#done', '#sideToggle'];

/** The comments fill the row between the controls (#pbar) and Record and Send (#precord): no gap above or below them,
 * and the page itself doesn't scroll. */
async function fillsSpace(page: Page, where: string): Promise<void> {
  const g = await page.evaluate(() => {
    const r = (sel: string) => document.querySelector(sel)!.getBoundingClientRect();
    const side = r('#side'), pbar = r('#pbar'), rec = r('#precord');
    return { top: side.top, bottom: side.bottom, under: pbar.bottom, rec: rec.top, page: document.scrollingElement!.scrollHeight, h: innerHeight };
  });
  expect(Math.abs(g.top - g.under), `${where}: the comments start right under the controls`).toBeLessThanOrEqual(1.5);
  expect(Math.abs(g.rec - g.bottom), `${where}: and reach Record and Send`).toBeLessThanOrEqual(1.5);
  expect(g.bottom - g.top, `${where}: with room for the list`).toBeGreaterThan(200);
  expect(g.page, `${where}: the page doesn't scroll`).toBeLessThanOrEqual(g.h + 1);
}

const COMMENT_BOX = '#side [data-part~="comment-line"] textarea';
const TABS = ['#side .tabs .seg button >> nth=0', '#side .tabs .seg button >> nth=1'];
const EIGHT = [
  'Cut the first two seconds, the intro drags',
  'Too loud here',
  'Hold this shot a beat longer so the item reads before we move on to the next part of the build',
  'Zoom in on the shop panel',
  'Swap the music under this section for something calmer',
  'Typo in the caption',
  'This transition is too fast',
  'End on the scoreboard, not the menu',
];

test.describe('the review page on phones in WebKit', () => {
  let server: Server;

  test.beforeAll(async () => {
    const dir = join(WORK, 'review-webkit', 'videos');
    const config = join(WORK, 'review-webkit', 'config');
    const m = mediaLayout(WORK);
    // alpha: the walk through the controls; quiet: never a comment; busy: eight comments on v6.
    for (const name of ['alpha', 'quiet', 'busy']) {
      for (const [v, src] of [['v5', m.v5], ['v6', m.v6]] as const) {
        const to = join(dir, name, v, `${name}_${v}.mp4`);
        mkdirSync(dirname(to), { recursive: true });
        copyFileSync(src, to);
      }
    }
    const file = join(config, 'retake', 'config.toml');
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, `name = "${AUTHOR}"\nopen_browser = false\nfind_agents = false\n
[[agents]]\nid = "one"\nname = "Agent One"\ncommand = ["true"]\n
[[agents]]\nid = "two"\nname = "Agent Two"\ncommand = ["true"]\n`);
    server = await startServer('review-webkit', dir, PORTS.sidebar, [], { env: { XDG_CONFIG_HOME: config } });
    for (const [i, text] of EIGHT.entries()) {
      const r = await fetch(`${server.url}p/busy/api/notes`, { method: 'POST', body: JSON.stringify({ version: 'v6', t: 2 + i * 4, text }) });
      expect(r.status, `comment ${i + 1}`).toBe(201);
    }
  });
  test.afterAll(async () => {
    await server?.stop();
    expect(errors).toEqual([]);
  });

  for (const device of PHONES) {
    test(`${device}: the comments fill the space under the controls, with 0 comments and with 8`, async ({ browser }) => {
      const { context, page } = await phonePage(browser, device);
      try {
        // No comment yet: the list says how to make one, To do · Done and the comment box are there, nothing is empty.
        await page.goto(`${server.url}p/quiet/`);
        await page.waitForFunction(() => !!window.__review?.view?.shown && !!window.__review.notes?.available, null, { timeout: 20_000 });
        await expect(page.locator('#side')).toBeVisible();
        expect(await page.evaluate(() => window.__review.sidebar)).toMatchObject({ phone: true, panel: true, toggle: true });
        await expect(page.locator('#side .empty')).toContainText('No comments yet');
        await expect(page.locator('#side .tabs .seg button')).toHaveText(['To do 0', 'Done 0']);
        await fillsSpace(page, `${device}: 0 comments`);
        await fitsWidth(page, `${device}: 0 comments`);
        await targets(page, `${device}: 0 comments`, [...BAR, COMMENT_BOX, ...TABS]);
        await shot(page, 'comments-0', device);

        // Eight: the To do list scrolls inside its row; the bar and the box stay put.
        await page.goto(`${server.url}p/busy/`);
        await page.waitForFunction(() => !!window.__review?.view?.shown && !!window.__review.notes?.available, null, { timeout: 20_000 });
        await expect(page.locator('#side .item')).toHaveCount(8, { timeout: 10_000 });
        await expect(page.locator('#side .tabs .seg button')).toHaveText(['To do 8', 'Done 0']);
        await expect(page.locator('#sideToggle')).toHaveText('8');
        await expect(page.locator('#sideToggle')).toHaveAttribute('aria-label', 'Comments: 8 to do');
        await fillsSpace(page, `${device}: 8 comments`);
        await fitsWidth(page, `${device}: 8 comments`);
        await targets(page, `${device}: 8 comments`, [...BAR, COMMENT_BOX, ...TABS, '#side .item .more', '#side .item .time.jump']);
        const send = (await page.locator('#done').boundingBox())!;
        const list = await page.locator('#side .vp').evaluate((el) => ({ scroll: el.scrollHeight, client: el.clientHeight }));
        expect(list.scroll, `${device}: the list is longer than its row, so it scrolls`).toBeGreaterThan(list.client);
        await shot(page, 'comments-8', device);
        const last = page.locator('#side .item').last();
        await last.scrollIntoViewIfNeeded();
        await expect(last).toBeInViewport();
        const side = (await page.locator('#side').boundingBox())!;
        const lb = (await last.boundingBox())!;
        expect(lb.y + lb.height, `${device}: the last comment shows inside the comments`).toBeLessThanOrEqual(side.y + side.height + 0.5);
        expect(await page.locator('#done').boundingBox(), `${device}: Send stays put`).toEqual(send);
        await fillsSpace(page, `${device}: 8 comments, scrolled`);
        await shot(page, 'comments-8-scrolled', device);

        // The bubble goes to the comments (To do, from the top); it never hides them here.
        await page.locator('#side .tabs .seg button', { hasText: 'Done' }).tap();
        await expect(page.locator('#side .empty')).toContainText('Nothing done yet');
        await fillsSpace(page, `${device}: Done`);
        await page.locator('#sideToggle').tap();
        await expect(page.locator('#side')).toBeVisible();
        await expect(page.locator('#side .item')).toHaveCount(8);
        await expect.poll(() => page.locator('#side .vp').evaluate((el) => el.scrollTop)).toBe(0);
        await fillsSpace(page, `${device}: after the bubble`);
      } finally {
        await context.close();
      }
    });

    test(`${device}: the controls fit, the pen, a typed comment with a drawing, the version menu, recording`, async ({ browser }) => {
      const { context, page } = await phonePage(browser, device);
      try {
        await page.goto(`${server.url}p/alpha/`);
        await page.waitForFunction(() => !!window.__review?.view?.shown, null, { timeout: 20_000 });
        await expect(page.locator('#precord')).toBeVisible();
        await fitsWidth(page, `${device}: idle`);
        await targets(page, `${device}: idle`, BAR);
        await expect(page.locator('#done')).toHaveText('Send');
        await expect(page.locator('body')).not.toContainText('Agent One');
        await shot(page, 'idle', device);

        // The pen: its colours and the eraser, thumb sized, on screen.
        await page.locator('#pen').tap();
        await expect(page.locator('.pen-pop')).toBeVisible();
        await targets(page, `${device}: the pen's popover`, ['.sw[data-color=red]', '.sw[data-color=white]', '#eraser']);
        await fitsWidth(page, `${device}: the pen's popover`);
        await shot(page, 'pen-popover', device);
        await page.locator('.sw[data-color=yellow]').tap();
        await expect(page.locator('.pen-pop')).toHaveCount(0);
        await expect(page.locator('#pen')).toHaveAttribute('data-color', 'yellow');

        // A typed comment with a drawing, no recording: the comments open with the box at their bottom.
        await draw(page, [...loop(0.45, 0.5, 0.08)] as [number, number][]);
        await expect.poll(() => page.evaluate(() => window.__review.draft?.lines ?? 0)).toBe(1);
        expect(await page.evaluate(() => !!window.__review.seg)).toBe(false);
        const box = page.locator('#side [data-part~="comment-line"] textarea');
        await expect(box).toBeVisible();
        await box.fill(`Hold this shot (${device})`);
        await expect(page.locator('#side [data-part~="comment-line"] .under')).toContainText('1 drawing');
        await targets(page, `${device}: the comment's Save`, ['#side [data-part~="comment-line"] button[type=submit]']);
        await fitsWidth(page, `${device}: a typed comment`);
        await shot(page, 'typed-comment', device);
        await page.locator('#side [data-part~="comment-line"] button[type=submit]').tap();
        const card = page.locator('#side [data-part~="comment"]', { hasText: `Hold this shot (${device})` });
        await expect(card).toBeVisible({ timeout: 10_000 });
        await expect(card.locator('[data-part~="into"]')).toHaveText('→ v7');
        await expect.poll(() => page.evaluate(() => window.__review.draft)).toBeNull();

        // The comments' version menu.
        await page.locator('#side [data-part~="version-menu"]').tap();
        const list = page.locator('[data-part~="version-menu-list"]');
        await expect(list).toBeVisible();
        await targets(page, `${device}: the version menu`, ['[data-part~="version-menu-list"] [data-version="v5"]']);
        await fitsWidth(page, `${device}: the version menu`);
        await shot(page, 'version-menu', device);
        await page.keyboard.press('Escape');
        await expect(list).toHaveCount(0);

        // Recording: REC and the time; Send doesn't move; the pen is a tap away.
        await page.evaluate(fakeMic); // installed now: an init script's class fields don't survive the transpiler
        const send = (await page.locator('#done').boundingBox())!;
        await page.locator('#rec').tap();
        await expect
          .poll(() => page.evaluate(() => (window.__review.seg ? 'recording' : document.querySelector('#toast')?.textContent ?? '')), { timeout: 10_000 })
          .toBe('recording');
        await expect(page.locator('#recLabel')).toHaveText(/^REC \d:\d\d$/);
        expect(await page.locator('#done').boundingBox()).toEqual(send);
        await fitsWidth(page, `${device}: recording`);
        await targets(page, `${device}: recording`, ['#rec', '#done', '#pen']);
        await shot(page, 'recording', device);
        await page.locator('#rec').tap();
        await page.waitForFunction(() => !window.__review.seg, null, { timeout: 10_000 });

        // The home's Settings.
        await page.goto(`${server.url}#settings`);
        await expect(page.locator('[data-part~="settings"] h1')).toHaveText('Settings', { timeout: 20_000 });
        await fitsWidth(page, `${device}: settings`);
        await shot(page, 'settings', device);
      } finally {
        await context.close();
      }
    });
  }
});
