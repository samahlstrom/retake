// Editing a comment's words (spec/notes.md, "Editing a comment"), for 24-edit.spec.ts (Chromium: a computer and the
// iPhone 15) and 24-edit-webkit.spec.ts (WebKit, the iPhone 15):
// - a computer: a voice comment (R, talk, R: a fake transcriber's words) edited through ⋯ → Edit and a typed one by a
//   double-click on its words; the field is where the words were, as tall, and no card moves; keys typed in it never
//   reach the player; Enter saves, Shift+Enter is a new line, Esc changes nothing; the final pass of the take (Send,
//   which saves it here) and a reload keep the new words;
// - a phone: a tap on a comment's words seeks and pauses without opening compose mode; ⋯ → Edit opens the comment box
//   on the keyboard (lib/keyboard.ts simulates it), where Return saves and Cancel changes nothing.
import { expect, type Page } from '@playwright/test';
import { linkSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { mediaLayout } from './fixtures.ts';
import { AUTHOR, REPO, WORK } from './env.ts';
import { KB, keyboard, keyboardDown } from './keyboard.ts';
import { READY } from './review.ts';
import { startServer, type Server } from './server.ts';

export const SHOTS = join(REPO, '.retake-work', 'edit-shots');

/** The fake transcriber: the same few words, whatever the audio. */
const WORDS = 'Make this part shorter please';
const TRANSCRIBER = `const words = ${JSON.stringify(WORDS)}.split(' ');
process.stdout.write(JSON.stringify({ words: words.map((w, i) => ({ w: ' ' + w, s: i * 0.25, e: i * 0.25 + 0.25, p: 0.9 })) }));
`;

/** A plain project `promo` (promo_v1.mp4, the v6 fixture) with the fake transcriber, on `port`. No agent: Send saves. */
export async function editServer(name: string, port: number): Promise<Server> {
  const dir = join(WORK, 'edit', name);
  rmSync(dir, { recursive: true, force: true });
  const video = join(dir, 'promo');
  const config = join(dir, 'config');
  for (const d of [video, join(config, 'retake')]) mkdirSync(d, { recursive: true });
  linkSync(mediaLayout(WORK).v6, join(video, 'promo_v1.mp4'));
  writeFileSync(join(dir, 'fake-words.mjs'), TRANSCRIBER);
  writeFileSync(join(config, 'retake', 'config.toml'), `name = "${AUTHOR}"
open_browser = false
find_agents = false

[transcribe]
engine = "command"
command = "node ${join(dir, 'fake-words.mjs')}"
`);
  return startServer(`edit-${name}`, join(video, 'promo_v1.mp4'), port, [], { env: { XDG_CONFIG_HOME: config } });
}

/** Typed comments on v1 through the API, as the comment box makes them; their ids. */
export async function addComments(url: string, comments: [number, string][]): Promise<number[]> {
  const ids: number[] = [];
  for (const [t, text] of comments) {
    const r = await fetch(`${url}api/notes`, { method: 'POST', body: JSON.stringify({ version: 'v1', t, text }) });
    expect(r.status, text).toBe(201);
    ids.push(((await r.json()) as { id: number }).id);
  }
  return ids;
}

type Box = { x: number; y: number; width: number; height: number };
const near = (a: Box, b: Box, what: string) => {
  for (const k of ['x', 'y', 'width', 'height'] as const) expect(Math.abs(a[k] - b[k]), `${what}: ${k}`).toBeLessThanOrEqual(1);
};

/** Every comment card's box, by its key. */
const cards = (page: Page) =>
  page.locator('#side .item').evaluateAll((els) =>
    Object.fromEntries(
      els.map((el) => {
        const b = el.getBoundingClientRect();
        return [(el as HTMLElement).dataset.key ?? '', { x: b.x, y: b.y, width: b.width, height: b.height }];
      }),
    ),
  );

/** The words of the comment `key` on the server: what the agent reads. */
async function serverWords(page: Page, url: string, key: string): Promise<{ text: string; edited?: boolean }> {
  const r = await page.request.get(`${url}api/notes?v=v1&scope=video`);
  const items = ((await r.json()) as { items: { kind: string; id: number; text: string; edited?: boolean }[] }).items;
  const it = items.find((i) => `${i.kind}:${i.id}` === key);
  if (!it) throw new Error(`no ${key} on the server`);
  return it;
}

/** A computer: edit a voice comment and a typed one, keep the words through the take's final pass and a reload. */
export async function desktopEdit(page: Page, url: string, shot: (what: string) => Promise<void>): Promise<void> {
  await page.goto(url);
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  const items = page.locator('#side .item');

  // A voice comment: R, talk, R; its words come back as a comment.
  await page.locator('video').evaluate((v: HTMLVideoElement) => {
    v.pause();
    v.currentTime = 2;
  });
  await page.keyboard.press('r');
  await page.waitForTimeout(1800);
  await page.keyboard.press('r');
  await expect(items.filter({ hasText: WORDS })).toHaveCount(1, { timeout: 30_000 });
  const voiceKey = (await items.filter({ hasText: WORDS }).getAttribute('data-key'))!;
  // A typed one, later in the video.
  await page.locator('video').evaluate((v: HTMLVideoElement) => {
    v.pause();
    v.currentTime = 9;
  });
  await page.locator('[data-part~="comment-line"] textarea').click();
  await page.keyboard.type('Too loud here');
  await page.keyboard.press('Enter');
  await expect(items).toHaveCount(2, { timeout: 10_000 });
  const typedKey = (await items.filter({ hasText: 'Too loud here' }).getAttribute('data-key'))!;
  const voice = page.locator(`#side .item[data-key="${voiceKey}"]`);
  const typed = page.locator(`#side .item[data-key="${typedKey}"]`);
  await page.locator('video').evaluate((v: HTMLVideoElement) => v.pause());
  await page.waitForTimeout(300);
  await shot('desktop-before');

  // ⋯ → Edit: the words become a field in their place, as tall; no card moves.
  const before = await cards(page);
  const words = (await voice.locator('.text').boundingBox())!;
  await voice.hover();
  await voice.locator('[data-part~="more"]').click();
  await page.getByRole('menuitem', { name: 'Edit' }).click();
  const field = voice.locator('[data-part="comment-edit"]');
  await expect(field).toBeFocused();
  await expect(field).toHaveValue(WORDS);
  near((await field.boundingBox())!, { ...words, x: words.x - 6, width: words.width + 12 }, 'the field is where the words were');
  const editing = await cards(page);
  for (const [k, b] of Object.entries(before)) near(editing[k]!, b, `card ${k} while editing`);
  await expect(voice.locator('[data-part~="more"]')).toHaveCount(0);
  await expect(voice.getByRole('button', { name: 'Save' })).toBeVisible();
  await shot('desktop-editing');

  // Keys typed in it are words: R doesn't record, Space doesn't play.
  await field.press('End');
  await page.keyboard.type(' r');
  await expect(field).toHaveValue(`${WORDS} r`);
  expect(await page.evaluate(() => window.__review.seg)).toBeNull();
  expect(await page.locator('video').evaluate((v: HTMLVideoElement) => v.paused)).toBe(true);
  // Enter saves; Shift+Enter is a new line.
  await field.fill('Make the intro shorter, cut two seconds');
  await field.press('Shift+Enter');
  await page.keyboard.type('and keep the music');
  await field.press('Enter');
  await expect(field).toHaveCount(0);
  const edited = 'Make the intro shorter, cut two seconds\nand keep the music';
  await expect(voice.locator('.text')).toHaveText(edited);
  await expect(voice.locator('.text')).toHaveAttribute('title', 'Edited');
  await expect(voice.locator('.edit-badge, .edited')).toHaveCount(0);
  expect(await serverWords(page, url, voiceKey)).toMatchObject({ text: edited, edited: true });

  // A double-click on the typed one's words; Esc changes nothing.
  await typed.locator('.text').dblclick();
  const field2 = typed.locator('[data-part="comment-edit"]');
  await expect(field2).toBeFocused();
  await field2.fill('never mind');
  await field2.press('Escape');
  await expect(field2).toHaveCount(0);
  await expect(typed.locator('.text')).toHaveText('Too loud here');
  // Again, and Save.
  await typed.locator('.text').dblclick();
  await field2.fill('Too loud here, bring the music down 6 dB');
  await typed.getByRole('button', { name: 'Save' }).click();
  await expect(typed.locator('.text')).toHaveText('Too loud here, bring the music down 6 dB');
  await shot('desktop-saved');

  // Send (here: save, no agent) closes the take: its final pass runs the notes engine again. The words stay, and a
  // reload shows them.
  await page.locator('#done').click();
  await expect
    .poll(async () => ((await (await page.request.get(`${url}api/notes?v=v1`)).json()) as { jobs: unknown[] }).jobs.length, {
      timeout: 30_000,
    })
    .toBe(0);
  await page.reload();
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  await expect(voice.locator('.text')).toHaveText(edited);
  await expect(typed.locator('.text')).toHaveText('Too loud here, bring the music down 6 dB');
  expect(await serverWords(page, url, voiceKey)).toMatchObject({ text: edited, edited: true });
}

const COMMENT_BOX = '#side [data-part~="comment-line"] textarea';
const COMPOSER = '#side [data-part~="comment-line"]';
const FADES = ['#pbar', '#precord', '#side > header', '#side > .list'];

const viewport = (page: Page) =>
  page.evaluate(() => (window as unknown as { __review: { viewport: Record<string, unknown> } }).__review.viewport);
const bottomOf = (page: Page, sel: string) =>
  page.locator(sel).first().evaluate((el) => Math.round(el.getBoundingClientRect().bottom * 10) / 10);
const hidden = (page: Page, sel: string) =>
  page.locator(sel).first().evaluate((el) => {
    const s = getComputedStyle(el);
    return s.visibility === 'hidden' && s.opacity === '0';
  });

/** A phone: a text tap seeks without composing; ⋯ → Edit opens the comment box on the keyboard, and Cancel works. */
export async function phoneEdit(page: Page, url: string, where: string, shot: (what: string) => Promise<void>): Promise<void> {
  const [hold, loud] = await addComments(url, [
    [12, `Hold the logo a beat longer (${where})`],
    [14, `Too loud here (${where})`],
  ]);
  await page.goto(url);
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  const card = (id: number) => page.locator(`#side .item[data-key="note:${id}"]`);
  await expect(card(hold!)).toBeVisible({ timeout: 10_000 });
  const inner = await page.evaluate(() => innerHeight);
  const box = page.locator(COMMENT_BOX);

  // A tap on its words seeks and pauses. It neither opens compose mode nor puts text in the bottom composer.
  await page.locator('video').evaluate((v: HTMLVideoElement) => { v.currentTime = 2; return v.play(); });
  await card(hold!).locator('.text').tap();
  await expect.poll(() => viewport(page).then((v) => v.compose), { message: `${where}: text tap does not compose` }).toBe(false);
  await expect(box).not.toBeFocused();
  await expect(box).toHaveValue('');
  await expect.poll(() => page.locator('video').evaluate((v: HTMLVideoElement) => ({ paused: v.paused, t: v.currentTime })))
    .toMatchObject({ paused: true, t: expect.closeTo(12, 1) });
  for (const sel of FADES) await expect.poll(() => hidden(page, sel), { message: `${where}: ${sel} stays visible` }).toBe(false);
  await shot('phone-text-seek');

  // ⋯ → Edit is the explicit way to compose: its words go in the box, which sits on the keyboard; the rest is hidden.
  await card(hold!).locator('[data-part~="more"]').tap();
  await page.getByRole('menuitem', { name: 'Edit' }).tap();
  await expect.poll(() => viewport(page).then((v) => v.compose), { message: `${where}: menu Edit composes` }).toBe(true);
  await expect(box).toBeFocused();
  await expect(box).toHaveValue(`Hold the logo a beat longer (${where})`);
  await expect(page.locator(`${COMPOSER} .under`)).toContainText('Editing 0:12');
  await keyboard(page, KB);
  await expect.poll(() => bottomOf(page, COMPOSER), { message: `${where}: the box sits on the keyboard` }).toBeCloseTo(inner - KB, 0);
  for (const sel of FADES) await expect.poll(() => hidden(page, sel), { message: `${where}: ${sel} is hidden` }).toBe(true);
  expect(await page.locator('video').evaluate((v: HTMLVideoElement) => v.paused)).toBe(true);
  await shot('phone-edit-keyboard');

  // Return saves: compose mode ends, the card has the new words, and the box is for a new comment again.
  await box.fill(`Hold the logo two beats longer (${where})`);
  await box.press('Enter');
  await expect.poll(() => viewport(page).then((v) => v.compose), { message: `${where}: Return ends it` }).toBe(false);
  await keyboardDown(page);
  await expect(card(hold!).locator('.text')).toHaveText(`Hold the logo two beats longer (${where})`);
  await expect(box).toHaveValue('');
  for (const sel of FADES) await expect.poll(() => hidden(page, sel)).toBe(false);
  await shot('phone-edit-saved');

  // ⋯ → Edit on another, then Cancel: nothing changes.
  await card(loud!).locator('[data-part~="more"]').tap();
  await page.getByRole('menuitem', { name: 'Edit' }).tap();
  await expect(box).toBeFocused();
  await expect(box).toHaveValue(`Too loud here (${where})`);
  await keyboard(page, KB);
  await expect.poll(() => bottomOf(page, COMPOSER)).toBeCloseTo(inner - KB, 0);
  await box.fill('never mind');
  await page.locator(`${COMPOSER} .under button`, { hasText: 'Cancel' }).tap();
  await expect.poll(() => viewport(page).then((v) => v.compose)).toBe(false);
  await keyboardDown(page);
  await expect(card(loud!).locator('.text')).toHaveText(`Too loud here (${where})`);
  await expect(box).toHaveValue('');

  // A reload keeps the new words.
  await page.reload();
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  await expect(card(hold!).locator('.text')).toHaveText(`Hold the logo two beats longer (${where})`);
}
