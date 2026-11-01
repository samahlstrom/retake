// An agent's question from inside its task (spec/automation.md, "Questions from inside a task"): under its comment,
// with a thumbnail of the frame and the agent's drawing, and as a "?" on the timeline at its moment. Clicking the
// marker seeks there and puts the agent's box and arrow on the video in their own style; tapping a choice answers
// the same task. Desktop and phone screenshots go to .retake-work/ask-shots/.
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Page, type Route } from '@playwright/test';
import { LEGACY, PORTS, REPO } from '../lib/env.ts';
import { family } from '../lib/work.ts';
import { startServer, type Server } from '../lib/server.ts';
import { READY } from '../lib/review.ts';

test.skip(LEGACY, "agents' questions are the current player");
let server: Server;
const SHOTS = join(REPO, '.retake-work', 'ask-shots');
const json = (r: Route, body: unknown) => r.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });

/** Two flat colour PNGs, for the agent's two options and the frame still (SVG would be simpler, but the API serves
 * only PNG, JPEG, WebP and GIF). */
function png(page: Page, colour: string): Promise<Buffer> {
  return page.evaluate(async (c) => {
    const cv = document.createElement('canvas');
    cv.width = 160;
    cv.height = 90;
    const g = cv.getContext('2d')!;
    g.fillStyle = c;
    g.fillRect(0, 0, 160, 90);
    g.fillStyle = '#fff';
    g.font = 'bold 22px sans-serif';
    g.fillText('TITLE', 40, 54);
    const url = cv.toDataURL('image/png');
    return Array.from(atob(url.split(',')[1]!), (ch) => ch.charCodeAt(0));
  }, colour).then((bytes) => Buffer.from(bytes));
}

const task = { id: 'task-ask', notes: [1], version: 'v6', state: 'running', agent: 'claude', worker: 'machine', attempt: 1,
  events: [], asked: [{ id: 31, note: 1 }] };
const ask = {
  id: 31,
  task: 'task-ask',
  t: 4,
  from: 3,
  to: 6,
  version: 'v6',
  text: 'Should the title be blue or teal?',
  visuals: {
    boxes: [{ box: [0.08, 0.12, 0.42, 0.22], label: 'this title' }],
    arrows: [{ from: [0.86, 0.78], to: [0.55, 0.3] }],
    options: [
      { text: 'Blue', image: { url: 'api/questions/31/option_1.png' } },
      { text: 'Teal', image: { url: 'api/questions/31/option_2.png' } },
    ],
    frame: 'api/questions/31/frame.jpg',
  },
};

test.beforeAll(async () => {
  const f = family('ask', ['v5', 'v6']);
  server = await startServer('ask', f.files.v6!, PORTS.sidebar);
});
test.afterAll(async () => {
  await server?.stop();
});

