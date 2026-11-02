// Embeds in a script's document (spec/scripts.md, "Embeds"), for 22-embeds and 22-embeds-webkit: a document with a
// YouTube address, an unknown page (a card), an image, a link in a sentence, a raw <iframe> and a Twitch clip, and the
// checks both browsers run. No site is ever loaded: every request off the test server is answered here (a plain stub
// page, or a generated picture), and so is the server's link preview, so the card's words are known.
import { expect, type BrowserContext, type Locator, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { REPO, WORK } from './env.ts';
import { scriptsConfig } from './scripts.ts';

export const SHOTS = join(REPO, '.retake-work', 'embeds-shots');

export const YOUTUBE = 'https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42';
export const YOUTUBE_SRC = 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?start=42';
export const PAGE = 'https://example.com/blog/cold-opens';
export const INLINE = 'https://example.com/inline-link';
export const RAW_SRC = 'https://player.example.net/embed/42';
export const CLIP = 'https://clips.twitch.tv/AwkwardHelplessSalamander';
export const CARD = { title: 'Cold opens that hold on', site: 'Example Blog', image: 'https://img.example/card.png' };
/** Every frame's attributes, and no others (spec/scripts.md, "Every frame"). */
export const FRAME_ATTRS = {
  sandbox: 'allow-scripts allow-same-origin allow-popups allow-presentation',
  allow: 'fullscreen; picture-in-picture; encrypted-media',
  referrerpolicy: 'strict-origin-when-cross-origin',
  loading: 'lazy',
};

export const DOC = `# References

${YOUTUBE}

<${PAGE}>

![The set at night](images/set.png)

The cold open borrows its timing from the clip above; the short version is at ${INLINE} if you need it.

<iframe src="${RAW_SRC}" width="400" height="300" title="A player" onload="alert(1)" style="position:fixed;inset:0" allow="camera; microphone" sandbox="allow-top-navigation"></iframe>

${CLIP}

## Script

### Cold open

[The set.] So the idea is the jump.
`;

/** A folder with one project and one script (deadlock/refs/v0/refs_v0.md, and its picture), and Scripts' config. */
export function embedsFolder(name: string): { dir: string; home: string } {
  const cfg = scriptsConfig(name);
  const dir = join(WORK, name, 'videos');
  const doc = join(dir, 'deadlock', 'refs', 'v0', 'refs_v0.md');
  mkdirSync(join(dirname(doc), 'images'), { recursive: true });
  writeFileSync(doc, DOC);
  picture(join(dirname(doc), 'images', 'set.png'), 'testsrc2=size=960x540');
  return { dir, home: cfg.home };
}

/** A generated still (ffmpeg's test pattern), never real media. */
export function picture(out: string, source: string): void {
  mkdirSync(dirname(out), { recursive: true });
  execFileSync('nice', ['-n', '19', 'ffmpeg', '-v', 'error', '-y', '-threads', '1', '-f', 'lavfi', '-i', source,
    '-frames:v', '1', out]);
}

/**
 * Answers every request that leaves the test server: a stub page for a frame or a tapped card, the generated picture
 * for an image, and the link preview after a moment (so the card is seen before and after it comes). `seen` gets each
 * outside address asked for.
 */
export async function stubTheWeb(context: BrowserContext, previewDelay = 700): Promise<string[]> {
  const seen: string[] = [];
  const png = join(WORK, 'embeds-card.png');
  picture(png, 'color=c=0x3a5a8c:s=640x360');
  const pic = readFileSync(png);
  await context.route(
    (u) => u.hostname !== '127.0.0.1' && u.hostname !== 'localhost',
    (route) => {
      const u = new URL(route.request().url());
      seen.push(u.href);
      if (u.hostname === 'img.example') return route.fulfill({ status: 200, contentType: 'image/png', body: pic });
      return route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: `<!doctype html><title>stub</title><body style="margin:0;display:grid;place-items:center;height:100vh;` +
          `background:#1c1d21;color:#9a9ca3;font:13px system-ui">${u.hostname} (a stub: no site is loaded in tests)</body>`,
      });
    },
  );
  await context.route('**/api/link-preview?**', async (route) => {
    const url = new URL(route.request().url()).searchParams.get('url') ?? '';
    await new Promise((r) => setTimeout(r, previewDelay));
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ url, ...CARD, description: '' }) });
  });
  return seen;
}

