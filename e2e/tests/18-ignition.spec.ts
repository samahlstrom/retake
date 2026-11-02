// Ignition (spec/ignition.md), turned on with `retake ext enable ignition`, with a fake transcriber (a command engine
// that prints fixed words) and a fake agent (e2e/lib/ignition.ts). v0 is one document, v0/<video>_v0.md, in one plain
// editor: CodeMirror drawing its Markdown as a live preview. Locally (`retake open <folder>`) and hosted (`retake serve
// --extensions ignition` and `retake connect`):
// - New idea opens the one document, typing saves, and an outside edit shows. Headings are drawn larger than the text,
//   their #s hidden off the caret's line; a task's box ticks `[ ]` to `[x]` in the file; a pasted image is kept in
//   v0/images/ and shows.
// - A talk-out lands in the document as a plain paragraph and adds a To do; a comment typed in the comments column
//   adds one too (the core line, anchored by the slot).
// - Send runs an agent that writes the outline and the script into the document and asks about "Cold open" with two
//   pictures; its place shows it in the document; the question shows in the document too, and a tap there (or in the
//   comments column) answers it; Done; ⋯ undoes the agent's edit.
// - Start v1 from ⋯ runs an agent that writes a video and cuts/v1.json; v1 opens, with `v0 · Doc` first.
// - A multi-file v0 from before (idea.md, outline.md, script.md) becomes one document and the old files stay.
// On phones (390×844 and 430×932) nothing is wider than the screen, the text is at least 17 px and the controls are
// thumb-sized. Screenshots go to .retake-work/ignition-shots/ (<engine>-<what>-<size>.png).
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { ignitionConfig, WORDS, type IgnitionConfig } from '../lib/ignition.ts';
import { LEGACY, PORTS, REPO, RETAKE_BIN, WORK } from '../lib/env.ts';
import { startDaemon, startServer, startTeamServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: the legacy player has no extensions');

const SHOTS = join(REPO, '.retake-work', 'ignition-shots');
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
const text = (page: Page) => page.locator('[data-ext="ignition"] [data-part="ignition-text"]');
type Hook = { ignition: { text(): string; caret(): number } };
/** The document as the editor holds it (the file's text), through the editor's test hook. */
const value = (page: Page) => text(page).evaluate((el) => (el as unknown as Hook).ignition.text());
const caretAt = (page: Page) => text(page).evaluate((el) => (el as unknown as Hook).ignition.caret());
/** The editor shows this text (or text matching it). */
async function shows(page: Page, want: string | RegExp, timeout = 10_000): Promise<void> {
  const poll = expect.poll(() => value(page).catch(() => ''), { timeout });
  await (typeof want === 'string' ? poll.toBe(want) : poll.toMatch(want));
}
const more = (page: Page) => page.locator('[data-part~="ignition-more"]');
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
  for (const sel of ['#rec', '#done', '#sideToggle', '[data-part~="review-back"]', '[data-part~="ignition-more"]']) {
    const el = page.locator(sel).first();
    if (!(await el.count()) || !(await el.isVisible())) continue;
    const b = (await el.boundingBox())!;
    expect(Math.min(b.width, b.height), `${sel} is big enough to tap`).toBeGreaterThanOrEqual(40);
  }
  const size = await text(page).evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
  expect(size).toBeGreaterThanOrEqual(17);
  // The document fills the page from the top down to Record and Send: nothing sits between them.
  const doc = (await page.locator('[data-ext="ignition"][data-slot="version"]').boundingBox())!;
  const bar = (await page.locator('#precord').boundingBox())!;
  expect(doc.y).toBeLessThanOrEqual(1);
  expect(Math.abs(doc.y + doc.height - bar.y)).toBeLessThanOrEqual(2);
  expect(bar.y + bar.height).toBeLessThanOrEqual(page.viewportSize()!.height + 1);
}

