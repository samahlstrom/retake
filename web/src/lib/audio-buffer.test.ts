import 'fake-indexeddb/auto';
import { IDBDatabase as FakeIDBDatabase, IDBFactory as FakeIDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  audioUrl,
  groupPending,
  memoryChunkStore,
  openChunkStore,
  PROBE_OFFSET,
  recoverPending,
  runs,
  type ChunkRecord,
  type Post,
} from './audio-buffer';

const bytes = (s: string) => new Blob([new TextEncoder().encode(s)]);
const chunk = (offset: number, text: string, over: Partial<ChunkRecord> = {}): ChunkRecord => ({
  take: '2026-10-29_20-41-05',
  version: 'v6',
  file: 'seg_01.webm',
  offset,
  data: bytes(text),
  size: text.length,
  mime: 'audio/webm',
  path: '/videos/v6/clip_v6.mp4',
  saved: 0,
  ...over,
});
const text = async (b: Blob) => new TextDecoder().decode(await b.arrayBuffer());

afterEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
});

describe('IndexedDB chunk store', () => {
  it('keeps chunks across reopening (a page reload)', async () => {
    const idb = new FakeIDBFactory();
    const a = await openChunkStore(idb);
    expect(a.kind).toBe('indexeddb');
    await a.put(chunk(0, 'abc'));
    await a.put(chunk(3, 'de'));
    await a.put(chunk(0, 'zz', { file: 'seg_02.webm' }));
    const b = await openChunkStore(idb);
    const list = await b.list();
    expect(list.map((c) => [c.file, c.offset, c.size])).toEqual([
      ['seg_01.webm', 0, 3],
      ['seg_01.webm', 3, 2],
      ['seg_02.webm', 0, 2],
    ]);
    expect(await text(list[1].data)).toBe('de');
  });

  it('deletes only the chunks the server has confirmed whole', async () => {
    const s = await openChunkStore(new FakeIDBFactory());
    await s.put(chunk(0, 'abc'));
    await s.put(chunk(3, 'de'));
    await s.put(chunk(5, 'fgh'));
    await s.put(chunk(0, 'x', { take: '2026-10-29_20-50-00' }));
    await s.confirm('2026-10-29_20-41-05', 'v6', 'seg_01.webm', 4); // the server has 'abcd': 'de' is half there
    expect((await s.list()).map((c) => [c.take.slice(-2), c.offset])).toEqual([
      ['05', 3],
      ['05', 5],
      ['00', 0],
    ]);
    await s.confirm('2026-10-29_20-41-05', 'v6', 'seg_01.webm', 8);
    expect((await s.list()).map((c) => c.take.slice(-2))).toEqual(['00']);
  });

  it('falls back to memory when IndexedDB cannot open (private mode)', async () => {
    const broken = {
      open() {
        throw new Error('The user denied permission to access the database.');
      },
    } as unknown as IDBFactory;
    const s = await openChunkStore(broken);
    expect(s.kind).toBe('memory');
    await s.put(chunk(0, 'abc'));
    await s.put(chunk(3, 'de'));
    expect((await s.list()).length).toBe(2);
    await s.confirm('2026-10-29_20-41-05', 'v6', 'seg_01.webm', 5);
    expect(await s.list()).toEqual([]);
  });

  it('falls back to memory when there is no IndexedDB at all', async () => {
    const s = await openChunkStore(null as unknown as IDBFactory);
    expect(s.kind).toBe('memory');
  });

  it('keeps recording when IndexedDB fails mid-take (quota, a broken profile)', async () => {
    const s = await openChunkStore(new FakeIDBFactory());
    await s.put(chunk(0, 'abc'));
    vi.spyOn(FakeIDBDatabase.prototype, 'transaction').mockImplementation(() => {
      throw new DOMException('QuotaExceededError', 'QuotaExceededError');
    });
    await expect(s.put(chunk(3, 'de'))).resolves.toBeUndefined();
    expect(s.kind).toBe('memory');
    const list = await s.list();
    expect(list.map((c) => c.offset)).toEqual([3]); // the IndexedDB copy is unreachable now; memory has the rest
    await s.confirm('2026-10-29_20-41-05', 'v6', 'seg_01.webm', 5);
    expect(await s.list()).toEqual([]);
  });
});

describe('pending runs', () => {
  it('groups by file and joins contiguous chunks; a hole starts a new run', async () => {
    const files = groupPending([
      chunk(5, 'fg'),
      chunk(0, 'abc'),
      chunk(3, 'de'),
      chunk(10, 'k'),
      chunk(0, 'Z', { file: 'seg_02.webm' }),
    ]);
    expect(files.map((f) => f.file)).toEqual(['seg_01.webm', 'seg_02.webm']);
    const r = runs(files[0]);
    expect(r.map((x) => [x.start, x.end])).toEqual([
      [0, 7],
      [10, 11],
    ]);
    expect(await text(r[0].blob)).toBe('abcdefg');
  });

  it('skips bytes it already has when chunks overlap', async () => {
    const [f] = groupPending([chunk(0, 'abcd'), chunk(2, 'cdef')]);
    const [r] = runs(f);
    expect([r.start, r.end, await text(r.blob)]).toEqual([0, 6, 'abcdef']);
  });
});

