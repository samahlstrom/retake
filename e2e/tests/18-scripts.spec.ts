// Scripts (spec/scripts.md), turned on with `retake ext enable scripts`, with a fake transcriber (a command engine
// that prints fixed words) and a fake agent (e2e/lib/scripts.ts). v0 is one document, v0/<video>_v0.md, in one plain
// editor: CodeMirror drawing its Markdown as a live preview. Locally (`retake open <folder>`) and hosted (`retake serve
// --extensions scripts` and `retake connect`):
// - New idea opens the one document, typing saves, and an outside edit shows. Headings are drawn larger than the text,
//   their #s hidden off the caret's line; a task's box ticks `[ ]` to `[x]` in the file; a pasted image is kept in
//   v0/images/ and shows.
// - The comments column is the chat with the agent (spec/chat.md). A voice message (R) is saved, transcribed and sent;
//   the agent (a fake that prints Claude Code's stream-json) writes the outline and the script into the document while
//   the page shows it, and asks "Which opening?" with two pictures; a tap answers it and the agent writes it in; ⋯
//   undoes the agent's edit; a link typed in the chat goes in alone on its line. The agent answers in plain words (no
//   "Agent" label), and each turn has ONE activity line: live while it runs ("Running a command…", the words streaming
//   in under it), then a summary ("Edited the script · read example.org/…"). The column's left edge drags it wider,
//   remembered after a reload; a double-click resets it. Scrollbars are thin and drawn from the theme's tokens.
// - Send makes the video: with no cut yet the agent builds v1 from the script ("Building v1…"); v1 opens, with
//   `Script` last in the version menu; back on the script, Send is dimmed with "Nothing changed since the last Send".
// - A multi-file v0 from before (idea.md, outline.md, script.md) becomes one document and the old files stay.
// On phones (390×844 and 430×932) nothing is wider than the screen, the text is at least 17 px and the controls are
// thumb-sized; the bar holds the chat's pill and Send, the pill opens the chat sheet with its composer focused, and
// tapping the document never raises the keyboard until ⋯ → Type in the script. Screenshots go to .retake-work/scripts-shots/ (<engine>-<what>-<size>.png).
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { scriptsConfig, WORDS, type ScriptsConfig } from '../lib/scripts.ts';
import { LEGACY, PORTS, REPO, RETAKE_BIN, WORK } from '../lib/env.ts';
import { startDaemon, startServer, startTeamServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no extensions');

const SHOTS = join(REPO, '.retake-work', 'scripts-shots');
const ENGINE = 'chromium';
const pageErrors: string[] = [];

/** The phones the layout is checked on. */
const PHONES = [
  { width: 390, height: 844 },
  { width: 430, height: 932 },
] as const;
type Size = { width: number; height: number };

const OLD_IDEA = '# Old idea\n\nThe idea, from before.\n';
const OLD_OUTLINE = '- Cold open\n- The trick\n';
const OLD_SCRIPT = '### Cold open\n\nThe jump.\n';

/** A folder with one project, deadlock, that holds a video (where new ideas go) and a v0 from before it was one file. */
function videos(name: string): string {
  const dir = join(WORK, name, 'videos');
  const dst = join(dir, 'deadlock', 'item-shop', 'item-shop_v1.mp4');
  mkdirSync(dirname(dst), { recursive: true });
  copyFileSync(mediaLayout(WORK).v5, dst);
  const old = join(dir, 'deadlock', 'old-idea', 'v0');
  mkdirSync(old, { recursive: true });
  writeFileSync(join(old, 'idea.md'), OLD_IDEA);
  writeFileSync(join(old, 'outline.md'), OLD_OUTLINE);
  writeFileSync(join(old, 'script.md'), OLD_SCRIPT);
  return dir;
}

async function newPage(browser: Browser, phone: Size | null = null): Promise<{ context: BrowserContext; page: Page }> {
  const context = phone
    ? await browser.newContext({ viewport: phone, isMobile: true, hasTouch: true, deviceScaleFactor: 2 })
    : await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', (e) => pageErrors.push(`${e.name}: ${e.message}`));
  return { context, page };
}

async function shot(page: Page, name: string, size?: Size): Promise<void> {
  mkdirSync(SHOTS, { recursive: true });
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(SHOTS, `${ENGINE}-${name}-${size ? size.width : 'desktop'}.png`) });
}