/** The live preview: the title (and every heading) larger than the text, its # hidden off the caret's line. */
async function preview(page: Page, title: string): Promise<void> {
  const ed = page.locator('[data-ext="ignition"] .cm-content');
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
  const box = page.locator('[data-ext="ignition"] [data-part="ignition-task"]').first();
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
  const img = page.locator('[data-ext="ignition"] [data-part="ignition-image"] img').last();
  await expect.poll(() => img.evaluate((i) => (i as HTMLImageElement).naturalWidth), { timeout: 10_000 }).toBe(200);
}

/** New idea from the home: the form at #ext/ignition/new, Create, and the new video's page on its one document. */
async function newIdea(page: Page, home: string, title: string): Promise<void> {
  await page.goto(`${home}#ext/ignition/new`);
  const form = page.locator('[data-part="ignition-new"]');
  await expect(form).toBeVisible({ timeout: 20_000 });
  await form.locator('[data-part="ignition-title"]').fill(title);
  await expect(form.locator('[data-part="ignition-project"]')).toBeEnabled();
  await form.locator('[data-part="ignition-project"]').selectOption('deadlock');
  await expect(form).toContainText('Goes in deadlock/jump-souls');
  await form.locator('[data-part="ignition-create"]').click();
  await page.waitForURL(/\/p\/deadlock-jump-souls\//, { timeout: 30_000 });
  await expect(text(page)).toBeVisible({ timeout: 20_000 });
  await shows(page, `# ${title}\n\n`);
  // One plain editor: no tabs, no ask box.
  await expect(page.locator('[data-part="ignition-tab"], [data-part="ignition-ask"]')).toHaveCount(0);
}

/** Types at the end of the document; the file has it 600 ms later (there is no "Saved" word). */
async function type(page: Page, folder: string, words: string): Promise<void> {
  await text(page).click();
  await page.keyboard.press('Control+End');
  await page.keyboard.type(words);
  // Typing is the text's: the r in the words never records.
  expect(await page.evaluate(() => window.__review?.seg ?? null)).toBeNull();
  await expect.poll(() => read(docOf(folder)), { timeout: 10_000 }).toContain(words);
  await expect(page.locator('[data-part="ignition-not-saved"], [data-part="ignition-outside"]')).toHaveCount(0);
}

/** A talk-out: Esc leaves the text, R talks, R stops; the words land as a plain paragraph and a To do asks the agent
 * to work them in. */
async function talkOut(page: Page, folder: string): Promise<void> {
  await text(page).click();
  await page.keyboard.press('Control+End');
  await page.keyboard.press('Escape');
  await page.keyboard.press('r');
  await expect(page.locator('#rec')).toHaveClass(/on|rec/, { timeout: 10_000 });
  await page.waitForTimeout(2500);
  await page.keyboard.press('r');
  await shows(page, new RegExp(`\\n\\n${WORDS}\\n$`), 30_000);
  expect(await value(page)).not.toMatch(/<!--|transcribing/);
  await expect(page.locator('#side .item', { hasText: 'Work this talk-out into the document.' })).toBeVisible({
    timeout: 10_000,
  });
  const voice = readdirSync(join(folder, 'v0', 'voice'));
  expect(voice.some((f) => f.endsWith('.webm'))).toBe(true);
  const sidecar = voice.find((f) => f.endsWith('.md'));
  expect(read(join(folder, 'v0', 'voice', sidecar ?? 'none'))).toMatch(/^Audio: \d{8}-\d{6}\.webm\nInserted: yes\n\n/);
  await expect.poll(() => read(docOf(folder))).toContain(`\n\n${WORDS}\n`);
}

/** The comments column's line: typing in it never records (R is a letter there), Enter adds a To do at the heading
 * above the document's caret. */
async function typedComment(page: Page, heading: string): Promise<void> {
  await line(page).click();
  await page.keyboard.type('Keep it under a minute, really');
  expect(await page.evaluate(() => window.__review?.seg ?? null)).toBeNull();
  await page.keyboard.press('Enter');
  const item = page.locator('#side .item', { hasText: 'Keep it under a minute, really' });
  await expect(item).toBeVisible({ timeout: 10_000 });
  await expect(item.locator('.time.anchor')).toHaveText(heading);
  await expect(line(page)).toHaveValue('');
}

/** ⋯ and one of its items. */
async function menu(page: Page, item: 'ignition-start-v1' | 'ignition-undo'): Promise<void> {
  await more(page).click();
  await page.locator(`[data-part~="ignition-menu"] [data-part="${item}"]`).click();
}

/** The agent's question under the talk-out's comment. */
const question = (page: Page) =>
  page
    .locator('#side .item', { hasText: 'Work this talk-out into the document.' })
    .locator('[data-part="question-card"]');

/** Send: the agent writes the outline and the script into the document and asks which opening. */
async function send(page: Page, folder: string): Promise<void> {
  await page.locator('#done').click();
  const card = question(page);
  await expect(card).toContainText('Which opening?', { timeout: 60_000 });
  await expect(card.locator('.choice')).toHaveCount(2);
  const doc = read(docOf(folder));
  expect(doc).toContain('## Outline');
  expect(doc).toContain('## Script\n\n### Cold open');
  expect(doc).toContain(WORDS); // the person's words stay
  await shows(page, /### Cold open/);
  // The question shows in the document too, under its heading, with its two pictures.
  const inline = page.locator('[data-ext="ignition"] [data-part="ignition-question"]');
  await expect(inline).toContainText('Which opening?', { timeout: 10_000 });
  await expect(inline.locator('[data-part="ignition-choice"]')).toHaveCount(2);
  await expect
    .poll(() => inline.locator('img').evaluateAll((im) => im.map((i) => (i as HTMLImageElement).naturalWidth)))
    .toEqual([320, 320]);
  // The question's place shows it in the document, caret on it.
  await card.locator('.ask-time', { hasText: 'Cold open' }).click();
  await expect.poll(async () => (await caretAt(page)) === (await value(page)).indexOf('### Cold open')).toBe(true);
}

/** A tap answers it (in the comments column, or in the document); the agent writes the answer in, and the comment is
 * Done (no render). */
async function answer(page: Page, folder: string, inDoc = false): Promise<void> {
  const inline = page.locator('[data-ext="ignition"] [data-part="ignition-question"]');
  const choice = inDoc
    ? inline.locator('[data-part="ignition-choice"]', { hasText: 'Start on the jump' })
    : question(page).locator('.choice', { hasText: 'Start on the jump' });
  await choice.click();
  await expect(inline).toHaveCount(0, { timeout: 20_000 });
  await expect.poll(() => read(docOf(folder)), { timeout: 60_000 }).toContain('[Start on the jump.]');
  await expect
    .poll(
      async () => {
        const r = await page.request.get(new URL('api/notes?v=v0', page.url()).href);
        const items = ((await r.json()) as { items: { text: string; status: string }[] }).items;
        return items.find((i) => i.text.startsWith('Work this talk-out'))?.status;
      },
      { timeout: 30_000 },
    )
    .toBe('answered');
}

/** ⋯ → Undo the agent's edit: the document as it was before the agent's newest edit. */
async function undo(page: Page, folder: string): Promise<void> {
  await shows(page, /\[Start on the jump\.\]/);
  await menu(page, 'ignition-undo');
  await expect.poll(() => read(docOf(folder))).toContain('[The jump.]');
  await shows(page, /\[The jump\.\]/);
}

/** ⋯ → Start v1: the agent's video becomes v1/<folder>_v1.mp4, and the page goes there. */
async function startV1(page: Page, folder: string): Promise<void> {
  await expect(more(page)).toBeVisible({ timeout: 10_000 });
  await menu(page, 'ignition-start-v1');
  const name = folder.split('/').pop();
  await expect.poll(() => existsSync(join(folder, 'v1', `${name}_v1.mp4`)), { timeout: 90_000 }).toBe(true);
  expect(read(join(folder, 'v0', 'cuts', 'v1.json'))).toContain('Cold open');
  await page.waitForFunction(() => window.__review?.view?.shown === 'v1', null, { timeout: 60_000 });
}

test.describe('locally', () => {
  let server: Server;
  let cfg: IgnitionConfig;
  let dir = '';
  const idea = () => join(dir, 'deadlock', 'jump-souls');
  const videoUrl = () => `${server.url}p/deadlock-jump-souls/`;

  test.beforeAll(async () => {
    cfg = ignitionConfig('ignition-local');
    expect(readFileSync(cfg.file, 'utf8')).toMatch(/\[extensions\][^[]*enabled = \["ignition"\]/);
    dir = videos('ignition-local');
    server = await startServer('ignition', dir, PORTS.ignition, ['--verbose'], { env: { XDG_CONFIG_HOME: cfg.home } });
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

      // The home: the idea in Ignition's section.
      await page.goto(server.url);
      const sec = page.locator('[data-part="nav-ext"][data-ext="ignition"]');
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
      await expect(page.locator('[data-part="nav-ext"][data-ext="ignition"]')).toBeVisible();
      // The drawer slides in: measure it once it's there.
      await expect
        .poll(async () => (await page.locator('[data-part="nav"]').boundingBox())?.x)
        .toBeGreaterThanOrEqual(0);
      await noOverflow(page, 'the home with its drawer open');
      await shot(page, 'nav', PHONES[0]);
      await page.goto(`${server.url}#ext/ignition/`);
      await expect(page.locator('[data-part="ignition-idea"]', { hasText: 'Jump souls' })).toBeVisible({
        timeout: 20_000,
      });
      await noOverflow(page, 'the ideas page');
      await shot(page, 'ideas', PHONES[0]);
      await page.goto(`${server.url}#ext/ignition/new`);
      await page.locator('[data-part="ignition-title"]').fill('A much longer title for a video about souls');
      await noOverflow(page, 'New idea');
      await shot(page, 'new-idea', PHONES[0]);
    } finally {
      await phone.context.close();
    }
  });

  test('a talk-out lands in the document as a plain paragraph and adds a To do', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(videoUrl());
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await talkOut(page, idea());
    } finally {
      await context.close();
    }
  });

  test('Send: the agent shapes the document, asks about Cold open; a tap answers; ⋯ undoes; a typed comment', async ({
    browser,
  }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(videoUrl());
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await send(page, idea());
      await shot(page, 'editor-question');

      // The same on phones, while the question is open: the document, the drawer with the question, the ⋯ menu.
      for (const size of PHONES) {
        const phone = await newPage(browser, size);
        try {
          const p = phone.page;
          await p.goto(videoUrl());
          await shows(p, /### Cold open/, 20_000);
          await expect(p.locator('#sideToggle')).toBeVisible();
          await expect(p.locator('[data-ext="ignition"] [data-part="ignition-question"] img')).toHaveCount(2);
          await noOverflow(p, `the document at ${size.width}`);
          await phoneSized(p);
          await preview(p, 'Jump souls');
          await shot(p, 'editor', size);
          await p.locator('#sideToggle').click();
          const card = question(p);
          await expect(card).toContainText('Which opening?');
          await expect(p.locator('#side.drawer [data-part="agent-status"]')).toBeVisible();
          await noOverflow(p, `the comments drawer at ${size.width}`);
          await shot(p, 'drawer-question', size);
          // Its place closes the drawer and shows it in the document.
          await card.locator('.ask-time').click();
          await expect(card).toBeHidden();
          await more(p).click();
          await expect(p.locator('[data-part~="ignition-menu"]')).toBeVisible();
          await expect(p.locator('[data-part="ignition-start-v1"]')).toBeVisible();
          await noOverflow(p, `the ⋯ menu at ${size.width}`);
          await shot(p, 'menu', size);
        } finally {
          await phone.context.close();
        }
      }

      await answer(page, idea(), true);
      await undo(page, idea());
      // A comment typed in the comments column points where the caret is (the question's place put it there).
      await typedComment(page, 'Cold open');
      await paste(page, idea());
      await shot(page, 'editor-image');
    } finally {
      await context.close();
    }
  });

  test('Start v1 from ⋯ builds the first cut; v1 opens, with v0 · Doc first', async ({ browser }) => {
    const { context, page } = await newPage(browser);
    try {
      await page.goto(videoUrl());
      await expect(text(page)).toBeVisible({ timeout: 20_000 });
      await startV1(page, idea());
      const strip = page.locator('[data-part="version-strip"]');
      await expect(strip).toContainText('v0 · Doc');
      await expect(strip).toContainText('v1');
      // The Script tab on v1: the document's ## Script, a section per beat, linked by cuts/v1.json.
      await page.locator('[data-ext-tab="ignition/sidebar-tab"]').click();
      const sections = page.locator('[data-part="ignition-section"]');
      await expect(sections).toHaveCount(2, { timeout: 10_000 });
      await expect(sections.first()).toContainText('Cold open');
      await expect(sections.nth(1)).toContainText('The trick');
      await page.locator('#verBtn').click();
      await expect(page.locator('#verList [data-ver="v0"]')).toContainText('v0 · Doc');
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
      await expect(p.locator('#verList [data-ver="v0"]')).toContainText('v0 · Doc');
      await noOverflow(p, 'the version list');
      await shot(p, 'versions', PHONES[0]);
      // v0 from there: the document again, and the drawer has the versions (the strip) and the agent.
      await p.locator('#verList [data-ver="v0"]').click();
      await expect(text(p)).toBeVisible({ timeout: 20_000 });
      await p.locator('#sideToggle').click();
      await expect(p.locator('#side.drawer [data-part="version-strip"]')).toContainText('v0 · Doc');
      await expect(p.locator('#side.drawer [data-part="version-strip"]')).toContainText('v1');
      await expect(p.locator('#side.drawer [data-part="agent-status"]')).toBeVisible();
      await noOverflow(p, 'the drawer on v0 with v1');
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
  const DATA = join(WORK, 'ignition-hosted', 'server');
  const BASE = `http://127.0.0.1:${PORTS.ignitionHosted}`;
  let server: Server;
  let connect: Server | null = null;
  let cfg: IgnitionConfig;
  let dir = '';
  const idea = () => join(dir, 'deadlock', 'jump-souls');

  const admin = (...args: string[]) =>
    JSON.parse(
      execFileSync(RETAKE_BIN, [...args, '--data', DATA, '--public-url', BASE, '--json'], {
        env: { ...process.env, XDG_CONFIG_HOME: cfg.home },
      }).toString(),
    ) as { link: string };

  test.beforeAll(async () => {
    cfg = ignitionConfig('ignition-hosted');
    dir = videos('ignition-hosted');
    admin('user', 'add', 'owner@example.com', '--name', 'Owner', '--admin');
    server = await startTeamServer('ignition-hosted', PORTS.ignitionHosted, [
      '--data',
      DATA,
      '--public-url',
      BASE,
      '--extensions',
      'ignition',
    ]);
  });
  test.afterAll(async () => {
    await connect?.stop();
    await server?.stop();
    expect(pageErrors).toEqual([]);
  });

  test('the same through retake connect: New idea, typing, a talk-out, Send, a question, undo, Start v1, an old v0', async ({
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
        'ignition-connect',
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
      await talkOut(page, idea());
      // The audio went to the computer and was transcribed there; the server keeps none of it.
      expect(readdirSync(join(idea(), 'v0', 'voice')).some((f) => f.endsWith('.words.json'))).toBe(true);
      await send(page, idea());
      await answer(page, idea());
      await undo(page, idea());
      await paste(page, idea());
      await startV1(page, idea());

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
