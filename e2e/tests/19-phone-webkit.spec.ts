// The phone layout in WebKit, iPhone Safari's engine (the `webkit` project: RETAKE_E2E_WEBKIT, lib/webkit.ts), on the
// iPhone 13 and iPhone 15 viewports (Safari's toolbars already taken off their height):
// - an Scripts document (v0) with a long paragraph, long words, headings, a list and a task, and a plain video;
// - nothing is wider than the screen (no sideways scroll, no element past the right edge);
// - Record and Send are fully on screen (inside innerHeight and the visual viewport);
// - the top bar fits: back on the left, the chat's toggle and the document's ⋯ on the right, none overlapping;
// - the chat (spec/chat.md): the bar holds its pill and Send; the pill opens the chat sheet with its composer focused,
//   over the bar; a message gets the (fake) agent's reply with its picture choices stacked; the sheet closes again;
// - the home fits too.
// Screenshots go to .retake-work/scripts-shots/webkit-<what>-<device>.png. Under Chromium the file skips.
import { devices, expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { scriptsConfig } from '../lib/scripts.ts';
import { LEGACY, PORTS, REPO, WORK } from '../lib/env.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no extensions');
test.skip(({ browserName }) => browserName !== 'webkit', 'WebKit only: set RETAKE_E2E_WEBKIT (e2e/README.md)');

const SHOTS = join(REPO, '.retake-work', 'scripts-shots');
const PHONES = ['iPhone 13', 'iPhone 15'] as const;
const slug = (d: string) => d.toLowerCase().replace(/\s+/g, '');

/** A plan as the owner writes one: long lines, a long unbroken word, a link, headings, a list and a task. */
const DOC = `# Retake — "CUT"

A product video for Retake that is its own joke. It opens on a film set after a take: a director gives an actor one tiny note ("hold that final stare a split second longer"), the set resets, the clapper claps, and the clapper's red dot blows up into the promo. The promo is Retake editing *this very video*: the ad plays inside Retake while we circle things, talk notes ("make this shorter", "make this visual"), send them to the agent, and the next take streams in. The agent even asks how much longer to hold the stare.

Supercalifragilisticexpialidocious-antidisestablishmentarianism-pneumonoultramicroscopic and https://example.com/a/very/long/path/that/never/breaks/anywhere/at/all/ever

## Outline

- Cold open: the set, the note, the clap.
- The trick: why timing matters, said once, plainly, without any jargon at all.
- [ ] Film the clapper close up

## Script

### Cold open

[The set.] So the idea is the jump.
`;

const errors: string[] = [];

async function phonePage(browser: Browser, device: (typeof PHONES)[number]): Promise<{ context: BrowserContext; page: Page }> {
  const { defaultBrowserType: _, ...d } = devices[device];
  const context = await browser.newContext(d);
  const page = await context.newPage();
  page.on('pageerror', (e) => errors.push(`${device}: ${e.name}: ${e.message}`));
  return { context, page };
}

async function shot(page: Page, what: string, device: string): Promise<void> {
  mkdirSync(SHOTS, { recursive: true });
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(SHOTS, `webkit-${what}-${slug(device)}.png`) });
}

/** Nothing reaches past the screen's right or left edge, and the page doesn't scroll sideways. */
async function fitsWidth(page: Page, where: string): Promise<void> {
  const out = await page.evaluate(() => {
    const w = innerWidth;
    const bad: string[] = [];
    const root = document.documentElement;
    if (root.scrollWidth > w) bad.push(`html is ${root.scrollWidth} wide (screen ${w})`);
    if (document.body.scrollWidth > w) bad.push(`body is ${document.body.scrollWidth} wide (screen ${w})`);
    if (visualViewport && Math.abs(visualViewport.width - w) > 1) bad.push(`visual viewport ${visualViewport.width}`);
    for (const el of document.querySelectorAll<HTMLElement>('body *')) {
      const s = getComputedStyle(el);
      if (s.display === 'none' || s.visibility === 'hidden') continue;
      const b = el.getBoundingClientRect();
      if (!b.width || !b.height || (b.right <= w + 1 && b.left >= -1)) continue;
      // Inside a box that scrolls sideways and is itself on screen: it scrolls into view. One that only clips doesn't
      // count (what it cuts off is still cut off), except the text editor's own lines, which wrap.
      let ok = false;
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const o = getComputedStyle(p).overflowX;
        if (o !== 'auto' && o !== 'scroll') continue;
        const pb = p.getBoundingClientRect();
        if (pb.right <= w + 1 && pb.left >= -1) ok = true;
        break;
      }
      if (!ok)
        bad.push(
          `${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''}.${[...el.classList].join('.')} ${Math.round(b.left)}..${Math.round(b.right)}`,
        );
    }
    return bad.slice(0, 10);
  });
  expect(out, `${where}: nothing past the screen's edge`).toEqual([]);
}