/** The editor's text (CodeMirror's contenteditable). */
const text = (page: Page) => page.locator('[data-ext="scripts"] [data-part="scripts-text"]');
type Hook = { scripts: { text(): string } };
/** The document as the editor holds it (the file's text), through the editor's test hook. */
const value = (page: Page) => text(page).evaluate((el) => (el as unknown as Hook).scripts.text());
/** The editor shows this text (or text matching it). */
async function shows(page: Page, want: string | RegExp, timeout = 10_000): Promise<void> {
  const poll = expect.poll(() => value(page).catch(() => ''), { timeout });
  await (typeof want === 'string' ? poll.toBe(want) : poll.toMatch(want));
}
const more = (page: Page) => page.locator('[data-part~="scripts-more"]');
const line = (page: Page) => page.locator('#side [data-part="comment-line"] textarea');
const read = (file: string) => (existsSync(file) ? readFileSync(file, 'utf8') : '');
const docOf = (folder: string) => join(folder, 'v0', `${folder.split('/').pop()}_v0.md`);

/** Nothing on the page reaches past the screen's edges (nor makes the page scroll sideways). */
async function noOverflow(page: Page, where: string): Promise<void> {
  const out = await page.evaluate(() => {
    const w = innerWidth;
    const bad: string[] = [];
    if (document.documentElement.scrollWidth > w) bad.push(`html is ${document.documentElement.scrollWidth} wide`);
    if (document.body.scrollWidth > w) bad.push(`body is ${document.body.scrollWidth} wide`);
    for (const el of document.querySelectorAll<HTMLElement>('body *')) {
      const s = getComputedStyle(el);
      if (s.display === 'none' || s.visibility === 'hidden') continue;
      const b = el.getBoundingClientRect();
      if (!b.width || !b.height || (b.right <= w + 1 && b.left >= -1)) continue;
      // Inside a box that scrolls sideways and is itself on screen (a strip of versions): it scrolls into view. A box
      // that only clips doesn't count: what it cuts off is still cut off.
      let clipped = false;
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const o = getComputedStyle(p).overflowX;
        if (o !== 'auto' && o !== 'scroll') continue;
        const pb = p.getBoundingClientRect();
        if (pb.right <= w + 1 && pb.left >= -1) clipped = true;
        break;
      }
      if (!clipped)
        bad.push(
          `${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''}.${[...el.classList].join('.')} ${Math.round(b.left)}..${Math.round(b.right)}`,
        );
    }
    return bad.slice(0, 8);
  });
  expect(out, `${where}: nothing past the screen's edge`).toEqual([]);
}

/** A phone's controls are thumb-sized (at least 40 px), and the document's text at least 17 px. */
async function phoneSized(page: Page): Promise<void> {
  for (const sel of ['#rec', '#done', '#sideToggle', '[data-part~="review-back"]', '[data-part~="scripts-more"]']) {
    const el = page.locator(sel).first();
    if (!(await el.count()) || !(await el.isVisible())) continue;
    const b = (await el.boundingBox())!;
    expect(Math.min(b.width, b.height), `${sel} is big enough to tap`).toBeGreaterThanOrEqual(40);
  }
  const size = await text(page).evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
  expect(size).toBeGreaterThanOrEqual(17);
  // The document fills the page from the top down to Record and Send: nothing sits between them.
  const doc = (await page.locator('[data-ext="scripts"][data-slot="version"]').boundingBox())!;
  const bar = (await page.locator('#precord').boundingBox())!;
  expect(doc.y).toBeLessThanOrEqual(1);
  expect(Math.abs(doc.y + doc.height - bar.y)).toBeLessThanOrEqual(2);
  expect(bar.y + bar.height).toBeLessThanOrEqual(page.viewportSize()!.height + 1);
}

