// The live activity line belongs after its running reply, in normal thread flow. This uses the Scripts fixture's
// generated media and an intercepted chat snapshot: no agent process is started for this layout regression.
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { mediaLayout } from '../lib/fixtures.ts';
import { scriptsConfig, type ScriptsConfig } from '../lib/scripts.ts';
import { PORTS, WORK } from '../lib/env.ts';
import { startServer, type Server } from '../lib/server.ts';

test.describe.configure({ mode: 'serial' });

const SHOTS = join(WORK, 'chat-activity-shots');
const PHONE = { width: 390, height: 844 };
const LONG_REPLY = Array.from(
  { length: 18 },
  (_, n) =>
    `Paragraph ${n + 1}: the agent keeps streaming enough ordinary words to make this reply taller than the chat thread.`,
).join('\n\n');

const VIEW = {
  messages: [
    {
      id: 1,
      role: 'person',
      text: 'Finish the opening.',
      created: '2026-01-01T00:00:00Z',
    },
    {
      id: 2,
      role: 'agent',
      turn: 'done',
      text: 'The completed reply stays readable.',
      created: '2026-01-01T00:00:01Z',
    },
    {
      id: 3,
      role: 'tool',
      turn: 'done',
      text: 'Editing the script',
      created: '2026-01-01T00:00:02Z',
    },
    {
      id: 4,
      role: 'person',
      text: 'Now make the middle tighter.',
      created: '2026-01-01T00:00:03Z',
    },
    {
      id: 5,
      role: 'agent',
      turn: 'live',
      text: LONG_REPLY,
      created: '2026-01-01T00:00:04Z',
      streaming: true,
    },
    {
      id: 6,
      role: 'agent',
      turn: 'live',
      text: LONG_REPLY,
      created: '2026-01-01T00:00:05Z',
      streaming: true,
    },
    {
      id: 7,
      role: 'tool',
      turn: 'live',
      text: 'Reading out.png',
      created: '2026-01-01T00:00:06Z',
    },
    {
      id: 8,
      role: 'person',
      text: 'This is the queued next message.',
      created: '2026-01-01T00:00:07Z',
      waiting: true,
    },
  ],
  turn: { id: 'live', state: 'running' },
  busy: '',
  offline: false,
  blocked: '',
  sent: null,
};

/** One Scripts project with generated media and a document version. */
function videos(name: string): string {
  const dir = join(WORK, name, 'videos');
  const project = join(dir, 'activity', 'long-reply');
  mkdirSync(join(project, 'v0'), { recursive: true });
  copyFileSync(mediaLayout(WORK).v5, join(project, 'long-reply_v1.mp4'));
  writeFileSync(join(project, 'v0', 'long-reply_v0.md'), '# Long reply\n\nA generated fixture.\n');
  return dir;
}

async function newPage(browser: Browser, phone = false): Promise<{ context: BrowserContext; page: Page }> {
  const context = phone
    ? await browser.newContext({
        viewport: PHONE,
        isMobile: true,
        hasTouch: true,
        deviceScaleFactor: 2,
      })
    : await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  await page.route('**/api/chat', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify(VIEW),
    }),
  );
  return { context, page };
}

type Layout = {
  liveCount: number;
  liveText: string;
  liveFollowsReply: boolean;
  livePrecedesQueuedPerson: boolean;
  donePrecedesReply: boolean;
  livePosition: string;
  replyBottom: number;
  lineTop: number;
  lineBottom: number;
  queuedTop: number;
};

