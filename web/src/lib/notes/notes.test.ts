import { describe, expect, it, vi } from 'vitest';
import {
  doneUrl,
  EVENTS_RETRY_MS,
  EVENTS_TRIES,
  eventsUrl,
  normalize,
  Notes,
  notesUrl,
  reopenUrl,
  SIDEBAR_KEY,
  type EventSourceLike,
} from './notes.svelte';
import type { NotesResponse } from './types';

const json = (status: number, body: unknown) =>
  new Response(typeof body === 'string' ? body : JSON.stringify(body), { status });

/** A fake notes server: /api/notes per version, POST reopen and done. */
function server(data: Record<string, NotesResponse>, opts: { missing?: boolean } = {}) {
  const calls: string[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input);
    calls.push(`${init.method ?? 'GET'} ${url}`);
    if (opts.missing) return json(404, { error: 'not found' });
    const u = new URL(url, 'http://x');
    if (u.pathname === '/api/notes') {
      const d = data[u.searchParams.get('v') ?? ''];
      return d ? json(200, d) : json(400, { error: 'bad version' });
    }
    if (/^\/api\/notes\/\d+\/reopen$/.test(u.pathname)) return json(200, { ok: true });
    if (u.pathname.endsWith('/done')) return json(200, { ok: true });
    return json(404, { error: 'not found' });
  });
  return { fetch: fetch as unknown as typeof globalThis.fetch, calls };
}

/** A fake EventSource the test drives. */
class FakeES implements EventSourceLike {
  static all: FakeES[] = [];
  readyState = 0;
  onerror: ((e: Event) => void) | null = null;
  closed = false;
  readonly listeners = new Map<string, ((e: MessageEvent) => void)[]>();
  constructor(readonly url: string) {
    FakeES.all.push(this);
  }
  addEventListener(type: string, cb: (e: MessageEvent) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), cb]);
  }
  close(): void {
    this.closed = true;
    this.readyState = 2;
  }
  emit(type: string, data?: unknown, id = ''): void {
    const e = { data: data === undefined ? '' : JSON.stringify(data), lastEventId: id } as MessageEvent;
    for (const cb of this.listeners.get(type) ?? []) cb(e);
  }
  fail(closed: boolean): void {
    this.readyState = closed ? 2 : 0;
    this.onerror?.(new Event('error'));
  }
}

const v8: NotesResponse = {
  version: 'v8',
  newest: true,
  items: [{ id: 21, kind: 'question', status: 'question', t0: 4, text: 'Keep the zoom?' }],
};
const v7: NotesResponse = {
  version: 'v7',
  newest: false,
  items: [{ id: 3, kind: 'note', status: 'answered', t0: 1, text: 'Late title', answered_in: 'v8' }],
};
const tick = () => new Promise((r) => setTimeout(r, 0));

function setup(opts: { missing?: boolean } = {}) {
  FakeES.all = [];
  const srv = server({ v8, v7 }, opts);
  const timers: (() => void)[] = [];
  const store = new Map<string, string>();
  const onVersion = vi.fn();
  const notes = new Notes({
    fetch: srv.fetch,
    eventSource: (u) => new FakeES(u),
    storage: () => ({ getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v) }),
    onVersion,
    setTimeout: (fn) => timers.push(fn),
  });
  return { notes, srv, timers, store, onVersion };
}

describe('notes routes', () => {
  it('builds the spec URLs', () => {
    expect(notesUrl('v8')).toBe('api/notes?v=v8');
    expect(reopenUrl(12)).toBe('api/notes/12/reopen');
    expect(doneUrl('2026-10-29_20-41-05', 'v8')).toBe('api/session/2026-10-29_20-41-05/done?v=v8');
    expect(eventsUrl(null)).toBe('api/events');
    expect(eventsUrl(42)).toBe('api/events?since=42');
  });

  it('normalize() keeps only items it can place on the timeline', () => {
    const items = [
      { id: 1, kind: 'note', status: 'open', t0: 2, text: 'ok' },
      { id: 'x', kind: 'note', status: 'open', t0: 2, text: 'bad id' },
      { id: 2, kind: 'note', status: 'open', t0: null, text: 'no time' },
      null,
    ] as unknown as NotesResponse['items'];
    expect(normalize({ items }, 'v8')).toEqual({ version: 'v8', newest: false, items: [items[0]] });
    expect(normalize(null, 'v7')).toEqual({ version: 'v7', newest: false, items: [] });
  });
});

