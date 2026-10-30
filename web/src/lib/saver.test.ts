import { describe, expect, it, vi } from 'vitest';
import { memoryChunkStore, type ChunkStore } from './audio-buffer';
import { MSG_SAVE_FAILED } from './constants';
import { dataUrl, Saver } from './saver';
import { addChunk, binCount, newSegment, newTake, serialize } from './take';
import type { Take } from './types';

const rec = {} as MediaRecorder;
const clock = () => ({ now: 50, videoTime: 1, duration: 40, width: 1920, height: 1080, date: new Date() });
const text = async (b: unknown) => new TextDecoder().decode(await (b as Blob).arrayBuffer());

/** A fake review server: POST /audio with the offset rules, POST /data. */
function server() {
  const files = new Map<string, string>();
  const data: unknown[] = [];
  let down = false;
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input);
    if (down) throw new TypeError('Failed to fetch');
    const u = new URL(url, 'http://x');
    const json = (status: number, body: unknown) =>
      ({ status, ok: status < 300, json: async () => body }) as unknown as Response;
    if (u.pathname.endsWith('/data')) {
      data.push(JSON.parse(init.body as string));
      return json(200, { ok: true });
    }
    const name = u.searchParams.get('name') ?? '',
      offset = Number(u.searchParams.get('offset'));
    const cur = files.get(name) ?? '',
      body = await text(init.body);
    if (offset > cur.length) return json(409, { size: cur.length, error: 'offset past end' });
    files.set(name, cur.slice(0, offset) + body);
    return json(200, { size: offset + body.length });
  });
  return {
    files,
    data,
    fetch,
    /** The URLs requested so far. */
    urls: () => fetch.mock.calls.map(([u]) => String(u)),
    setDown: (d: boolean) => (down = d),
  };
}

function take(store: ChunkStore): Take {
  const t = newTake('v6', binCount(40));
  t.id = '2026-10-29_20-41-05';
  t.t0 = 40;
  const s = newSegment(t, rec, 'audio/webm', { version: 'v6', flip: false, videoTime: 0 });
  s.p0 = 41;
  t.segments.push(s);
  const put = (offset: number, d: string) =>
    store.put({
      take: t.id ?? '',
      version: 'v6',
      file: s.file,
      offset,
      data: new Blob([d]),
      size: d.length,
      mime: '',
      path: '',
      saved: 0,
    });
  addChunk(s, new Blob(['abc']), (o) => put(o, 'abc'));
  addChunk(s, new Blob(['de']), (o) => put(o, 'de'));
  return t;
}

describe('Saver', () => {
  it('uploads buffered audio by offset, confirms it in the buffer, then posts the JSON', async () => {
    const store = memoryChunkStore();
    const srv = server();
    const toast = vi.fn();
    const saver = new Saver({ serialize: (t) => serialize(t, clock()), toast, store: () => store, fetch: srv.fetch });
    const t = take(store);
    t.dirty = true;
    await saver.save(t);
    expect(srv.files.get('seg_01.webm')).toBe('abcde');
    expect(t.segments[0].uploaded).toBe(5);
    expect(await store.list()).toEqual([]);
    expect(srv.data.length).toBe(1);
    expect(srv.urls().at(-1)).toBe(dataUrl('2026-10-29_20-41-05', 'v6'));
    expect(t.dirty).toBe(false);
    expect(toast).not.toHaveBeenCalled();
  });

  it('sends only the new bytes next time', async () => {
    const store = memoryChunkStore();
    const srv = server();
    const saver = new Saver({
      serialize: (t) => serialize(t, clock()),
      toast: vi.fn(),
      store: () => store,
      fetch: srv.fetch,
    });
    const t = take(store);
    await saver.save(t);
    addChunk(t.segments[0], new Blob(['fg']), (o) =>
      store.put({
        take: t.id ?? '',
        version: 'v6',
        file: 'seg_01.webm',
        offset: o,
        data: new Blob(['fg']),
        size: 2,
        mime: '',
        path: '',
        saved: 0,
      }),
    );
    srv.fetch.mockClear();
    await saver.save(t);
    const audio = srv.urls().filter((u) => u.includes('/audio'));
    expect(audio.map((u) => new URL(u, 'http://x').searchParams.get('offset'))).toEqual(['5']);
    expect(srv.files.get('seg_01.webm')).toBe('abcdefg');
  });

  it('resumes from the server size after a 409 (the server lost the tail)', async () => {
    const store = memoryChunkStore();
    const srv = server();
    const saver = new Saver({
      serialize: (t) => serialize(t, clock()),
      toast: vi.fn(),
      store: () => store,
      fetch: srv.fetch,
    });
    const t = take(store);
    t.segments[0].uploaded = 3; // the page thinks 3 bytes are there, the server has none
    await saver.save(t);
    expect(srv.files.get('seg_01.webm')).toBe('abcde');
  });

  it('toasts once when the server is gone, keeps the audio, and says so when saving works again', async () => {
    const store = memoryChunkStore();
    const srv = server();
    const toast = vi.fn();
    const saver = new Saver({ serialize: (t) => serialize(t, clock()), toast, store: () => store, fetch: srv.fetch });
    const t = take(store);
    t.dirty = true;
    srv.setDown(true);
    await saver.save(t);
    await saver.save(t);
    expect(toast.mock.calls).toEqual([[MSG_SAVE_FAILED, 6000]]);
    expect(t.saveErr).toBe('Failed to fetch');
    expect(t.dirty).toBe(true);
    expect((await store.list()).length).toBe(2);
    srv.setDown(false);
    await saver.save(t);
    expect(toast).toHaveBeenLastCalledWith('Saving again');
    expect(t.saveErr).toBeNull();
    expect(await store.list()).toEqual([]);
  });

  it('coalesces saves requested while one runs into one more', async () => {
    const store = memoryChunkStore();
    const srv = server();
    const saver = new Saver({
      serialize: (t) => serialize(t, clock()),
      toast: vi.fn(),
      store: () => store,
      fetch: srv.fetch,
    });
    const t = take(store);
    const a = saver.save(t);
    expect(saver.save(t)).toBe(a);
    expect(saver.save(t)).toBe(a);
    await a;
    await vi.waitFor(() => expect(t.saving).toBeNull());
    expect(srv.data.length).toBe(2);
  });

  it('does nothing for a take that has not started', () => {
    const saver = new Saver({ serialize: (t) => serialize(t, clock()), toast: vi.fn(), store: memoryChunkStore });
    expect(saver.save(newTake('v6', 6))).toBeUndefined();
    expect(saver.save(null)).toBeUndefined();
  });

  it('flushes the unsent tail and the JSON with keepalive on page exit', async () => {
    const store = memoryChunkStore();
    const srv = server();
    const saver = new Saver({
      serialize: (t) => serialize(t, clock()),
      toast: vi.fn(),
      store: () => store,
      fetch: srv.fetch,
    });
    const t = take(store);
    saver.flush(t);
    expect(srv.fetch.mock.calls.map(([u, i]) => [String(u).split('?')[0].split('/').pop(), i?.keepalive])).toEqual([
      ['audio', true],
      ['data', true],
    ]);
    t.closed = true;
    srv.fetch.mockClear();
    saver.flush(t);
    expect(srv.fetch).not.toHaveBeenCalled();
  });
});
