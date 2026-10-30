import { describe, expect, it, vi } from 'vitest';
import { memoryChunkStore } from './audio-buffer';
import { memoryImageStore, type ImageStore } from './image-buffer';
import { MSG_IMAGE_REFUSED } from './images';
import { Saver } from './saver';
import { newTake, serialize } from './take';
import type { Take, TakeImage } from './types';

const clock = () => ({ now: 50, videoTime: 1, duration: 40, width: 1920, height: 1080, date: new Date() });

/** A fake server: POST /image answers `imageStatus`, POST /data records the body. */
function server(imageStatus: (name: string) => number = () => 200) {
  const images = new Map<string, { type: string; size: number }>();
  const data: { images?: { file: string }[] }[] = [];
  const order: string[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const u = new URL(String(input), 'http://x');
    const res = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });
    if (u.pathname.endsWith('/data')) {
      order.push('data');
      data.push(JSON.parse(init.body as string));
      return res(200, { ok: true });
    }
    const name = u.searchParams.get('name') ?? '';
    order.push(name);
    const status = imageStatus(name);
    if (status === 200) {
      const type = new Headers(init.headers).get('Content-Type') ?? '';
      images.set(name, { type, size: (init.body as Blob).size });
    }
    return res(status, status === 200 ? { ok: true } : { error: status === 404 ? 'not found' : 'bad' });
  });
  return { fetch, images, data, order };
}

async function takeWith(buffer: ImageStore, ...specs: Partial<TakeImage>[]): Promise<Take> {
  const t = newTake('v6', 6);
  t.id = '2026-10-29_20-41-05';
  t.t0 = 10;
  for (const [i, over] of specs.entries()) {
    const file = over.file ?? `img_0${i + 1}.png`,
      blob = new Blob([new Uint8Array(3 + i)], { type: over.mime ?? 'image/png' });
    const img: TakeImage = {
      id: i + 1,
      seg: 'S1',
      version: 'v6',
      flip: false,
      file,
      mime: 'image/png',
      p0: 11,
      video_t: 2,
      box: [0.1, 0.1, 0.2, 0.2],
      url: 'blob:x',
      blob,
      upload: 'pending',
      stored: buffer.put({
        take: t.id,
        version: 'v6',
        file,
        mime: over.mime ?? 'image/png',
        data: blob,
        size: blob.size,
        path: '',
        saved: 0,
      }),
      ...over,
    };
    t.images.push(img);
  }
  return t;
}

function saver(srv: ReturnType<typeof server>, buffer: ImageStore, toast = vi.fn()) {
  return new Saver({
    serialize: (t) => serialize(t, clock()),
    toast,
    store: memoryChunkStore,
    images: () => buffer,
    fetch: srv.fetch as unknown as typeof fetch,
  });
}

describe('Saver: images', () => {
  it('stores new images with their type before the JSON that lists them, then forgets them locally', async () => {
    const buffer = memoryImageStore(),
      srv = server();
    const t = await takeWith(buffer, {}, { file: 'img_02.jpg', mime: 'image/jpeg' });
    await saver(srv, buffer).save(t);
    expect(srv.order).toEqual(['img_01.png', 'img_02.jpg', 'data']);
    expect([...srv.images]).toEqual([
      ['img_01.png', { type: 'image/png', size: 3 }],
      ['img_02.jpg', { type: 'image/jpeg', size: 4 }],
    ]);
    expect(srv.data[0].images?.map((x) => x.file)).toEqual(['img_01.png', 'img_02.jpg']);
    expect(t.images.map((x) => x.upload)).toEqual(['done', 'done']);
    expect(await buffer.list()).toEqual([]);
    srv.order.length = 0;
    await saver(srv, buffer).save(t);
    expect(srv.order).toEqual(['data']); // sent once
  });

  it('never sends an image undone before it went out', async () => {
    const buffer = memoryImageStore(),
      srv = server();
    const t = await takeWith(buffer, { undone: true }, {});
    await saver(srv, buffer).save(t);
    expect(srv.order).toEqual(['img_02.png', 'data']);
    expect(t.images[0].upload).toBe('dropped');
    expect(srv.data[0].images?.map((x) => x.file)).toEqual(['img_02.png']);
    expect(await buffer.list()).toEqual([]);
  });

  it('a server without the image route still saves the take, toasts once, and keeps the image buffered', async () => {
    const buffer = memoryImageStore(),
      srv = server(() => 404),
      toast = vi.fn();
    const t = await takeWith(buffer, {}, {});
    await saver(srv, buffer, toast).save(t);
    expect(srv.order).toEqual(['img_01.png', 'img_02.png', 'data']);
    expect(t.saveErr).toBeNull();
    expect(toast.mock.calls).toEqual([[MSG_IMAGE_REFUSED, 4000]]);
    expect((await buffer.list()).length).toBe(2);
  });

  it('a failing image upload fails the save (the JSON waits), and the retry sends it', async () => {
    const buffer = memoryImageStore();
    let down = true;
    const srv = server(() => (down ? 503 : 200));
    const t = await takeWith(buffer, {});
    const s = saver(srv, buffer);
    await s.save(t);
    expect(srv.order).toEqual(['img_01.png']);
    expect(t.saveErr).toBe('image 503');
    expect(t.images[0].upload).toBe('pending');
    down = false;
    await s.save(t);
    expect(srv.order).toEqual(['img_01.png', 'img_01.png', 'data']);
    expect(t.images[0].upload).toBe('done');
    expect(t.saveErr).toBeNull();
  });

  it('an image the server refuses for good (wrong type) is dropped with a toast; the take still saves', async () => {
    const buffer = memoryImageStore(),
      srv = server(() => 415),
      toast = vi.fn();
    const t = await takeWith(buffer, {});
    await saver(srv, buffer, toast).save(t);
    expect(t.images[0].upload).toBe('refused');
    expect(toast).toHaveBeenCalledWith('Image img_01.png refused: bad', 4000);
    expect(srv.order.at(-1)).toBe('data');
    expect(await buffer.list()).toEqual([]);
  });
});