/** Record and Send (the bottom bar) are wholly on screen: inside the layout and the visual viewport. */
async function barOnScreen(page: Page, where: string): Promise<void> {
  const r = await page.evaluate(() => {
    const bar = [...document.querySelectorAll<HTMLElement>('#precord')].find((e) => e.getBoundingClientRect().height);
    if (!bar) return null;
    const b = bar.getBoundingClientRect();
    const vv = visualViewport!;
    const parts = [...bar.querySelectorAll<HTMLElement>('#rec, #done, [data-part="chat-pill"]')].map(
      (e) => e.getBoundingClientRect().bottom,
    );
    return { top: b.top, bottom: b.bottom, parts, inner: innerHeight, vv: vv.offsetTop + vv.height };
  });
  expect(r, `${where}: the bottom bar is there`).not.toBeNull();
  expect(r!.top, `${where}: the bar's top`).toBeGreaterThanOrEqual(0);
  expect(r!.bottom, `${where}: the bar inside innerHeight`).toBeLessThanOrEqual(r!.inner + 0.5);
  expect(r!.bottom, `${where}: the bar inside the visual viewport`).toBeLessThanOrEqual(r!.vv + 0.5);
  for (const p of r!.parts) expect(p, `${where}: Record and Send above the bar's bottom`).toBeLessThanOrEqual(r!.bottom);
}

/** The top bar: each control inside the screen, thumb sized, and none on top of another. */
async function topBarFits(page: Page, where: string, sels: string[]): Promise<void> {
  const boxes: { sel: string; x: number; y: number; w: number; h: number }[] = [];
  const width = page.viewportSize()!.width;
  for (const sel of sels) {
    const el = page.locator(sel).first();
    if (!(await el.count()) || !(await el.isVisible())) continue;
    const b = (await el.boundingBox())!;
    boxes.push({ sel, x: b.x, y: b.y, w: b.width, h: b.height });
    expect(b.x, `${where}: ${sel} starts on screen`).toBeGreaterThanOrEqual(0);
    expect(b.x + b.width, `${where}: ${sel} ends on screen`).toBeLessThanOrEqual(width + 0.5);
    expect(Math.min(b.width, b.height), `${where}: ${sel} is big enough to tap`).toBeGreaterThanOrEqual(40);
    expect(b.y, `${where}: ${sel} is at the top`).toBeLessThan(80);
  }
  for (let i = 0; i < boxes.length; i++) {
    for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i]!,
        b = boxes[j]!;
      const apart = a.x + a.w <= b.x + 0.5 || b.x + b.w <= a.x + 0.5 || a.y + a.h <= b.y + 0.5 || b.y + b.h <= a.y + 0.5;
      expect(apart, `${where}: ${a.sel} and ${b.sel} overlap`).toBe(true);
    }
  }
}