/** The line is between the current reply and its queued successor; a completed summary remains above its reply. */
async function layout(page: Page): Promise<Layout> {
  const thread = page.locator('[data-part="chat-thread"]');
  await expect(thread.locator('[data-id="6"]')).toBeVisible();
  await thread.evaluate((el) => (el.scrollTop = el.scrollHeight));
  return thread.evaluate((el): Layout => {
    const one = (selector: string) => el.querySelector<HTMLElement>(selector)!;
    const follows = (before: Element, after: Element) =>
      !!(before.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING);
    const live = one('[data-part="chat-tool"][data-state="live"]');
    const latest = one('[data-part="chat-agent"][data-id="6"]');
    const queued = one('[data-part="chat-person"][data-id="8"]');
    const done = one('[data-part="chat-tool"][data-state="done"]');
    const completed = one('[data-part="chat-agent"][data-id="2"]');
    const reply = latest.getBoundingClientRect();
    const activity = live.getBoundingClientRect();
    const next = queued.getBoundingClientRect();
    return {
      liveCount: el.querySelectorAll('[data-part="chat-tool"][data-state="live"]').length,
      liveText: live.textContent ?? '',
      liveFollowsReply: follows(latest, live),
      livePrecedesQueuedPerson: follows(live, queued),
      donePrecedesReply: follows(done, completed),
      livePosition: getComputedStyle(live).position,
      replyBottom: reply.bottom,
      lineTop: activity.top,
      lineBottom: activity.bottom,
      queuedTop: next.top,
    };
  });
}

async function assertLiveActivity(page: Page, name: string): Promise<void> {
  const atEnd = await layout(page);
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `${name}.png`) });

  expect(atEnd.liveCount, `${name}: exactly one live activity line`).toBe(1);
  expect(atEnd.liveText, `${name}: the newest tool labels the line`).toBe('Reading out.png…');
  expect(atEnd.donePrecedesReply, `${name}: a completed summary stays above its reply`).toBe(true);
  expect(atEnd.liveFollowsReply, `${name}: live activity follows the latest agent words`).toBe(true);
  expect(atEnd.livePrecedesQueuedPerson, `${name}: live activity precedes the queued next message`).toBe(true);
  expect(atEnd.livePosition, `${name}: live activity stays in normal flow`).not.toBe('sticky');
  expect(atEnd.lineTop, `${name}: the line directly follows the long reply at scroll end`).toBeGreaterThanOrEqual(
    atEnd.replyBottom - 1,
  );
  expect(atEnd.lineTop, `${name}: the line is adjacent to the long reply at scroll end`).toBeLessThanOrEqual(
    atEnd.replyBottom + 16,
  );
  expect(atEnd.lineBottom, `${name}: the queued next message stays below the line`).toBeLessThanOrEqual(
    atEnd.queuedTop + 1,
  );

  const scrolledUp = await page.locator('[data-part="chat-thread"]').evaluate((el) => {
    el.scrollTop = 0;
    return (el.querySelector('[data-part="chat-tool"][data-state="live"]') as HTMLElement).getBoundingClientRect().top;
  });
  expect(scrolledUp, `${name}: scrolling up moves the activity line with its reply`).toBeGreaterThan(
    atEnd.lineTop + 200,
  );
}

async function openScript(page: Page, url: string): Promise<void> {
  await page.goto(url);
  await page.locator('#verBtn').click();
  await page.locator('#verList [data-ver="v0"]').click();
  await expect(page.locator('[data-part="chat"]')).toBeAttached();
}

test.describe('chat activity placement', () => {
  let cfg: ScriptsConfig;
  let server: Server;
  const url = () => `${server.url}p/activity-long-reply/`;

  test.beforeAll(async () => {
    cfg = scriptsConfig('chat-activity');
    const dir = videos('chat-activity');
    server = await startServer('chat-activity', dir, PORTS.scripts, ['--verbose'], {
      env: { XDG_CONFIG_HOME: cfg.home },
    });
  });
  test.afterAll(async () => {
    await server?.stop();
  });

  test('desktop sidebar keeps a live activity after its long reply and before the queued message', async ({
    browser,
  }) => {
    const { context, page } = await newPage(browser);
    try {
      await openScript(page, url());
      await assertLiveActivity(page, 'desktop');
    } finally {
      await context.close();
    }
  });

  test('phone chat sheet keeps a live activity after its long reply and before the queued message', async ({
    browser,
  }) => {
    const { context, page } = await newPage(browser, true);
    try {
      await openScript(page, url());
      await page.locator('#sideToggle').click();
      await expect(page.locator('#side.drawer.chatting')).toBeVisible();
      await expect(page.locator('#side.drawer [data-part="chat"]')).toBeVisible();
      await assertLiveActivity(page, 'phone');
    } finally {
      await context.close();
    }
  });
});