async function fake(page: Page) {
  const answers: unknown[] = [];
  const blue = await png(page, '#2255dd');
  const teal = await png(page, '#159a8c');
  const frame = await png(page, '#3a3a44');
  await page.route('**/api/questions/31/*', (r) => {
    const name = new URL(r.request().url()).pathname.split('/').at(-1);
    return r.fulfill({ contentType: 'image/png', body: name === 'option_1.png' ? blue : name === 'option_2.png' ? teal : frame });
  });
  await page.route('**/api/events*', (r) => r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 60000\n\n' }));
  await page.route('**/api/automation', (r) =>
    json(r, { local: true, config: { worker: 'machine', agent: 'claude', enabled: false }, renders: [], sending: [],
      workers: [{ id: 'machine', name: 'Editing computer', seen: Date.now() / 1000, online: true, agents: [{ id: 'claude', name: 'Claude Code' }] }],
      tasks: [answers.length ? { ...task, state: 'queued' } : task] }),
  );
  await page.route('**/api/automation/tasks/*/answer', (r) => {
    answers.push(JSON.parse(r.request().postData()!));
    return json(r, { ok: true });
  });
  await page.route('**/api/notes?*', (r) =>
    json(r, { version: 'v6', newest: true, scope: 'review', jobs: [], items: [
      { id: 1, kind: 'note', status: answers.length ? 'open' : 'question', t0: 2, t1: 3, text: 'Make the title stand out more.', by: 'Alex',
        version: 'v6', work: answers.length ? { ...task, state: 'queued' } : task, question: ask.text, ask: answers.length ? undefined : ask },
    ] }),
  );
  return answers;
}

test("the agent's question: under its comment, on the timeline, on the video in its own style, answered with a tap", async ({ page }) => {
  mkdirSync(SHOTS, { recursive: true });
  await page.goto(server.url);
  await page.waitForFunction(READY);
  const answers = await fake(page);
  await page.reload();
  await page.waitForFunction(READY);

  const card = page.locator('.item[data-key="note:1"]');
  await expect(card.locator('.status')).toHaveText('Question');
  const q = card.locator('.agent-ask');
  await expect(q.locator('.ask-text')).toContainText('Agent');
  await expect(q.locator('.ask-text')).toContainText('Should the title be blue or teal?');
  await expect(q.locator('.ask-shot .agent-box')).toHaveCount(1); // the thumbnail: the frame with the agent's drawing
  await expect(q.locator('.choice')).toHaveCount(2);
  await expect(q.locator('.choice img')).toHaveCount(2);
  await expect(q.getByRole('textbox', { name: 'Answer the agent' })).toBeVisible();
  await expect(q.getByRole('button', { name: 'Answer out loud' })).toBeVisible();

  // The marker sits at the question's own moment (4 s), not the comment's (2 s); clicking it seeks there.
  const marker = page.locator('#timeline .ask-marker');
  await expect(marker).toHaveCount(1);
  await page.locator('video').evaluate((v: HTMLVideoElement) => { v.pause(); v.currentTime = 9; });
  await expect(page.locator('#agentLayer')).toHaveCount(0); // out of its shot, nothing of the agent's on the video
  await marker.click();
  await expect.poll(() => page.locator('video').evaluate((v: HTMLVideoElement) => v.currentTime)).toBeCloseTo(4, 1);
  await expect(marker).toHaveClass(/on/);
  const layer = page.locator('#agentLayer');
  await expect(layer).toBeVisible();
  await expect(layer.locator('.agent-box')).toHaveCount(1);
  await expect(layer.locator('.agent-arrow')).toHaveCount(1);
  await expect(layer.locator('.agent-chip text').first()).toHaveText('this title');
  await expect(layer.locator('.agent-chip text').nth(1)).toHaveText('Agent'); // an arrow without a label says whose
  // Its own style: the Question violet (the theme's --agent), dashed, never a pen colour.
  const style = await layer.locator('.agent-box').evaluate((el) => ({ stroke: getComputedStyle(el).stroke, dash: el.getAttribute('stroke-dasharray') }));
  expect(style).toEqual({ stroke: 'rgb(166, 147, 250)', dash: '6 4' });
  // It takes no pointer: the pen (while recording) goes over it.
  expect(await layer.evaluate((el) => getComputedStyle(el).pointerEvents)).toBe('none');
  await page.waitForTimeout(1000); // the arrival highlight is over
  await page.screenshot({ path: join(SHOTS, 'desktop.png') });

  // Phone: the same card in the list under the controls.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#sideToggle')).toBeVisible(); // phones: the list opens under the controls
  await page.locator('#sideToggle').click();
  await expect(page.locator('#side .agent-ask')).toBeVisible();
  const box = await page.locator('#side .agent-ask').boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  await page.locator('#side .agent-ask .choices').scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(SHOTS, 'phone.png'), fullPage: true });
  await page.setViewportSize({ width: 1920, height: 1080 });
  if (!(await q.isVisible())) await page.locator('#sideToggle').click();

  // One tap answers the same task with the choice's words.
  await q.locator('.choice', { hasText: 'Teal' }).click();
  await expect.poll(() => answers).toEqual([{ question: 31, message: 'Teal' }]);
  await expect(card.locator('.status')).toHaveText('Working');
  await expect(page.locator('#timeline .ask-marker')).toHaveCount(0);
});