test.describe('phones in WebKit', () => {
  let server: Server;

  test.beforeAll(async () => {
    const cfg = scriptsConfig('webkit-phone');
    const dir = join(WORK, 'webkit-phone', 'videos');
    const video = join(dir, 'deadlock', 'item-shop', 'item-shop_v1.mp4');
    mkdirSync(dirname(video), { recursive: true });
    copyFileSync(mediaLayout(WORK).v5, video);
    const doc = join(dir, 'deadlock', 'promo', 'v0', 'promo_v0.md');
    mkdirSync(dirname(doc), { recursive: true });
    writeFileSync(doc, DOC);
    server = await startServer('webkit-phone', dir, PORTS.scripts, [], { env: { XDG_CONFIG_HOME: cfg.home } });
  });
  test.afterAll(async () => {
    await server?.stop();
    expect(errors).toEqual([]);
  });

  for (const device of PHONES) {
    test(`${device}: the document, a video and the home fit the screen`, async ({ browser }) => {
      const { context, page } = await phonePage(browser, device);
      try {
        // The Scripts document (v0): the editor from the top down to Record and Send.
        await page.goto(`${server.url}p/deadlock-promo/`);
        const text = page.locator('[data-ext="scripts"] [data-part~="scripts-text"]');
        await expect(text).toBeVisible({ timeout: 20_000 });
        await expect(page.locator('[data-part~="scripts-more"]')).toBeVisible({ timeout: 10_000 });
        await expect(page.locator('#sideToggle')).toBeVisible();
        await fitsWidth(page, `${device}: the document`);
        await barOnScreen(page, `${device}: the document`);
        await topBarFits(page, `${device}: the document`, [
          '[data-part~="review-back"]',
          '#sideToggle',
          '[data-part~="scripts-more"]',
        ]);
        // The ⋯ is at the right edge, the comments toggle just left of it.
        const more = (await page.locator('[data-part~="scripts-more"]').boundingBox())!;
        const toggle = (await page.locator('#sideToggle').boundingBox())!;
        const width = page.viewportSize()!.width;
        expect(width - (more.x + more.width)).toBeLessThanOrEqual(16);
        expect(toggle.x + toggle.width).toBeLessThanOrEqual(more.x);
        await shot(page, 'doc', device);
        // Further down: the list with its bullets in the hanging indent, the task's box, the headings.
        await page.locator('[data-ext="scripts"] .cm-line.ig-h2', { hasText: 'Outline' }).scrollIntoViewIfNeeded();
        await expect(page.locator('[data-ext="scripts"] [data-part="scripts-task"]')).toBeVisible();
        await fitsWidth(page, `${device}: the document's list`);
        await shot(page, 'doc-list', device);
        await page.locator('[data-ext="scripts"] .ig-scroll').evaluate((el) => (el.scrollTop = 0));

        // The chat: its pill and Send in the bar; the pill opens the sheet with the composer focused, over the bar.
        const pill = page.locator('#precord [data-part="chat-pill"]');
        await expect(pill).toBeVisible();
        await shot(page, 'chat-closed', device);
        await pill.locator('.ask').tap();
        const sheet = page.locator('#side.drawer.chatting');
        await expect(sheet).toBeVisible();
        await expect.poll(() => page.evaluate(() => document.activeElement?.getAttribute('aria-label'))).toBe(
          'Ask the agent',
        );
        await fitsWidth(page, `${device}: the chat sheet`);
        const box = (await sheet.boundingBox())!;
        expect(box.y + box.height, `${device}: the sheet reaches the bottom`).toBeGreaterThanOrEqual(
          (await page.evaluate(() => innerHeight)) - 1,
        );
        await shot(page, 'chat-open', device);
        // A message, and the agent's reply with its two pictures to choose from, stacked.
        await page.locator('#side [data-part="chat-composer"] textarea').fill(`Give me two openings (${device})`);
        await page.locator('#side [data-part="chat-composer"] textarea').press('Enter');
        const q = page.locator('#side [data-part="chat-question"]').last();
        await expect(q).toContainText('Which opening?', { timeout: 60_000 });
        await expect(q.locator('.choice')).toHaveCount(2);
        const xs = await q.locator('.choice').evaluateAll((els) => els.map((e) => e.getBoundingClientRect().x));
        expect(Math.abs(xs[0]! - xs[1]!)).toBeLessThan(1);
        await page.locator('#side [data-part="chat-composer"] textarea').blur();
        await fitsWidth(page, `${device}: the chat with a question`);
        await shot(page, 'chat-question', device);
        await page.locator('#side.drawer .icon.hide').tap();
        await expect(page.locator('#side.drawer')).toBeHidden();
        await barOnScreen(page, `${device}: the document after the chat`);
        await topBarFits(page, `${device}: the document after the chat`, [
          '[data-part~="review-back"]',
          '#sideToggle',
          '[data-part~="scripts-more"]',
        ]);

        // A plain video.
        await page.goto(`${server.url}p/deadlock-item-shop/`);
        await page.waitForFunction(() => !!window.__review?.view?.shown, null, { timeout: 20_000 });
        await expect(page.locator('#precord')).toBeVisible();
        await fitsWidth(page, `${device}: a video`);
        await barOnScreen(page, `${device}: a video`);
        await topBarFits(page, `${device}: a video`, ['[data-part~="review-back"]', '[data-part~="video-menu"]', '#sideToggle']);
        await shot(page, 'video', device);

        // The home.
        await page.goto(server.url);
        await expect(page.locator('[data-part="home"]')).toBeVisible({ timeout: 20_000 });
        await fitsWidth(page, `${device}: the home`);
        await shot(page, 'home', device);
      } finally {
        await context.close();
      }
    });
  }
});