/** A fake review server for one or more audio files: the POST /audio rules of spec/api.md. */
function fakeServer(initial: Record<string, string> = {}) {
  const files = new Map(Object.entries(initial));
  const calls: { file: string; offset: number; body: string }[] = [];
  const post: Post = async (url, body) => {
    const u = new URL(url, 'http://x');
    if (u.searchParams.get('v') === 'gone') return { status: 400, json: { error: 'bad version' } };
    const file = `${u.pathname.split('/')[3]}/${u.searchParams.get('name')}`;
    const offset = Number(u.searchParams.get('offset'));
    const b = await text(body);
    calls.push({ file, offset, body: b });
    const cur = files.get(file) ?? '';
    if (offset < 0 || offset > cur.length) return { status: 409, json: { size: cur.length, error: 'offset past end' } };
    files.set(file, cur.slice(0, offset) + b);
    return { status: 200, json: { size: offset + b.length } };
  };
  return { files, calls, post };
}
const fam = [{ label: 'v6', path: '/videos/v6/clip_v6.mp4' }];
const key = '2026-10-29_20-41-05/seg_01.webm';

describe('recovery on page load', () => {
  it('asks the server for its size (409), then resends only the missing tail', async () => {
    const store = memoryChunkStore();
    for (const c of [chunk(0, 'abc'), chunk(3, 'de'), chunk(5, 'fgh')]) await store.put(c);
    const srv = fakeServer({ [key]: 'abcd' });
    const res = await recoverPending(store, fam, srv.post);
    expect(srv.calls[0]).toEqual({ file: key, offset: PROBE_OFFSET, body: '' });
    expect(srv.calls[1]).toEqual({ file: key, offset: 4, body: 'efgh' });
    expect(srv.files.get(key)).toBe('abcdefgh');
    expect(res).toEqual({ recovered: 1, sent: 4, kept: 0, retry: 0 });
    expect(await store.list()).toEqual([]);
  });

  it('only confirms what the server already has (no upload when nothing is missing)', async () => {
    const store = memoryChunkStore();
    await store.put(chunk(0, 'abc'));
    const srv = fakeServer({ [key]: 'abc' });
    expect(await recoverPending(store, fam, srv.post)).toEqual({ recovered: 1, sent: 0, kept: 0, retry: 0 });
    expect(srv.calls.length).toBe(1);
    expect(await store.list()).toEqual([]);
  });

  it('starts a file the server never got', async () => {
    const store = memoryChunkStore();
    await store.put(chunk(0, 'abc'));
    const srv = fakeServer();
    await recoverPending(store, fam, srv.post);
    expect(srv.files.get(key)).toBe('abc');
  });

  it('never sends chunks to another video that reuses the port, or to a version that is gone', async () => {
    const store = memoryChunkStore();
    await store.put(chunk(0, 'abc', { path: '/videos/other/clip_v6.mp4' }));
    await store.put(chunk(0, 'abc', { version: 'v9' }));
    const srv = fakeServer();
    expect(await recoverPending(store, fam, srv.post)).toEqual({ recovered: 0, sent: 0, kept: 2, retry: 0 });
    expect(srv.calls).toEqual([]);
    expect((await store.list()).length).toBe(2);
  });

  it('keeps what lies past a hole the server cannot fill', async () => {
    const store = memoryChunkStore();
    await store.put(chunk(5, 'fgh'));
    const srv = fakeServer({ [key]: 'ab' });
    expect(await recoverPending(store, fam, srv.post)).toEqual({ recovered: 0, sent: 0, kept: 1, retry: 0 });
    expect(srv.files.get(key)).toBe('ab');
    expect((await store.list()).length).toBe(1);
  });

  it('keeps everything when the server is unreachable', async () => {
    const store = memoryChunkStore();
    await store.put(chunk(0, 'abc'));
    const down: Post = () => Promise.reject(new TypeError('Failed to fetch'));
    expect(await recoverPending(store, fam, down)).toEqual({ recovered: 0, sent: 0, kept: 1, retry: 1 });
    expect((await store.list()).length).toBe(1);
  });

  it('leaves takes another writer owns alone (this page saves its own)', async () => {
    const store = memoryChunkStore();
    await store.put(chunk(0, 'abc'));
    const srv = fakeServer();
    expect(await recoverPending(store, fam, srv.post, (take) => take === '2026-10-29_20-41-05')).toEqual({
      recovered: 0,
      sent: 0,
      kept: 0,
      retry: 0,
    });
    expect(srv.calls).toEqual([]);
  });

  it('retries a server that errors, until it has the whole file (uploads failed, then the page reloaded)', async () => {
    const store = await openChunkStore(new FakeIDBFactory());
    for (const c of [chunk(0, 'abc'), chunk(3, 'de'), chunk(5, 'fgh')]) await store.put(c);
    const srv = fakeServer({ [key]: 'abc' }); // the first chunk made it before uploads started failing
    let failing = true;
    const flaky: Post = (url, body) =>
      failing ? Promise.resolve({ status: 502, json: { error: 'bad gateway' } }) : srv.post(url, body);
    expect(await recoverPending(store, fam, flaky)).toEqual({ recovered: 0, sent: 0, kept: 1, retry: 1 });
    expect((await store.list()).length).toBe(3);
    failing = false;
    expect(await recoverPending(store, fam, flaky)).toEqual({ recovered: 1, sent: 5, kept: 0, retry: 0 });
    expect(srv.files.get(key)).toBe('abcdefgh');
    expect(await store.list()).toEqual([]);
  });

  it('builds the audio URL of spec/api.md', () => {
    expect(audioUrl('2026-10-29_20-41-05', 'v6', 'seg_01.webm', 42)).toBe(
      '/api/session/2026-10-29_20-41-05/audio?v=v6&name=seg_01.webm&offset=42',
    );
  });
});
