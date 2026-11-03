// Typing on a phone (spec/notes.md, "Typing on a phone"), for 23-keyboard.spec.ts (Chromium) and
// 23-keyboard-webkit.spec.ts (WebKit, the iPhone 13 and 15). No browser here can raise a real on-screen keyboard, so
// it is simulated the way the page sees one: the visual viewport's height (and offset, for a pan) stubbed, and its
// events sent. Then:
// - a video: the comment box sits on the keyboard, every other visible thing keeps its place, the rest is hidden, a
//   pan is followed, and leaving (a tap on the video, the keyboard's Done, Return) puts everything back, the list's
//   scroll too;
// - a script: the chat sheet sits on the keyboard with nothing behind it moving; typing in the document keeps the
//   caret's line above the keyboard, with the bar hidden.
import { expect, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from './fixtures.ts';
import { PORTS, REPO, WORK } from './env.ts';
import { scriptsConfig } from './scripts.ts';
import { startServer, type Server } from './server.ts';

export const SHOTS = join(REPO, '.retake-work', 'keyboard-shots');
/** An iPhone's keyboard over Safari's page (an iPhone 15: 659 px, 413 of them left above it). */
export const KB = 246;

const COMMENT_BOX = '#side [data-part~="comment-line"] textarea';
const COMPOSER = '#side [data-part~="comment-line"]';
/** What compose mode hides (spec/notes.md). */
const FADES = ['#pbar', '#precord', '#sideToggle', '[data-part~="review-back"]', '#side > header', '#side > .list'];

const COMMENTS = [
  'Cut the first two seconds, the intro drags',
  'Too loud here',
  'Hold this shot a beat longer so the item reads before we move on to the next part of the build',
  'Zoom in on the shop panel',
  'Swap the music under this section for something calmer',
  'Typo in the caption',
  'This transition is too fast',
  'End on the scoreboard, not the menu',
];

const PARA =
  'A product video that is its own joke. It opens on a film set after a take: a director gives an actor one tiny ' +
  'note, the set resets, the clapper claps, and the red dot blows up into the promo.';
/** A script long enough to reach under the keyboard. */
const DOC = `# Typing test\n\n${Array.from({ length: 8 }, (_, i) => `## Part ${i + 1}\n\n${PARA}\n`).join('\n')}`;

/** A server with a video with eight comments on v6 (`p/busy/`) and a script (`p/talk-promo/`). */
export async function keyboardServer(name: string): Promise<Server> {
  const cfg = scriptsConfig(name);
  const dir = join(WORK, name, 'videos');
  const m = mediaLayout(WORK);
  for (const [v, src] of [['v5', m.v5], ['v6', m.v6]] as const) {
    const to = join(dir, 'busy', v, `busy_${v}.mp4`);
    mkdirSync(dirname(to), { recursive: true });
    copyFileSync(src, to);
  }
  const doc = join(dir, 'talk', 'promo', 'v0', 'promo_v0.md');
  mkdirSync(dirname(doc), { recursive: true });
  writeFileSync(doc, DOC);
  const server = await startServer(name, dir, PORTS.keyboard, [], { env: { XDG_CONFIG_HOME: cfg.home } });
  for (const [i, text] of COMMENTS.entries()) {
    const r = await fetch(`${server.url}p/busy/api/notes`, {
      method: 'POST',
      body: JSON.stringify({ version: 'v6', t: 2 + i * 4, text }),
    });
    expect(r.status, `comment ${i + 1}`).toBe(201);
  }
  return server;
}

/** The keyboard comes up: the visual viewport is `kb` shorter, panned down by `top` (what the browser does to reveal
 * a field; the page undoes or follows it). */
export async function keyboard(page: Page, kb: number, top = 0): Promise<void> {
  await page.evaluate(
    ({ kb, top }) => {
      const vv = window.visualViewport!;
      const height = innerHeight - kb;
      Object.defineProperty(vv, 'height', { configurable: true, get: () => height });
      Object.defineProperty(vv, 'offsetTop', { configurable: true, get: () => top });
      vv.dispatchEvent(new Event(top ? 'scroll' : 'resize'));
    },
    { kb, top },
  );
}

/** The keyboard goes down: the visual viewport is the browser's own again. */
export async function keyboardDown(page: Page): Promise<void> {
  await page.evaluate(() => {
    const vv = window.visualViewport! as unknown as Record<string, unknown>;
    delete vv.height;
    delete vv.offsetTop;
    window.visualViewport!.dispatchEvent(new Event('resize'));
  });
}

type Box = [number, number, number, number];
type Places = Record<string, Box>;

/** Every element on screen (laid out, visible, not transparent), outside `skip`, with its box. Each element keeps a
 * key for the page's life, so two calls compare the same elements. */
async function places(page: Page, skip: string): Promise<Places> {
  return page.evaluate((skip) => {
    const w = window as unknown as { __kbKey?: number };
    const out: Record<string, [number, number, number, number]> = {};
    for (const el of document.querySelectorAll<HTMLElement>('body *')) {
      if (el.closest(skip) || el.closest('.kb-proxy, .ig-kb')) continue;
      const b = el.getBoundingClientRect();
      if (!b.width || !b.height) continue;
      if (el.checkVisibility && !el.checkVisibility({ opacityProperty: true, visibilityProperty: true })) continue;
      el.dataset.kbKey ??= String((w.__kbKey = (w.__kbKey ?? 0) + 1));
      const name = `${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''}${el.className && typeof el.className === 'string' ? `.${el.className.trim().split(/\s+/).join('.')}` : ''}`;
      out[`${el.dataset.kbKey} ${name}`] = [b.x, b.y, b.width, b.height].map((n) => Math.round(n * 10) / 10) as [
        number,
        number,
        number,
        number,
      ];
    }
    return out;
  }, skip);
}

/** What was on screen both times and moved (by more than half a pixel), with `dy` the whole page's expected shift. */
function moved(a: Places, b: Places, dy = 0): string[] {
  const out: string[] = [];
  for (const [k, p] of Object.entries(a)) {
    const q = b[k];
    if (!q) continue;
    if (Math.abs(p[0] - q[0]) > 0.5 || Math.abs(p[1] + dy - q[1]) > 0.5 || Math.abs(p[2] - q[2]) > 0.5 || Math.abs(p[3] - q[3]) > 0.5)
      out.push(`${k}: ${p.join(',')} → ${q.join(',')}`);
  }
  return out.slice(0, 12);
}

/** Every rect the same (within half a pixel) as before. */
function same(a: Places, b: Places): string[] {
  const out = moved(a, b);
  for (const k of Object.keys(a)) if (!b[k]) out.push(`${k}: gone`);
  for (const k of Object.keys(b)) if (!a[k]) out.push(`${k}: new`);
  return out.slice(0, 12);
}

const hidden = (page: Page, sel: string) =>
  page.locator(sel).first().evaluate((el) => {
    const s = getComputedStyle(el);
    return s.visibility === 'hidden' && s.opacity === '0';
  });

const viewport = (page: Page) =>
  page.evaluate(() => (window as unknown as { __review: { viewport: Record<string, unknown> } }).__review.viewport);

const bottomOf = (page: Page, sel: string) =>
  page.locator(sel).first().evaluate((el) => Math.round(el.getBoundingClientRect().bottom * 10) / 10);

const listScroll = (page: Page) => page.locator('#side .vp').evaluate((el) => el.scrollTop);

/** A video, in compose mode and out of it again. */
export async function videoKeyboard(page: Page, url: string, where: string, shot: (what: string) => Promise<void>): Promise<void> {
  await page.goto(`${url}p/busy/`);
  await page.waitForFunction(() => !!window.__review?.view?.shown && !!window.__review.notes?.available, null, { timeout: 20_000 });
  // The eight comments, and one per device run before on this server (each saves one with Return, below).
  const items = page.locator('#side .item');
  await expect.poll(() => items.count(), { timeout: 10_000 }).toBeGreaterThanOrEqual(COMMENTS.length);
  const count = await items.count();
  const inner = await page.evaluate(() => innerHeight);
  // The list scrolled part way, as someone reading it would have it.
  await page.locator('#side .vp').evaluate((el) => (el.scrollTop = 60));
  await expect.poll(() => listScroll(page)).toBeGreaterThan(0);
  const scroll = await listScroll(page);
  await page.waitForTimeout(300);
  const before = await places(page, '.kb-proxy');
  await shot('video');

  // The comment box takes the focus: compose mode.
  await page.locator(COMMENT_BOX).tap();
  await expect.poll(() => viewport(page).then((v) => v.compose), { message: `${where}: compose mode` }).toBe(true);
  await expect(page.locator('html')).toHaveAttribute('data-compose', '');
  expect(await page.evaluate(() => document.activeElement?.closest('[data-part~="comment-line"]') !== null)).toBe(true);
  // The keyboard comes up: the box sits on it.
  await keyboard(page, KB);
  await expect.poll(() => bottomOf(page, COMPOSER), { message: `${where}: the box sits on the keyboard` }).toBeCloseTo(inner - KB, 0);
  expect(await viewport(page)).toMatchObject({ kb: KB, up: true, typing: 'comment', compose: true, top: 0 });
  for (const sel of FADES) await expect.poll(() => hidden(page, sel), { message: `${where}: ${sel} is hidden` }).toBe(true);
  // Nothing else on screen moved (the video stays where it was, at the comment's frame).
  const composing = await places(page, COMPOSER);
  expect(moved(before, composing), `${where}: nothing outside the box moved`).toEqual([]);
  expect(Object.keys(composing).some((k) => k.includes('video#video')), `${where}: the video is still there`).toBe(true);
  expect(await page.evaluate(() => (document.querySelector('video') as HTMLVideoElement).paused)).toBe(true);
  await shot('video-keyboard');

  // The browser pans the visible area down by 100 px: the page follows it, so on screen nothing moves, and the box is
  // still on the keyboard.
  await keyboard(page, KB, 100);
  await expect.poll(() => viewport(page).then((v) => v.top)).toBe(100);
  await expect.poll(() => bottomOf(page, COMPOSER), { message: `${where}: on the keyboard after a pan` }).toBeCloseTo(100 + inner - KB, 0);
  expect(moved(composing, await places(page, COMPOSER), 100), `${where}: the page follows a pan`).toEqual([]);
  await keyboard(page, KB);
  await expect.poll(() => viewport(page).then((v) => v.top)).toBe(0);

  // A tap on the video ends it (and draws nothing); the keyboard goes down. Everything is back where it was.
  const ink = (await page.locator('#ink').boundingBox())!;
  await page.mouse.click(ink.x + ink.width / 2, ink.y + ink.height / 2);
  await expect.poll(() => viewport(page).then((v) => v.compose), { message: `${where}: a tap on the video ends it` }).toBe(false);
  await keyboardDown(page);
  for (const sel of FADES) await expect.poll(() => hidden(page, sel)).toBe(false);
  await page.waitForTimeout(300);
  expect(same(before, await places(page, '.kb-proxy')), `${where}: back where it was`).toEqual([]);
  expect(await listScroll(page), `${where}: the list keeps its scroll`).toBe(scroll);
  expect(await page.evaluate(() => window.__review.draft)).toBeNull();
  await shot('video-after');

  // The keyboard's Done (the field loses the focus): the same.
  await page.locator(COMMENT_BOX).tap();
  await keyboard(page, KB);
  await expect.poll(() => bottomOf(page, COMPOSER)).toBeCloseTo(inner - KB, 0);
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await keyboardDown(page);
  await expect.poll(() => viewport(page).then((v) => v.compose)).toBe(false);
  await page.waitForTimeout(300);
  expect(same(before, await places(page, '.kb-proxy')), `${where}: back after Done`).toEqual([]);
  expect(await listScroll(page)).toBe(scroll);

  // Return saves the comment and ends it.
  await page.locator(COMMENT_BOX).tap();
  await keyboard(page, KB);
  await page.locator(COMMENT_BOX).fill(`Hold on the logo (${where})`);
  await expect.poll(() => bottomOf(page, COMPOSER)).toBeCloseTo(inner - KB, 0);
  await page.locator(COMMENT_BOX).press('Enter');
  await expect.poll(() => viewport(page).then((v) => v.compose)).toBe(false);
  await keyboardDown(page);
  await expect(items).toHaveCount(count + 1, { timeout: 10_000 });
  for (const sel of FADES) await expect.poll(() => hidden(page, sel)).toBe(false);
}

const text = (page: Page) => page.locator('[data-ext="scripts"] [data-part="scripts-text"]');

/** A script: the chat sheet on the keyboard, and typing in the document. */
export async function scriptKeyboard(page: Page, url: string, where: string, shot: (what: string) => Promise<void>): Promise<void> {
  await page.goto(`${url}p/talk-promo/`);
  await expect(text(page)).toBeVisible({ timeout: 20_000 });
  await expect(page.locator('[data-part~="scripts-more"]')).toBeVisible({ timeout: 10_000 });
  const inner = await page.evaluate(() => innerHeight);
  await page.waitForTimeout(300);
  const before = await places(page, '#side');
  await shot('script');

  // The pill opens the chat sheet with its composer focused; the keyboard comes up under it.
  await page.locator('#precord [data-part="chat-pill"] .ask').tap();
  const sheet = page.locator('#side.drawer.chatting');
  await expect(sheet).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.activeElement?.getAttribute('aria-label'))).toBe('Ask the agent');
  expect((await viewport(page)).typing).toBe('chat');
  await keyboard(page, KB);
  await expect.poll(() => bottomOf(page, '#side.drawer.chatting'), { message: `${where}: the sheet sits on the keyboard` }).toBeCloseTo(inner - KB, 0);
  const top = await sheet.evaluate((el) => el.getBoundingClientRect().top);
  expect(top, `${where}: the sheet's top`).toBeCloseTo(64, 0);
  // Its composer is at its bottom, on the keyboard too.
  const composer = await bottomOf(page, '#side [data-part="chat-composer"]');
  expect(composer).toBeLessThanOrEqual(inner - KB + 0.5);
  expect(composer).toBeGreaterThan(inner - KB - 40);
  // Nothing behind it moved (the bar faded out under it).
  expect(moved(before, await places(page, '#side')), `${where}: nothing behind the sheet moved`).toEqual([]);
  await expect.poll(() => hidden(page, '#precord')).toBe(true);
  await shot('script-chat-keyboard');

  // Done: the keyboard goes down and the sheet reaches the bottom again.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await keyboardDown(page);
  await expect.poll(() => bottomOf(page, '#side.drawer.chatting')).toBeCloseTo(inner, 0);
  await page.locator('#side.drawer .icon.hide').tap();
  await expect(page.locator('#side.drawer')).toBeHidden();
  await expect.poll(() => hidden(page, '#precord')).toBe(false);
  expect(moved(before, await places(page, '#side')), `${where}: back after the chat`).toEqual([]);

  // Typing in the document (⋯ → Type in the script), read part way down: iOS raises the keyboard in that tap (the
  // stand-in), the page ends at it with the bar hidden, and the text stays where it was (the caret goes to the first
  // line in view, not the end). The page lays out for the keyboard remembered from the chat at once, before it is up
  // (spec/notes.md), so the text above it is all that can be tapped now.
  await page.locator('[data-ext="scripts"] .ig-scroll').evaluate((el) => (el.scrollTop = 400));
  const reading = () =>
    page.evaluate(() => {
      const l = [...document.querySelectorAll('[data-ext="scripts"] .cm-line')].find(
        (e) => /Part \d/.test(e.textContent ?? '') && e.getBoundingClientRect().top > 64,
      );
      return l ? `${/Part \d/.exec(l.textContent!)![0]} at ${Math.round(l.getBoundingClientRect().top)}` : null;
    });
  const read = await reading();
  expect(read).not.toBeNull();
  await page.locator('[data-part~="scripts-more"]').tap();
  await page.locator('[data-part="scripts-type"]').tap();
  await expect(text(page)).toHaveAttribute('contenteditable', 'true');
  await expect.poll(() => viewport(page).then((v) => v.typing), { message: `${where}: typing in the document` }).toBe('doc');
  await expect.poll(reading, { message: `${where}: Type in the script keeps the text in place` }).toBe(read);
  await keyboard(page, KB);
  await expect.poll(() => bottomOf(page, '#app'), { message: `${where}: the page ends at the keyboard` }).toBeCloseTo(inner - KB, 0);
  await expect.poll(() => hidden(page, '#precord'), { message: `${where}: the bar hides while typing` }).toBe(true);
  await page.waitForTimeout(100); // the caret keeping runs in the next frame
  expect(await reading(), `${where}: the keyboard coming up keeps the text in place`).toBe(read);
  // The keyboard's Done: the text stays editable, the bar is back.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await keyboardDown(page);
  await expect.poll(() => hidden(page, '#precord')).toBe(false);
  await expect.poll(() => bottomOf(page, '#app')).toBeCloseTo(inner, 0);
  await expect(text(page)).toHaveAttribute('contenteditable', 'true');
  // Then a tap in the text low on the screen, under where the keyboard comes (above the bar): the page ends at the
  // keyboard and the caret is above it.
  const box = (await text(page).boundingBox())!;
  const low = await page.evaluate(
    ({ x, from, to }) => {
      for (let y = from; y > to; y -= 4) if (document.elementFromPoint(x, y)?.closest('.cm-line')) return { x, y };
      return null;
    },
    { x: box.x + 24, from: inner - 120, to: inner - KB + 20 },
  );
  expect(low, `${where}: text under where the keyboard comes`).not.toBeNull();
  await page.mouse.click(low!.x, low!.y);
  await expect.poll(() => viewport(page).then((v) => v.typing), { message: `${where}: the tap types in the document` }).toBe('doc');
  await keyboard(page, KB);
  await expect.poll(() => bottomOf(page, '#app'), { message: `${where}: the page ends at the keyboard again` }).toBeCloseTo(inner - KB, 0);
  await expect.poll(() => hidden(page, '#precord'), { message: `${where}: the bar hides again` }).toBe(true);
  /** The caret's box: its line of text (a paragraph's line, not the whole paragraph). */
  const caret = () =>
    page.evaluate(() => {
      const s = getSelection();
      if (!s?.rangeCount) return null;
      const r = s.getRangeAt(0).cloneRange();
      r.collapse(false);
      let b: DOMRect | null = r.getClientRects()[0] ?? null;
      if (!b?.height) {
        const n = s.focusNode;
        b = (n instanceof Element ? n : n?.parentElement)?.closest('.cm-line')?.getBoundingClientRect() ?? null;
      }
      return b ? { top: b.top, bottom: b.bottom } : null;
    });
  await expect
    .poll(async () => (await caret())?.bottom ?? Infinity, { message: `${where}: the caret is above the keyboard` })
    .toBeLessThanOrEqual(inner - KB);
  expect((await caret())!.top).toBeGreaterThanOrEqual(0);
  await shot('script-doc-keyboard');
  // The keyboard's Done: the bar is back, the page is whole again.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await keyboardDown(page);
  await expect.poll(() => hidden(page, '#precord')).toBe(false);
  await expect.poll(() => bottomOf(page, '#app')).toBeCloseTo(inner, 0);
}
