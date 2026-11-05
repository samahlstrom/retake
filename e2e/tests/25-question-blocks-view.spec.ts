// A completed target is not ready to view while a source comment is still an unanswered question. Task completion and
// notes are delivered in separate reloads to mirror a reviewer returning after each independent server update.
import { expect, test, type Page, type Route } from '@playwright/test';
import { LEGACY, PORTS } from '../lib/env.ts';
import { READY } from '../lib/review.ts';
import { startServer, type Server } from '../lib/server.ts';
import { family, type Family } from '../lib/work.ts';

test.describe.configure({ mode: 'serial' });
test.skip(LEGACY, 'new: a completed target waits for unanswered questions');
test.skip(({ browserName }) => browserName !== 'chromium', 'Chromium only: generated v5/v6 media');

let server: Server;
let fam: Family;
let task = 'running';
let questioned = false;
let answered = false;

const shown = (page: Page) => page.evaluate(() => window.__review.view.shown);
const json = (route: Route, body: unknown) =>
  route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify(body),
  });

async function chooseSource(page: Page): Promise<void> {
  await page.locator('#verBtn').click();
  await page.locator('[data-part~="version-list"] [data-version="v5"]').click();
  await expect.poll(() => shown(page)).toBe('v5');
}

async function open(page: Page): Promise<void> {
  await page.goto(server.url);
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  await chooseSource(page);
}

async function mock(page: Page): Promise<void> {
  await page.route('**/api/events*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: 'retry: 60000\n\n',
    }),
  );
  await page.route('**/api/automation', (route) =>
    json(route, {
      tasks: [
        {
          id: 'question-task',
          agent: 'codex', worker: 'test-worker', attempt: 1, events: [],
          notes: [41],
          version: 'v5',
          target: 'v6',
          state: task,
        },
      ],
      renders: [{ label: 'v6', task: 'question-task', complete: true }],
      config: { enabled: false, worker: 'test-worker', agent: 'codex' },
      workers: [],
      sending: [],
    }),
  );
  await page.route('**/api/notes?*', (route) => {
    const version = new URL(route.request().url()).searchParams.get('v') ?? '';
    const item = questioned
      ? {
          id: 41,
          kind: 'note',
          status: 'question',
          version: 'v5',
          t0: 6,
          t1: 6,
          by: 'agent',
          text: 'Keep the close-up?',
          question: 'Keep the close-up?',
          ask: {
            id: 71,
            task: 'question-task',
            t: 6,
            version: 'v5',
            text: 'Keep the close-up?',
          },
        }
      : {
          id: 41,
          kind: 'note',
          status: 'open',
          version: 'v5',
          t0: 6,
          t1: 6,
          by: 'Reviewer',
          text: 'Keep the close-up?',
        };
    return json(route, {
      version,
      newest: version === 'v6',
      scope: 'video',
      items: answered ? [
        { ...item, status: 'answered', answered_in: 'v6', change: 'Close-up kept' },
        { id: 71, kind: 'question', status: 'answered', version: 'v5', t0: 6, t1: 6, text: 'Keep the close-up?', answered_in: 'v6' },
      ] : [item],
    });
  });
  await page.route('**/api/automation/tasks/*/answer', (route) => json(route, { ok: true }));
}

test.beforeAll(async () => {
  fam = family('question-blocks-view', ['v5', 'v6']);
  server = await startServer('question-blocks-view', fam.files.v6!, PORTS.keyboard);
});
test.afterAll(async () => {
  await server?.stop();
});

test('a finished v6 waits on a v5 question and reply without navigating the reviewer', async ({ page }) => {
  task = 'running';
  questioned = false;
  answered = false;
  await mock(page);
  await open(page);
  await expect(page.getByRole('button', { name: 'View v6', exact: true })).toHaveCount(0);

  // Completion arrives first. Its completed render is real generated v6 media, but the open source note still blocks it.
  task = 'completed';
  await page.reload();
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  await expect.poll(() => shown(page)).toBe('v5');
  await expect(page.getByRole('button', { name: 'View v6', exact: true })).toHaveCount(0);

  // The task's question arrives later. It and its reply stay on v5 and still block View v6.
  questioned = true;
  await page.reload();
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  await expect.poll(() => shown(page)).toBe('v5');
  const card = page.locator('#side .item[data-key="note:41"]');
  await expect(card.locator('.status')).toHaveText('Question');
  await expect(card.getByRole('textbox', { name: 'Answer the agent' })).toBeVisible();
  await expect(card.getByRole('textbox', { name: 'Answer the agent' })).toHaveValue('');
  await expect(page.getByRole('button', { name: 'View v6', exact: true })).toHaveCount(0);
});

// Resolved question history must not keep an otherwise finished batch blocked forever.
test('View v6 becomes available after the source note and question are answered', async ({ page }) => {
  task = 'completed';
  questioned = true;
  answered = true;
  await mock(page);
  await open(page);
  await expect.poll(() => shown(page)).toBe('v5');
  await expect(page.getByRole('button', { name: 'View v6', exact: true })).toBeVisible();
});
