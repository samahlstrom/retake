// Drives the review page like a person (real keys and mouse through Playwright) and reads its state through the
// `window.__review` test hook (spec/api.md), exactly as the legacy driver tests/e2e_headless.mjs did.
import type { Page } from '@playwright/test';

/** A sidebar row as the hook's `sidebar` lists it (milestone 2). */
export interface SidebarRow {
  key: string; id: number; kind: 'note' | 'question'; mark: 'answered' | 'question' | 'open'; time: string; t0: number;
  text: string; change: string | null; answeredIn: string | null; see: string | null; question: string | null;
  answer: number | null; reopen: boolean; options: { label: string; known: boolean; on: boolean }[];
}

/** The parts of the page's test hook this suite reads. */
export interface ReviewHook {
  take: { id: string | null; closed: boolean; saving: unknown; strokes: { undone?: boolean }[];
    /** Milestone 2 (new page only). */
    images?: { id: number; file: string; mime: string; video_t: number; box: [number, number, number, number];
      undone?: boolean; upload: string }[] };
  seg: unknown;
  /** Milestone 2 (new page only): the sidebar's data and what it shows. */
  notes?: { available: boolean; version: string | null; newest: boolean; items: { id: number }[];
    job: { text: string; error: boolean; hint: string | null } | null };
  sidebar?: { panel: boolean; toggle: boolean; phone: boolean; active: string | null; rows: SidebarRow[] };
  view: { base: string | null; shown: string | null; flipped: boolean; switching: boolean };
  info: { label?: string; name?: string; versions?: { label: string; name: string; duration: number | null }[] };
  hist: { strokes: unknown[]; images?: unknown[] };
  serialize: (take: unknown) => Record<string, unknown>;
  save: (take: unknown) => Promise<unknown>;
}
declare global {
  interface Window { __review: ReviewHook }
}

/** What the legacy driver's state() returned: the player, the view, and the visible toast. */
export interface State {
  t: number;
  paused: boolean;
  base: string | null;
  shown: string | null;
  flipped: boolean;
  switching: boolean;
  rec: boolean;
  take: string | null;
  strokes: number;
  toast: string;
}

export type Point = [number, number];

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** A closed loop (circle) in normalized video space, as the legacy driver drew it. */
export const loop = (cx: number, cy: number, r = 0.04, n = 36): Point[] =>
  Array.from({ length: n + 1 }, (_, i) => [cx + r * Math.cos((i / n) * 2 * Math.PI),
    cy + r * 1.7 * Math.sin((i / n) * 2 * Math.PI)]);
/** A straight line in normalized video space. */
export const line = (a: Point, b: Point, n = 12): Point[] =>
  Array.from({ length: n + 1 }, (_, i) => [a[0] + ((b[0] - a[0]) * i) / n, a[1] + ((b[1] - a[1]) * i) / n]);

/** The page is ready: the hook is there, info is loaded, and the video has a frame. */
export const READY = () => !!(window.__review && window.__review.info.label
  && (document.querySelector('video')?.readyState ?? 0) >= 2);

const KEY_NAMES: Record<string, string> = { ' ': 'Space' };

export class Review {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  async open(url: string): Promise<void> {
    await this.page.goto(url);
    await this.page.waitForFunction(READY, null, { timeout: 20_000, polling: 80 });
  }

  /** One key press (keydown + keyup), like a person typing it. */
  async key(k: string): Promise<void> {
    await this.page.keyboard.press(KEY_NAMES[k] ?? k);
  }

  /** A pen stroke: press, move through the points (normalized to the ink canvas), release. */
  async drag(points: Point[]): Promise<void> {
    const box = await this.page.locator('#ink').boundingBox();
    if (!box) throw new Error('#ink has no box');
    const P = points.map(([x, y]) => [box.x + x * box.width, box.y + y * box.height] as const);
    const m = this.page.mouse;
    await m.move(P[0]![0], P[0]![1]);
    await m.down();
    for (const [x, y] of P.slice(1)) {
      await m.move(x, y);
      await sleep(8);
    }
    await m.up();
  }

  async click(selector: string): Promise<void> {
    await this.page.locator(selector).click();
  }

  /** A finger tap (the context needs `hasTouch`). `force`: tap it even when it looks disabled (aria-disabled). */
  async tap(selector: string, opts: { force?: boolean } = {}): Promise<void> {
    await this.page.locator(selector).tap(opts);
  }

  /**
   * A one-finger drag through `points`, normalized to `selector`'s box (the pen canvas by default). Real touch input
   * through the Chrome DevTools protocol, so the page sees touch pointer events, as on a phone. Chromium only.
   */
  async touchDrag(points: Point[], selector = '#ink'): Promise<void> {
    const box = await this.page.locator(selector).boundingBox();
    if (!box) throw new Error(`${selector} has no box`);
    const P = points.map(([x, y]) => ({ x: box.x + x * box.width, y: box.y + y * box.height }));
    const cdp = await this.page.context().newCDPSession(this.page);
    try {
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [P[0]!] });
      for (const p of P.slice(1)) {
        await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [p] });
        await sleep(8);
      }
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    } finally {
      await cdp.detach();
    }
    // Chromium makes no click from a tap that starts within ~50 ms of a drag's end (no finger is that quick).
    await sleep(150);
  }

  async state(): Promise<State> {
    return this.page.evaluate(() => {
      const v = document.querySelector('video')!;
      const r = window.__review;
      const toast = document.querySelector('#toast')!;
      return {
        t: v.currentTime,
        paused: v.paused,
        ...r.view,
        rec: !!r.seg,
        take: r.take.id ?? null,
        strokes: r.take.strokes.filter((s) => !s.undone).length,
        toast: toast.classList.contains('show') ? (toast.textContent ?? '') : '',
      };
    });
  }

  /** Polls a page predicate every 80 ms. */
  async waitFor(fn: () => unknown, ms = 15_000): Promise<void> {
    await this.page.waitForFunction(fn, null, { timeout: ms, polling: 80 });
  }

  /** Polls a page predicate that takes one number (such as a time to pass). */
  async waitUntil(fn: (arg: number) => unknown, arg: number, ms = 15_000): Promise<void> {
    await this.page.waitForFunction(fn, arg, { timeout: ms, polling: 80 });
  }

  /** The <video> element's playback state, plus its source (a blob: URL when the page uses MSE). */
  async video(): Promise<{ t: number; paused: boolean; readyState: number; error: number | null; src: string;
    duration: number }> {
    return this.page.evaluate(() => {
      const v = document.querySelector('video')!;
      return { t: v.currentTime, paused: v.paused, readyState: v.readyState, error: v.error?.code ?? null,
        src: v.currentSrc, duration: v.duration };
    });
  }

  /** Waits until a version switch (C, [ or ]) has finished. */
  async settle(): Promise<void> {
    await this.waitFor(() => !window.__review.view.switching, 8000);
  }

  async screenshot(): Promise<Buffer> {
    return this.page.screenshot({ type: 'png' });
  }
}

/** A clock for a timed scenario: at(s) sleeps until s seconds after start(). */
export class Schedule {
  private t0 = 0;

  start(): void {
    this.t0 = Date.now();
  }

  async at(s: number): Promise<void> {
    const d = this.t0 + s * 1000 - Date.now();
    if (d > 0) await sleep(d);
  }

  /** Seconds since start(). */
  now(): number {
    return (Date.now() - this.t0) / 1000;
  }
}

export { sleep };
