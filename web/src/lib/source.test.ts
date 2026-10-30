import { describe, expect, it, vi } from 'vitest';
import { HLS_MIME, isPlaylist, planSource, VideoSource, type HlsClass, type HlsInstance } from './source';

describe('source selection', () => {
  it('plays files from /video/<label>', () => {
    for (const name of ['clip_v6.mp4', 'clip_v6.MOV', 'clip_v6.webm', 'clip_v6.mkv'])
      expect(planSource({ label: 'v6', name }, true)).toEqual({ kind: 'file', url: 'video/v6' });
  });
  it('plays the current version from /video before /api/info answers', () => {
    expect(planSource(null, false)).toEqual({ kind: 'file', url: 'video' });
  });
  it('plays .m3u8 versions from /video/<label>/<name>, natively when possible, else via hls.js', () => {
    const v = { label: 'v7', name: 'draft_v7.m3u8' };
    expect(planSource(v, true)).toEqual({ kind: 'native-hls', url: 'video/v7/draft_v7.m3u8' });
    expect(planSource(v, false)).toEqual({ kind: 'hls.js', url: 'video/v7/draft_v7.m3u8' });
    expect(isPlaylist('A.M3U8')).toBe(true);
    expect(isPlaylist('a.m3u8.mp4')).toBe(false);
  });
  it('escapes odd labels and names', () => {
    expect(planSource({ label: 'my cut', name: 'my cut #2.m3u8' }, true).url).toBe(
      'video/my%20cut/my%20cut%20%232.m3u8',
    );
  });
});

type Media = ConstructorParameters<typeof VideoSource>[0];

/** A <video> stand-in: records src changes, fires listeners on demand. */
function fakeVideo(native: boolean) {
  const listeners = new Map<string, Set<() => void>>();
  const v = {
    src: '',
    srcs: [] as string[],
    canPlayType: (t: string) => (native && t === HLS_MIME ? 'maybe' : ''),
    addEventListener: (t: string, f: () => void) => {
      if (!listeners.has(t)) listeners.set(t, new Set());
      listeners.get(t)?.add(f);
    },
    removeEventListener: (t: string, f: () => void) => listeners.get(t)?.delete(f),
    removeAttribute: vi.fn(),
    load: vi.fn(),
    fire: (t: string) => [...(listeners.get(t) ?? [])].forEach((f) => f()),
    count: (t: string) => listeners.get(t)?.size ?? 0,
  };
  const proxy = new Proxy(v, {
    set(o, k, val) {
      if (k === 'src') o.srcs.push(val as string);
      return Reflect.set(o, k, val);
    },
  });
  return Object.assign(proxy, { media: proxy as unknown as Media });
}

/** An hls.js stand-in. */
function fakeHls(supported = true) {
  const made: (HlsInstance & { url?: string; media?: unknown; destroyed: boolean; emit: (fatal: boolean) => void })[] =
    [];
  class Hls {
    static Events = { ERROR: 'hlsError' };
    static isSupported = () => supported;
    url?: string;
    media?: unknown;
    destroyed = false;
    #cb: ((e: string, d: { fatal?: boolean; type?: string; details?: string }) => void) | undefined;
    constructor() {
      made.push(this);
    }
    loadSource(u: string) {
      this.url = u;
    }
    attachMedia(m: unknown) {
      this.media = m;
    }
    on(_e: string, cb: (e: string, d: { fatal?: boolean }) => void) {
      this.#cb = cb;
    }
    destroy() {
      this.destroyed = true;
    }
    emit(fatal: boolean) {
      this.#cb?.('hlsError', { fatal, type: 'networkError', details: 'manifestLoadError' });
    }
  }
  return { Hls: Hls as unknown as HlsClass, made };
}
const tick = () => new Promise((r) => setTimeout(r, 0));

describe('VideoSource', () => {
  it('sets a file version as the src and resolves on loadedmetadata', async () => {
    const video = fakeVideo(false);
    const src = new VideoSource(video.media);
    const p = src.load({ label: 'v6', name: 'clip_v6.mp4' });
    expect(video.src).toBe('video/v6');
    video.fire('loadedmetadata');
    await expect(p).resolves.toBeUndefined();
    expect(video.count('loadedmetadata') + video.count('error')).toBe(0); // listeners cleaned up
  });

  it('rejects on a media error', async () => {
    const video = fakeVideo(false);
    const p = new VideoSource(video.media).load({ label: 'v6', name: 'clip_v6.mp4' });
    video.fire('error');
    await expect(p).rejects.toThrow(/could not load video\/v6/);
  });

  it('plays a playlist natively when the browser can', async () => {
    const video = fakeVideo(true);
    const { Hls, made } = fakeHls();
    const src = new VideoSource(video.media, { loadHls: async () => Hls });
    void src.load({ label: 'v7', name: 'v7.m3u8' });
    expect(video.src).toBe('video/v7/v7.m3u8');
    expect(src.plan?.kind).toBe('native-hls');
    expect(made.length).toBe(0);
  });

  it('plays a playlist through hls.js elsewhere, and flips back to a file cleanly', async () => {
    const video = fakeVideo(false);
    const { Hls, made } = fakeHls();
    const src = new VideoSource(video.media, { loadHls: async () => Hls });
    const p = src.load({ label: 'v7', name: 'v7.m3u8' });
    await tick();
    expect(made.length).toBe(1);
    expect(made[0].url).toBe('video/v7/v7.m3u8');
    expect(made[0].media).toBe(video.media);
    expect(video.srcs).toEqual([]); // hls.js attaches its own MediaSource
    video.fire('loadedmetadata');
    await p;
    // C back to the mp4 version: hls.js is torn down first, then the plain src is set
    const q = src.load({ label: 'v6', name: 'clip_v6.mp4' });
    expect(made[0].destroyed).toBe(true);
    expect(video.src).toBe('video/v6');
    video.fire('loadedmetadata');
    await q;
    // and ] to the playlist again: a fresh hls.js instance
    void src.load({ label: 'v7', name: 'v7.m3u8' });
    await tick();
    expect(made.length).toBe(2);
    expect(made[1].destroyed).toBe(false);
  });

  it('turns a fatal hls.js error into a rejected load and a fatal message', async () => {
    const video = fakeVideo(false);
    const { Hls, made } = fakeHls();
    const onFatal = vi.fn();
    const src = new VideoSource(video.media, { loadHls: async () => Hls, onFatal });
    const p = src.load({ label: 'v7', name: 'v7.m3u8' });
    await tick();
    made[0].emit(false); // recoverable: ignored
    made[0].emit(true);
    await expect(p).rejects.toThrow(/networkError manifestLoadError/);
    expect(onFatal).toHaveBeenCalledTimes(1);
  });

  it('lets the element try when neither native HLS nor MSE exists', async () => {
    const video = fakeVideo(false);
    const { Hls, made } = fakeHls(false);
    void new VideoSource(video.media, { loadHls: async () => Hls }).load({ label: 'v7', name: 'v7.m3u8' });
    await tick();
    expect(made.length).toBe(0);
    expect(video.src).toBe('video/v7/v7.m3u8');
  });

  it('rejects a load that another load replaced', async () => {
    const video = fakeVideo(false);
    const src = new VideoSource(video.media);
    const p = src.load({ label: 'v6', name: 'clip_v6.mp4' });
    void src.load({ label: 'v5', name: 'clip_v5.mp4' });
    await expect(p).rejects.toThrow('superseded');
    expect(video.srcs).toEqual(['video/v6', 'video/v5']);
  });
});