/** The document's text area, once it shows. */
export async function openDoc(page: Page, url: string): Promise<Locator> {
  await page.goto(url);
  const text = page.locator('[data-ext="scripts"] [data-part~="scripts-text"]');
  await expect(text).toBeVisible({ timeout: 20_000 });
  return text;
}

/** The document's scroller back at its top. */
export async function toTop(page: Page): Promise<void> {
  await page.locator('[data-ext="scripts"] .ig-scroll').evaluate((el) => (el.scrollTop = 0));
  await page.waitForTimeout(150);
}

/** Scrolls the document down until `sel` is drawn: the editor draws only what's near the screen. */
export async function reveal(page: Page, sel: Locator): Promise<void> {
  const scroller = page.locator('[data-ext="scripts"] .ig-scroll');
  for (let i = 0; i < 40 && !(await sel.count()); i++) {
    await scroller.evaluate((el) => (el.scrollTop += el.clientHeight * 0.5));
    await page.waitForTimeout(120);
  }
  await sel.first().scrollIntoViewIfNeeded();
}

/** The frames: each one's address and attributes, the YouTube box at 16:9 from the start, the raw frame rebuilt. */
export async function checkFrames(page: Page): Promise<void> {
  const yt = page.locator('[data-part="scripts-embed"][data-site="YouTube"]');
  await expect(yt).toHaveCount(1);
  const frame = yt.locator('iframe');
  expect(await frame.evaluate((f) => f.getAttributeNames().sort())).toEqual(
    ['allow', 'loading', 'referrerpolicy', 'sandbox', 'src', 'title'],
  );
  await expect(frame).toHaveAttribute('src', YOUTUBE_SRC);
  for (const [k, v] of Object.entries(FRAME_ATTRS)) await expect(frame).toHaveAttribute(k, v);
  await expect(frame).toHaveAttribute('title', 'YouTube video');
  const box = (await yt.locator('.ig-embed-box').boundingBox())!;
  expect(box.width / box.height).toBeCloseTo(16 / 9, 1);

  // The raw frame: only its address, size and title; its own allow, sandbox, style and onload are gone.
  const raw = page.locator('[data-part="scripts-embed"][data-site="player.example.net"] iframe');
  await reveal(page, raw);
  await expect(raw).toHaveCount(1);
  expect(await raw.evaluate((f) => f.getAttributeNames().sort())).toEqual(
    ['allow', 'loading', 'referrerpolicy', 'sandbox', 'src', 'title'],
  );
  await expect(raw).toHaveAttribute('src', RAW_SRC);
  await expect(raw).toHaveAttribute('title', 'A player');
  for (const [k, v] of Object.entries(FRAME_ATTRS)) await expect(raw).toHaveAttribute(k, v);
  const rb = (await raw.boundingBox())!;
  expect(rb.width / rb.height).toBeCloseTo(4 / 3, 1);

  // Twitch names this page's host as its parent.
  const host = new URL(page.url()).hostname;
  await reveal(page, page.locator('[data-part="scripts-embed"][data-site="Twitch"]'));
  await expect(page.locator('[data-part="scripts-embed"][data-site="Twitch"] iframe')).toHaveAttribute(
    'src',
    `https://clips.twitch.tv/embed?clip=AwkwardHelplessSalamander&parent=${host}`,
  );
  // The link in a sentence stays a link: no embed of it.
  await expect(page.locator(`[data-part="scripts-card"] a[href="${INLINE}"]`)).toHaveCount(0);
  await expect(page.locator('[data-ext="scripts"] .ig-a', { hasText: INLINE })).toHaveCount(1);
}

