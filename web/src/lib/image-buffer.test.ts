import 'fake-indexeddb/auto';
import { IDBFactory as FakeIDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  imageUrl,
  memoryImageStore,
  openImageStore,
  recoverImages,
  type ImageRecord,
  type PostImage,
} from './image-buffer';

const rec = (file: string, over: Partial<ImageRecord> = {}): ImageRecord => ({
  take: '2026-10-29_20-41-05',
  version: 'v6',
  file,
  mime: 'image/png',
  data: new Blob([new Uint8Array([137, 80, 78, 71])], { type: 'image/png' }),
  size: 4,
  path: '/videos/v6/clip_v6.mp4',
  saved: 0,
  ...over,
});
const family = [{ label: 'v6', path: '/videos/v6/clip_v6.mp4' }];

afterEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
});

describe('image buffer', () => {
  it('keeps images across reopening (a page reload) until confirmed', async () => {
    const idb = new FakeIDBFactory();
    const a = await openImageStore(idb);
    expect(a.kind).toBe('indexeddb');
    await a.put(rec('img_02.jpg', { mime: 'image/jpeg' }));
    await a.put(rec('img_01.png'));
    const b = await openImageStore(idb);
    const list = await b.list();
    expect(list.map((r) => r.file)).toEqual(['img_01.png', 'img_02.jpg']);
    expect(new Uint8Array(await list[0].data.arrayBuffer())).toEqual(new Uint8Array([137, 80, 78, 71]));
    await b.confirm('2026-10-29_20-41-05', 'v6', 'img_01.png');
    expect((await (await openImageStore(idb)).list()).map((r) => r.file)).toEqual(['img_02.jpg']);
  });

  it('falls back to memory when IndexedDB cannot open', async () => {
    const broken = {
      open() {
        throw new Error('denied');
      },
    } as unknown as IDBFactory;
    const s = await openImageStore(broken);
    expect(s.kind).toBe('memory');
    await s.put(rec('img_01.png'));
    expect((await s.list()).length).toBe(1);
    await s.confirm('2026-10-29_20-41-05', 'v6', 'img_01.png');
    expect(await s.list()).toEqual([]);
  });

  it('builds the image route', () => {
    expect(imageUrl('2026-10-29_20-41-05', 'v6', 'img_01.png')).toBe(
      '/api/session/2026-10-29_20-41-05/image?v=v6&name=img_01.png',
    );
  });
});

describe('recoverImages', () => {
  const poster = (status: (url: string) => number | Error) => {
    const sent: { url: string; mime: string; size: number }[] = [];
    const post: PostImage = async (url, body, mime) => {
      const s = status(url);
      if (s instanceof Error) throw s;
      sent.push({ url, mime, size: body.size });
      return s;
    };
    return { post, sent };
  };

  it('resends what a crashed page left, with its type, and forgets it once stored', async () => {
    const store = memoryImageStore();
    await store.put(rec('img_01.png'));
    await store.put(rec('img_02.webp', { mime: 'image/webp' }));
    const { post, sent } = poster(() => 200);
    const res = await recoverImages(store, family, post);
    expect(res).toEqual({ recovered: 2, dropped: 0, kept: 0, retry: 0 });
    expect(sent.map((s) => [s.url.split('name=')[1], s.mime, s.size])).toEqual([
      ['img_01.png', 'image/png', 4],
      ['img_02.webp', 'image/webp', 4],
    ]);
    expect(await store.list()).toEqual([]);
  });

  it("leaves this page's takes and other videos alone", async () => {
    const store = memoryImageStore();
    await store.put(rec('img_01.png', { take: '2026-10-30_09-00-00' }));
    await store.put(rec('img_01.png', { path: '/elsewhere/v6.mp4' }));
    await store.put(rec('img_01.png', { take: '2026-10-29_21-00-00', version: 'v9' }));
    const { post, sent } = poster(() => 200);
    const res = await recoverImages(store, family, post, (take) => take === '2026-10-30_09-00-00');
    expect(sent).toEqual([]);
    expect(res).toEqual({ recovered: 0, dropped: 0, kept: 2, retry: 0 });
    expect((await store.list()).length).toBe(3);
  });

  it('keeps images an older server has no route for, retries when unreachable, drops refused ones', async () => {
    const store = memoryImageStore();
    await store.put(rec('img_01.png'));
    await store.put(rec('img_02.png'));
    await store.put(rec('img_03.png'));
    await store.put(rec('img_04.png'));
    const { post } = poster((url) =>
      url.includes('img_01')
        ? 404
        : url.includes('img_02')
          ? new TypeError('Failed to fetch')
          : url.includes('img_03')
            ? 415
            : 503,
    );
    const res = await recoverImages(store, family, post);
    expect(res).toEqual({ recovered: 0, dropped: 1, kept: 3, retry: 2 });
    expect((await store.list()).map((r) => r.file)).toEqual(['img_01.png', 'img_02.png', 'img_04.png']);
  });
});