describe('Notes', () => {
  it('stays unavailable (no sidebar, no event stream) on a server without the notes routes', async () => {
    const { notes } = setup({ missing: true });
    await notes.load('v8');
    expect(notes.available).toBe(false);
    expect(notes.res).toBeNull();
    expect(FakeES.all).toHaveLength(0);
  });

  it('loads the version on screen, connects to the event stream once, and keeps it on a flip', async () => {
    const { notes } = setup();
    await notes.load('v8');
    expect(notes.available).toBe(true);
    expect(notes.res?.version).toBe('v8');
    expect(FakeES.all.map((e) => e.url)).toEqual(['api/events']);
    await notes.load('v7');
    expect(notes.res).toMatchObject({ version: 'v7', newest: false });
    expect(FakeES.all).toHaveLength(1);
    expect(notes.snapshot).toMatchObject({ available: true, version: 'v7', newest: false, items: v7.items });
  });

  it('drops an answer that arrives after a newer request (fast C flips)', async () => {
    const { notes } = setup();
    const a = notes.load('v8'),
      b = notes.load('v7');
    await Promise.all([a, b]);
    expect(notes.res?.version).toBe('v7');
  });

  it('refetches on a notes event, tracks jobs, and reloads the family on a version event', async () => {
    const { notes, srv, onVersion } = setup();
    await notes.load('v8');
    const es = FakeES.all[0];
    const before = srv.calls.length;
    es.emit('notes', {}, '7');
    await tick();
    expect(srv.calls.slice(before)).toEqual(['GET api/notes?v=v8']);
    es.emit('job', { id: 1, kind: 'transcribe', state: 'running' }, '8');
    expect(notes.job).toEqual({ text: 'Transcribing your take…', error: false, hint: null });
    es.emit('job', {
      id: 1,
      kind: 'transcribe',
      state: 'failed',
      error: "transcription isn't set up",
      hint: 'Set it up',
    });
    expect(notes.job).toMatchObject({ error: true, text: "transcription isn't set up", hint: 'Set it up' });
    notes.dismissJob();
    expect(notes.job).toBeNull();
    es.emit('job', 'not json');
    expect(notes.jobs).toEqual([]);
    es.emit('version', {}, '9');
    expect(onVersion).toHaveBeenCalledOnce();
  });

  it('reconnects from the last event after the stream closes, and catches up when it reopens', async () => {
    const { notes, srv, timers } = setup();
    await notes.load('v8');
    const es = FakeES.all[0];
    es.emit('open');
    es.emit('notes', {}, '41');
    es.fail(false); // the browser retries by itself
    expect(timers).toHaveLength(0);
    es.fail(true);
    expect(es.closed).toBe(true);
    expect(timers).toHaveLength(1);
    timers[0]();
    expect(FakeES.all[1].url).toBe('api/events?since=41');
    const before = srv.calls.length;
    FakeES.all[1].emit('open');
    await tick();
    expect(srv.calls.slice(before)).toEqual(['GET api/notes?v=v8']);
  });

  it(`gives up on the event stream after ${EVENTS_TRIES} closes in a row that never opened`, async () => {
    const { notes, timers } = setup();
    await notes.load('v8');
    for (let i = 0; i < EVENTS_TRIES; i++) {
      FakeES.all[i].fail(true);
      if (i < EVENTS_TRIES - 1) timers[i]();
    }
    expect(timers).toHaveLength(EVENTS_TRIES - 1);
    expect(EVENTS_RETRY_MS).toBeGreaterThan(1000);
  });

  it('remembers the desktop open state; the phone list is its own toggle', () => {
    const { notes, store } = setup();
    notes.restore();
    expect(notes.open).toBe(true);
    notes.toggle();
    expect(notes.open).toBe(false);
    expect(store.get(SIDEBAR_KEY)).toBe('closed');
    notes.toggle(true);
    expect(notes).toMatchObject({ open: false, phoneOpen: true });
    const again = new Notes({ storage: () => ({ getItem: (k) => store.get(k) ?? null, setItem: () => {} }) });
    again.restore();
    expect(again.open).toBe(false);
  });

  it('reopens a note and refetches; posts Done for a take', async () => {
    const { notes, srv } = setup();
    await notes.load('v7');
    expect(await notes.reopen(3)).toBe(true);
    await tick();
    expect(srv.calls).toContain('POST api/notes/3/reopen');
    expect(srv.calls.at(-1)).toBe('GET api/notes?v=v7');
    expect(await notes.postDone('2026-10-29_20-41-05', 'v8')).toBe(200);
    expect(srv.calls).toContain('POST api/session/2026-10-29_20-41-05/done?v=v8');
  });

  it('postDone and reopen survive an unreachable server', async () => {
    const notes = new Notes({ fetch: (() => Promise.reject(new TypeError('Failed to fetch'))) as typeof fetch });
    expect(await notes.postDone('2026-10-29_20-41-05', 'v8')).toBe(0);
    expect(await notes.reopen(1)).toBe(false);
    await notes.load('v8');
    expect(notes.available).toBe(false);
  });
});