/** The card: the address first, its title once the preview comes, in a box that doesn't move; a new tab on a tap. */
export async function checkCard(page: Page): Promise<Locator> {
  const card = page.locator('[data-part="scripts-card"]');
  await expect(card).toHaveCount(1);
  const a = card.locator('a.ig-card');
  await expect(a).toHaveAttribute('href', PAGE);
  await expect(a).toHaveAttribute('target', '_blank');
  await expect(a).toHaveAttribute('rel', 'noopener noreferrer');
  // Before the preview: the host and the path, in the card's own box.
  await expect(card.locator('.ig-card-title')).toHaveText('example.com/blog/cold-opens');
  const image = page.locator('[data-part="scripts-image"]').first();
  const before = { card: (await card.boundingBox())!, image: (await image.boundingBox())! };
  await expect(card).toHaveAttribute('data-loaded', 'true', { timeout: 10_000 });
  await expect(card.locator('.ig-card-title')).toHaveText(CARD.title);
  await expect(card.locator('.ig-card-site')).toHaveText(CARD.site);
  await expect(card.locator('.ig-card-pic img')).toHaveAttribute('src', CARD.image);
  await expect.poll(() => card.locator('.ig-card-pic img').evaluate((i: HTMLImageElement) => i.naturalWidth)).toBeGreaterThan(0);
  const after = { card: (await card.boundingBox())!, image: (await image.boundingBox())! };
  expect(after.card, 'the card keeps its box').toEqual(before.card);
  expect(after.image.y, 'what follows the card doesn’t move').toBe(before.image.y);
  return a;
}

/** Nothing reaches past the screen's edges, and the page doesn't scroll sideways (19-phone-webkit's check). */
export async function fitsWidth(page: Page, where: string): Promise<void> {
  const out = await page.evaluate(() => {
    const w = innerWidth;
    const bad: string[] = [];
    const root = document.documentElement;
    if (root.scrollWidth > w) bad.push(`html is ${root.scrollWidth} wide (screen ${w})`);
    if (document.body.scrollWidth > w) bad.push(`body is ${document.body.scrollWidth} wide (screen ${w})`);
    for (const el of document.querySelectorAll<HTMLElement>('body *')) {
      const s = getComputedStyle(el);
      if (s.display === 'none' || s.visibility === 'hidden') continue;
      const b = el.getBoundingClientRect();
      if (!b.width || !b.height || (b.right <= w + 1 && b.left >= -1)) continue;
      let ok = false;
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const o = getComputedStyle(p).overflowX;
        if (o !== 'auto' && o !== 'scroll') continue;
        const pb = p.getBoundingClientRect();
        if (pb.right <= w + 1 && pb.left >= -1) ok = true;
        break;
      }
      if (!ok) bad.push(`${el.tagName.toLowerCase()}.${[...el.classList].join('.')} ${Math.round(b.left)}..${Math.round(b.right)}`);
    }
    for (const f of document.querySelectorAll('iframe')) {
      const b = f.getBoundingClientRect();
      if (b.width > w) bad.push(`an iframe ${Math.round(b.width)} wide`);
    }
    return bad.slice(0, 10);
  });
  expect(out, `${where}: nothing past the screen's edge`).toEqual([]);
}

/** Every outside address asked for was a frame's, the card's picture or the tapped card: nothing else left. */
export function onlyExpectedOutside(seen: string[]): void {
  const allowed = [YOUTUBE_SRC, RAW_SRC, CARD.image, PAGE];
  for (const u of seen) {
    expect(allowed.some((a) => u === a) || u.startsWith('https://clips.twitch.tv/embed?'), `asked for ${u}`).toBe(true);
  }
}

export async function shot(page: Page, name: string): Promise<string> {
  mkdirSync(SHOTS, { recursive: true });
  const path = join(SHOTS, `${name}.png`);
  await page.waitForTimeout(400);
  await page.screenshot({ path });
  return path;
}