/** The live preview: the title (and every heading) larger than the text, its # hidden off the caret's line. */
async function preview(page: Page, title: string): Promise<void> {
  const ed = page.locator('[data-ext="scripts"] .cm-content');
  const h1 = ed.locator('.cm-line.ig-h1').first();
  const body = ed.locator('.cm-line:not(.ig-h):not(.ig-li)', { hasText: /\w/ }).first();
  const size = (l: typeof h1) => l.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
  await expect(h1).toHaveText(title);
  expect(await size(h1)).toBeGreaterThan((await size(body)) * 1.4);
  for (const h of await ed.locator('.cm-line.ig-h').all()) expect(await h.textContent()).not.toMatch(/^#/);
}

/** A task's box ticks the file's `[ ]` to `[x]`, and nothing else in it. */
async function tick(page: Page, folder: string, task: string): Promise<void> {
  const before = read(docOf(folder));
  const box = page.locator('[data-ext="scripts"] [data-part="scripts-task"]').first();
  await expect(box).toHaveAttribute('aria-checked', 'false');
  await box.click();
  await expect.poll(() => read(docOf(folder))).toBe(before.replace(`- [ ] ${task}`, `- [x] ${task}`));
  await expect(box).toHaveAttribute('aria-checked', 'true');
}

/** A pasted image (a PNG drawn in the page) is kept in v0/images/ and shows in the document. */
async function paste(page: Page, folder: string): Promise<void> {
  await text(page).click();
  await page.keyboard.press('Control+End');
  await text(page).evaluate(async (el) => {
    const c = document.createElement('canvas');
    c.width = 200;
    c.height = 100;
    const g = c.getContext('2d')!;
    g.fillStyle = 'teal';
    g.fillRect(0, 0, 200, 100);
    const blob = await new Promise<Blob>((r) => c.toBlob((b) => r(b!), 'image/png'));
    const data = new DataTransfer();
    data.items.add(new File([blob], 'Shop front.png', { type: 'image/png' }));
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
  });
  const doc = () => read(docOf(folder));
  await expect.poll(doc, { timeout: 10_000 }).toMatch(/\n\n!\[\]\(images\/shop-front-\d{8}-\d{6}\.png\)\n$/);
  const name = /images\/(shop-front-[\d-]+\.png)/.exec(doc())![1]!;
  expect(existsSync(join(folder, 'v0', 'images', name))).toBe(true);
  // Off the caret's line it's the picture (Esc leaves the text, so every line is drawn).
  await page.keyboard.press('Escape');
  const img = page.locator('[data-ext="scripts"] [data-part="scripts-image"] img').last();
  await expect.poll(() => img.evaluate((i) => (i as HTMLImageElement).naturalWidth), { timeout: 10_000 }).toBe(200);
}

/** New idea from the home: the form at #ext/scripts/new, Create, and the new video's page on its one document. */
async function newIdea(page: Page, home: string, title: string): Promise<void> {
  await page.goto(`${home}#ext/scripts/new`);
  const form = page.locator('[data-part="scripts-new"]');
  await expect(form).toBeVisible({ timeout: 20_000 });
  await form.locator('[data-part="scripts-title"]').fill(title);
  await expect(form.locator('[data-part="scripts-project"]')).toBeEnabled();
  await form.locator('[data-part="scripts-project"]').selectOption('deadlock');
  await expect(form).toContainText('Goes in deadlock/jump-souls');
  await form.locator('[data-part="scripts-create"]').click();
  await page.waitForURL(/\/p\/deadlock-jump-souls\//, { timeout: 30_000 });
  await expect(text(page)).toBeVisible({ timeout: 20_000 });
  await shows(page, `# ${title}\n\n`);
  // One plain editor: no tabs, no ask box.
  await expect(page.locator('[data-part="scripts-tab"], [data-part="scripts-ask"]')).toHaveCount(0);
}

/** Types at the end of the document; the file has it 600 ms later (there is no "Saved" word). */
async function type(page: Page, folder: string, words: string): Promise<void> {
  await text(page).click();
  await page.keyboard.press('Control+End');
  await page.keyboard.type(words);
  // Typing is the text's: the r in the words never records.
  expect(await page.evaluate(() => window.__review?.seg ?? null)).toBeNull();
  await expect.poll(() => read(docOf(folder)), { timeout: 10_000 }).toContain(words);
  await expect(page.locator('[data-part="scripts-not-saved"], [data-part="scripts-outside"]')).toHaveCount(0);
}

/** The chat's thread, its messages and its composer (spec/chat.md). */
const chat = (page: Page) => page.locator('#side [data-part="chat"]');
const composer = (page: Page) => page.locator('#side [data-part="chat-composer"] textarea');
/** The agent's question in the chat. */
const question = (page: Page) => chat(page).locator('[data-part="chat-question"]').last();

/** A voice message: Esc leaves the text, R talks, R stops; the words are sent to the chat (not into the document), and
 * the agent shapes the document and asks which opening, with two pictures. */
async function voiceMessage(page: Page, folder: string): Promise<void> {
  const before = read(docOf(folder));
  await text(page).click();
  await page.keyboard.press('Control+End');
  await page.keyboard.press('Escape');
  await page.keyboard.press('r');
  await expect(page.locator('[data-part="chat-composer"] .mic')).toHaveClass(/on/, { timeout: 10_000 });
  await page.waitForTimeout(2500);
  await page.keyboard.press('r');
  const mine = chat(page).locator('[data-part="chat-person"]', { hasText: WORDS });
  await expect(mine).toBeVisible({ timeout: 30_000 });
  await expect(mine.locator('svg.voice')).toHaveCount(1);
  // The words went to the agent, not into the document (no talk-out of before, now or when it opens again).
  expect(read(docOf(folder))).not.toContain(WORDS);
  const voice = readdirSync(join(folder, 'v0', 'voice', 'chat'));
  expect(voice.some((f) => f.endsWith('.webm'))).toBe(true);
  expect(voice.some((f) => f.endsWith('.words.json'))).toBe(true);
  await asked(page, folder, before);
}

/** The agent's answer to a first message: it writes the outline and the script while the page shows them, says what it
 * does in quiet lines (never raw JSON), and asks which opening with two pictures to tap. */
async function asked(page: Page, folder: string, before: string): Promise<void> {
  const q = question(page);
  await expect(q).toContainText('Which opening?', { timeout: 60_000 });
  await expect(q.locator('.choice')).toHaveCount(2);
  await expect
    .poll(() => q.locator('img').evaluateAll((im) => im.map((i) => (i as HTMLImageElement).naturalWidth)))
    .toEqual([320, 320]);
  await expect(chat(page).locator('[data-part="chat-agent"]', { hasText: 'Which opening do you want?' })).toBeVisible({
    timeout: 20_000,
  });
  // The tools are ONE line over the reply; the turn done, it sums them up (spec/chat.md, "The activity line").
  const line = chat(page).locator('[data-part="chat-tool"]');
  await expect(line).toHaveCount(1);
  await expect(line).toHaveAttribute('data-state', 'done', { timeout: 20_000 });
  await expect(line).toHaveText('Edited the script · saved an image · read the script');
  // Plain words: no raw JSON, no "Agent" label, no line per tool.
  expect(await chat(page).innerText()).not.toMatch(/"type"|stream_event|tool_use|^Agent$|Editing the script/m);
  const doc = read(docOf(folder));
  expect(doc).toContain('## Outline');
  expect(doc).toContain('## Script\n\n### Cold open');
  expect(doc.startsWith(before.trimEnd())).toBe(true); // the person's words stay
  await shows(page, /### Cold open/);
  // Nothing went to To do: a script has no comments, only the chat.
  await expect(page.locator('#side [data-part="tabs"]')).toHaveCount(0);
}

/** Typed in the chat: Enter sends; the agent puts the link alone on its own line. */
async function chatLink(page: Page, folder: string): Promise<void> {
  await composer(page).click();
  await page.keyboard.type('Pull in https://example.org/clip please');
  // Typing is the composer's: the r in the words never records.
  expect(await page.evaluate(() => window.__review?.seg ?? null)).toBeNull();
  await page.keyboard.press('Enter');
  await expect(composer(page)).toHaveValue('');
  await expect.poll(() => read(docOf(folder)), { timeout: 30_000 }).toMatch(/\n\nhttps:\/\/example\.org\/clip\n$/);
  // The page has the agent's edit too (hosted, it comes from the computer a moment after the file changed there).
  await shows(page, /\n\nhttps:\/\/example\.org\/clip\n$/);
  await expect(chat(page).locator('[data-part="chat-agent"]').last()).toContainText('Added the link');
  const line = chat(page).locator('[data-part="chat-tool"]').last();
  await expect(line).toHaveText('Edited the script · read example.org/…', { timeout: 20_000 });
  await expect(line).toHaveAttribute('data-state', 'done');
}

/** A slow turn: while it runs ONE live line says what the agent does now, the words stream in under it, and the
 * composer's arrow is Stop; once it's done the line sums the turn up. */
async function slowTurn(page: Page): Promise<void> {
  await composer(page).click();
  await page.keyboard.type('Check the server slowly');
  await page.keyboard.press('Enter');
  const line = chat(page).locator('[data-part="chat-tool"]').last();
  await expect(line).toHaveText('Running a command…', { timeout: 30_000 });
  await expect(line).toHaveAttribute('data-state', 'live');
  const words = chat(page).locator('[data-part="chat-agent"]').last();
  await expect(words).toContainText('Checking the server first.');
  expect((await words.boundingBox())!.y).toBeGreaterThan((await line.boundingBox())!.y);
  await expect(page.locator('#side [data-part="chat-stop"]')).toBeVisible();
  await expect(page.locator('[data-part="chat-status"]')).toHaveText('');
  await shot(page, 'chat-midturn');
  await expect(line).toHaveAttribute('data-state', 'done', { timeout: 30_000 });
  await expect(line).toHaveText('Ran 2 commands');
  await expect(words).toContainText('It serves the script from the right place.');
  await shot(page, 'chat-after');
}

/** The column's left edge drags it wider (the document reflows beside it), ←/→ on it resize it, the width is
 * remembered after a reload, and a double-click goes back to the default. */
async function dragWider(page: Page): Promise<void> {
  const side = page.locator('#side');
  const grip = page.locator('#side [data-part="sidebar-grip"]');
  const width = async () => Math.round((await side.boundingBox())!.width);
  const before = await width();
  const docSlot = page.locator('[data-ext="scripts"][data-slot="version"]');
  const doc = (await docSlot.boundingBox())!;
  const g = (await grip.boundingBox())!;
  await page.mouse.move(g.x + g.width / 2, g.y + 300);
  await page.mouse.down();
  await page.mouse.move(g.x + g.width / 2 - 240, g.y + 300, { steps: 8 });
  await page.mouse.up();
  expect(await width()).toBe(before + 240);
  const after = (await side.boundingBox())!;
  const doc2 = (await docSlot.boundingBox())!;
  expect(doc2.width).toBeLessThan(doc.width - 200);
  expect(doc2.x + doc2.width).toBeLessThanOrEqual(after.x + 1); // nothing overlaps
  await expect(grip).toHaveAttribute('aria-valuenow', String(before + 240));
  await shot(page, 'chat-wide');
  await page.reload();
  await expect(text(page)).toBeVisible({ timeout: 20_000 });
  await expect.poll(width).toBe(before + 240);
  await grip.focus();
  await page.keyboard.press('ArrowRight');
  await expect.poll(width).toBe(before + 224);
  await page.keyboard.press('Shift+ArrowLeft');
  await expect.poll(width).toBe(before + 272);
  await grip.dblclick();
  await expect.poll(width).toBe(before);
  expect(await page.evaluate(() => localStorage.getItem('retake.sidebar-width'))).toBeNull();
}

/** Every scrolling part has a thin scrollbar in the theme's tokens (no arrows), the chat's thread and the document
 * alike; a close-up of the chat's. */
async function scrollbars(page: Page): Promise<void> {
  const look = await page.evaluate(() => {
    const of = (el: Element | null) => {
      if (!el) return null;
      const s = getComputedStyle(el);
      return { width: s.scrollbarWidth, color: s.scrollbarColor };
    };
    /** A token's colour as computed styles write it. */
    const token = (name: string) => {
      const probe = document.createElement('div');
      probe.style.color = `var(${name})`;
      document.body.append(probe);
      const c = getComputedStyle(probe).color;
      probe.remove();
      return c;
    };
    return {
      want: `${token('--scrollbar-thumb')} ${token('--scrollbar-track')}`,
      thread: of(document.querySelector('[data-part="chat-thread"]')),
      doc: of(document.querySelector('[data-ext="scripts"] .ig-scroll')),
      root: of(document.documentElement),
    };
  });
  for (const where of ['thread', 'doc', 'root'] as const) {
    expect(look[where], where).toEqual({ width: 'thin', color: look.want });
  }
  // A short window, so the thread scrolls: the close-up of its scrollbar.
  const size = page.viewportSize()!;
  await page.setViewportSize({ width: size.width, height: 560 });
  const thread = chat(page).locator('[data-part="chat-thread"]');
  await expect.poll(() => thread.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
  await thread.evaluate((el) => el.scrollTo({ top: el.scrollHeight / 3 }));
  const box = (await page.locator('#side').boundingBox())!;
  mkdirSync(SHOTS, { recursive: true });
  await page.waitForTimeout(300);
  await page.screenshot({
    path: join(SHOTS, `${ENGINE}-scrollbar-closeup.png`),
    clip: { x: box.x - 160, y: 40, width: 160 + box.width, height: 420 },
  });
  await page.setViewportSize(size);
}

/** ⋯ and one of its items. */
async function menu(page: Page, item: 'scripts-type' | 'scripts-undo'): Promise<void> {
  await more(page).click();
  await page.locator(`[data-part~="scripts-menu"] [data-part="${item}"]`).click();
}

/** A tap on a choice answers it in the chat; the agent writes the answer in. */
async function answer(page: Page, folder: string): Promise<void> {
  await question(page).locator('.choice', { hasText: 'Start on the jump' }).click();
  await expect(chat(page).locator('[data-part="chat-person"]', { hasText: 'Start on the jump' })).toBeVisible();
  await expect.poll(() => read(docOf(folder)), { timeout: 60_000 }).toContain('[Start on the jump.]');
  await expect(question(page).locator('.choice.picked')).toHaveText('Start on the jump');
  await expect(question(page).locator('.choice').first()).toBeDisabled();
}

/** ⋯ → Undo the agent's edit: the document as it was before the agent's newest edit. */
async function undo(page: Page, folder: string): Promise<void> {
  await shows(page, /\[Start on the jump\.\]/);
  await menu(page, 'scripts-undo');
  await expect.poll(() => read(docOf(folder))).toContain('[The jump.]');
  await shows(page, /\[The jump\.\]/);
}

/** Send: "I like it, make the video". With no cut, the agent builds v1 from the script and the page goes there; back
 * on the script, Send is dimmed with its reason until the script changes. */
async function sendBuildsV1(page: Page, folder: string): Promise<void> {
  const send = page.locator('#done');
  await expect(send).toHaveAttribute('title', 'Make the video from the script', { timeout: 10_000 });
  await send.click();
  await expect(page.locator('[data-part="chat-status"]')).toContainText(/Building v1/, { timeout: 10_000 });
  const name = folder.split('/').pop();
  await expect.poll(() => existsSync(join(folder, 'v1', `${name}_v1.mp4`)), { timeout: 90_000 }).toBe(true);
  expect(read(join(folder, 'v0', 'cuts', 'v1.json'))).toContain('Cold open');
  await page.waitForFunction(() => window.__review?.view?.shown === 'v1', null, { timeout: 60_000 });
  await page.locator('#verBtn').click();
  await page.locator('#verList [data-ver="v0"]').click();
  await expect(text(page)).toBeVisible({ timeout: 20_000 });
  await expect(send).toHaveAttribute('aria-disabled', 'true', { timeout: 10_000 });
  await expect(send).toHaveAttribute('title', 'Nothing changed since the last Send');
  await send.click({ force: true }); // never a silent no-op: it says why
  await expect(page.locator('#toast')).toHaveText('Nothing changed since the last Send');
}

test.describe('locally', () => {
  let server: Server;
  let cfg: ScriptsConfig;
  let dir = '';
  const idea = () => join(dir, 'deadlock', 'jump-souls');
  const videoUrl = () => `${server.url}p/deadlock-jump-souls/`;

  test.beforeAll(async () => {
    cfg = scriptsConfig('scripts-local');
    expect(readFileSync(cfg.file, 'utf8')).toMatch(/\[extensions\][^[]*enabled = \["scripts"\]/);
    dir = videos('scripts-local');
    server = await startServer('scripts', dir, PORTS.scripts, ['--verbose'], { env: { XDG_CONFIG_HOME: cfg.home } });
  });
  test.afterAll(async () => {
    await server?.stop();
    expect(pageErrors).toEqual([]);
  });

  test('New idea opens the one document, typing saves, and an outside edit shows', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    try {
      await newIdea(page, server.url, 'Jump souls');
      expect(read(docOf(idea()))).toBe('# Jump souls\n\n');
      await type(page, idea(), 'The jump to secure souls only works if you time it.');
      expect(read(docOf(idea()))).toBe('# Jump souls\n\nThe jump to secure souls only works if you time it.');
      // Edited outside Retake, with nothing unsaved: it reloads in place.
      writeFileSync(
        docOf(idea()),
        '# Jump souls\n\nThe jump to secure souls only works if you time it.\n\nOutside.\n\n- [ ] Film the jump\n',
      );
      await shows(page, /Outside\.\n\n- \[ \] Film the jump\n$/);
      // The live preview: a heading is larger than the text and its # is hidden; a task's box ticks the file.
      await preview(page, 'Jump souls');
      await tick(page, idea(), 'Film the jump');
      await shot(page, 'editor-preview');

      // The home: the idea in Scripts' section.
      await page.goto(server.url);
      const sec = page.locator('[data-part="nav-ext"][data-ext="scripts"]');
      await expect(sec.locator('[data-part="nav-ext-video"]', { hasText: /jump-souls|Jump souls/i })).toBeVisible({
        timeout: 20_000,
      });
      await shot(page, 'nav');
    } finally {
      await context.close();
    }
    const phone = await newPage(browser, PHONES[0]);
    try {
      const page = phone.page;
      await page.goto(server.url);
      await page.locator('[data-part="nav-toggle"]').click();
      await expect(page.locator('[data-part="nav-ext"][data-ext="scripts"]')).toBeVisible();
      // The drawer slides in: measure it once it's there.
      await expect
        .poll(async () => (await page.locator('[data-part="nav"]').boundingBox())?.x)
        .toBeGreaterThanOrEqual(0);
      await noOverflow(page, 'the home with its drawer open');
      await shot(page, 'nav', PHONES[0]);
      await page.goto(`${server.url}#ext/scripts/`);
      await expect(page.locator('[data-part="scripts-idea"]', { hasText: 'Jump souls' })).toBeVisible({
        timeout: 20_000,
      });
      await noOverflow(page, 'the ideas page');
      await shot(page, 'ideas', PHONES[0]);
      await page.goto(`${server.url}#ext/scripts/new`);
      await page.locator('[data-part="scripts-title"]').fill('A much longer title for a video about souls');
      await noOverflow(page, 'New idea');
      await shot(page, 'new-idea', PHONES[0]);
    } finally {
      await phone.context.close();
    }
  });

  test('the chat: a voice message, the agent shapes the document and asks; a tap answers; ⋯ undoes; a link', async ({
    browser,
  }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(videoUrl());
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await expect(chat(page)).toBeVisible();
      await expect(page.locator('#rec')).toHaveCount(0); // the chat's mic records (and R)
      await voiceMessage(page, idea());
      await shot(page, 'chat-question');

      // The same on phones, while the question is open: the document, the drawer with the question, the ⋯ menu.
      for (const size of PHONES) {
        const phone = await newPage(browser, size);
        try {
          const p = phone.page;
          await p.goto(videoUrl());
          await shows(p, /### Cold open/, 20_000);
          await expect(p.locator('#sideToggle')).toBeVisible();
          await expect(p.locator('#precord [data-part="chat-pill"]')).toBeVisible();
          await noOverflow(p, `the document at ${size.width}`);
          await phoneSized(p);
          await preview(p, 'Jump souls');
          // Reading: a tap on the text never raises the keyboard (the agent edits; ⋯ → Type in the script to type).
          await text(p).tap();
          expect(await p.evaluate(() => !!document.activeElement?.closest('[contenteditable="true"]'))).toBe(false);
          await shot(p, 'chat-closed', size);
          // The pill opens the chat sheet with its composer focused.
          await p.locator('[data-part="chat-pill"] .ask').tap();
          await expect(p.locator('#side.drawer.chatting')).toBeVisible();
          await expect.poll(() => p.evaluate(() => document.activeElement?.getAttribute('aria-label'))).toBe(
            'Ask the agent',
          );
          const q = question(p);
          await expect(q).toContainText('Which opening?');
          // An idea has one version (v0): nothing to choose, so no version menu.
          await expect(p.locator('[data-part~="version-picker"]')).toHaveCount(0);
          // The pictures are stacked on a phone.
          const pics = await q.locator('.choice').evaluateAll((els) => els.map((e) => e.getBoundingClientRect().x));
          expect(Math.abs(pics[0]! - pics[1]!)).toBeLessThan(1);
          await noOverflow(p, `the chat sheet at ${size.width}`);
          await shot(p, 'chat-open', size);
          await p.locator('#side.drawer .icon.hide').tap();
          await expect(p.locator('#side.drawer')).toBeHidden();
          await more(p).click();
          await expect(p.locator('[data-part~="scripts-menu"]')).toBeVisible();
          await expect(p.locator('[data-part="scripts-type"]')).toBeVisible();
          await noOverflow(p, `the ⋯ menu at ${size.width}`);
          await shot(p, 'menu', size);
          await p.locator('[data-part="scripts-type"]').click();
          await expect(text(p)).toHaveAttribute('contenteditable', 'true');
        } finally {
          await phone.context.close();
        }
      }

      await answer(page, idea());
      await undo(page, idea());
      await chatLink(page, idea());
      await shot(page, 'chat');
      await slowTurn(page);
      await dragWider(page);
      await scrollbars(page);
      await paste(page, idea());
      await shot(page, 'editor-image');
    } finally {
      await context.close();
    }
  });

  test('Send builds the first cut; v1 opens, with Script last in the version menu; then Send says nothing changed', async ({
    browser,
  }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(videoUrl());
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await sendBuildsV1(page, idea());
      await shot(page, 'sent');
      await page.locator('#verBtn').click();
      await page.locator('#verList [data-ver="v1"]').click();
      await page.waitForFunction(() => window.__review?.view?.shown === 'v1', null, { timeout: 20_000 });
      // The one version menu (the bar's; the comments header has none) lists v1, then Script, set apart.
      await expect(page.locator('#side [data-part~="version-picker"]')).toHaveCount(0);
      await page.locator('#verBtn').click();
      const versions = page.locator('[data-part~="version-list"] [data-version]');
      await expect(versions).toHaveCount(2);
      await expect(versions.first()).toContainText('v1');
      await expect(versions.nth(1).locator('.name')).toHaveText('Script');
      await expect(page.locator('[data-part~="version-list"] [role="separator"]')).toHaveCount(1);
      await page.keyboard.press('Escape');
      // The Script tab on v1: the document's ## Script, a section per beat, linked by cuts/v1.json.
      await page.locator('[data-ext-tab="scripts/sidebar-tab"]').click();
      const sections = page.locator('[data-part="scripts-section"]');
      await expect(sections).toHaveCount(2, { timeout: 10_000 });
      await expect(sections.first()).toContainText('Cold open');
      await expect(sections.nth(1)).toContainText('The trick');
      await page.locator('#verBtn').click();
      await expect(page.locator('#verList [data-ver="v0"] .name')).toHaveText('Script');
      await shot(page, 'versions');
    } finally {
      await context.close();
    }
    const phone = await newPage(browser, PHONES[0]);
    try {
      const p = phone.page;
      await p.goto(videoUrl());
      await p.waitForFunction(() => window.__review?.view?.shown === 'v1', null, { timeout: 30_000 });
      await p.locator('#verBtn').click();
      await expect(p.locator('#verList [data-ver="v0"] .name')).toHaveText('Script');
      await noOverflow(p, 'the version list');
      await shot(p, 'versions', PHONES[0]);
      // v0 from there: the document again (no controls row), and the drawer's header has the one version menu.
      await p.locator('#verList [data-ver="v0"]').click();
      await expect(text(p)).toBeVisible({ timeout: 20_000 });
      await expect(p.locator('[data-part~="version-picker"]:visible')).toHaveCount(0);
      await p.locator('#sideToggle').click();
      await expect(p.locator('#side.drawer [data-part~="version-picker"]')).toHaveText('Script');
      await expect(p.locator('[data-part~="version-picker"]:visible')).toHaveCount(1);
      await expect(p.locator('#side.drawer [data-part="chat"]')).toBeVisible();
      await p.locator('#side.drawer [data-part~="version-picker"]').click();
      await expect(p.locator('[data-part~="version-list"] [data-version="v1"]')).toBeVisible();
      await noOverflow(p, 'the drawer on v0 with v1');
      await p.keyboard.press('Escape');
      await shot(p, 'drawer-versions', PHONES[0]);
    } finally {
      await phone.context.close();
    }
  });

  test('a multi-file v0 from before becomes one document, and the old files stay', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    const old = join(dir, 'deadlock', 'old-idea');
    try {
      await page.goto(`${server.url}p/deadlock-old-idea/`);
      const migrated =
        '# Old idea\n\nThe idea, from before.\n\n## Outline\n\n- Cold open\n- The trick\n\n## Script\n\n### Cold open\n\nThe jump.\n';
      await shows(page, migrated, 20_000);
      expect(read(docOf(old))).toBe(migrated);
      expect(read(join(old, 'v0', 'idea.md'))).toBe(OLD_IDEA);
      expect(read(join(old, 'v0', 'outline.md'))).toBe(OLD_OUTLINE);
      expect(read(join(old, 'v0', 'script.md'))).toBe(OLD_SCRIPT);
    } finally {
      await context.close();
    }
  });
});

test.describe('hosted', () => {
  const DATA = join(WORK, 'scripts-hosted', 'server');
  const BASE = `http://127.0.0.1:${PORTS.scriptsHosted}`;
  let server: Server;
  let connect: Server | null = null;
  let cfg: ScriptsConfig;
  let dir = '';
  const idea = () => join(dir, 'deadlock', 'jump-souls');

  const admin = (...args: string[]) =>
    JSON.parse(
      execFileSync(RETAKE_BIN, [...args, '--data', DATA, '--public-url', BASE, '--json'], {
        env: { ...process.env, XDG_CONFIG_HOME: cfg.home },
      }).toString(),
    ) as { link: string };

  test.beforeAll(async () => {
    cfg = scriptsConfig('scripts-hosted');
    dir = videos('scripts-hosted');
    admin('user', 'add', 'owner@example.com', '--name', 'Owner', '--admin');
    server = await startTeamServer('scripts-hosted', PORTS.scriptsHosted, [
      '--data',
      DATA,
      '--public-url',
      BASE,
      '--extensions',
      'scripts',
    ]);
  });
  test.afterAll(async () => {
    await connect?.stop();
    await server?.stop();
    expect(pageErrors).toEqual([]);
  });

  test('the same through retake connect: New idea, typing, a voice message, the chat, a question, undo, Send, an old v0', async ({
    browser,
  }) => {
    test.setTimeout(300_000);
    const { context, page } = await newPage(browser);
    try {
      await page.goto(admin('user', 'link', 'owner@example.com').link);
      await page.getByRole('button', { name: /Sign in as/ }).click();
      await page.waitForURL(`${BASE}/`);
      const made = (await (await page.request.post(`${BASE}/api/connect`, { headers: { Origin: BASE } })).json()) as {
        token: string;
      };
      connect = startDaemon(
        'scripts-connect',
        RETAKE_BIN,
        ['connect', BASE, '--token', made.token, '--folder', dir, '--name', 'e2e-pc'],
        { XDG_CONFIG_HOME: cfg.home },
      );
      // The computer is there once its video is.
      await expect
        .poll(async () => (await page.request.get(`${BASE}/p/deadlock-item-shop/api/info`)).status(), {
          timeout: 30_000,
        })
        .toBe(200);

      await newIdea(page, `${BASE}/`, 'Jump souls');
      // The folder is on the computer, never on the server.
      expect(read(docOf(idea()))).toBe('# Jump souls\n\n');
      expect(existsSync(join(DATA, 'projects', 'deadlock-jump-souls', 'v0'))).toBe(false);
      await type(page, idea(), 'Hosted, the files stay on the computer.');
      // The voice message's audio goes to the computer and is transcribed there; the agent runs there too.
      await voiceMessage(page, idea());
      await answer(page, idea());
      await undo(page, idea());
      await chatLink(page, idea());
      await paste(page, idea());
      await sendBuildsV1(page, idea());
      // The chat is kept on the server, the documents and the audio on the computer.
      expect(existsSync(join(DATA, 'projects', 'deadlock-jump-souls', 'v0'))).toBe(false);

      // A v0 from before, migrated on the computer.
      const old = join(dir, 'deadlock', 'old-idea');
      await page.goto(`${BASE}/p/deadlock-old-idea/`);
      await shows(page, /^# Old idea\n\nThe idea, from before\.\n\n## Outline/, 20_000);
      await expect.poll(() => read(docOf(old))).toContain('## Script\n\n### Cold open');
      expect(read(join(old, 'v0', 'idea.md'))).toBe(OLD_IDEA);
    } finally {
      await context.close();
    }
  });
});
