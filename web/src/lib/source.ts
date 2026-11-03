// Where the <video> gets a version from. A plain file (mp4, mov, webm, mkv) is `video/<label>`, as the legacy page did.
// A chunked playlist (a version whose file name ends in .m3u8) is `video/<label>/<name>`, so its relative segment
// URLs resolve beside it; it plays natively where the browser can (Safari, some Chromes), else through hls.js on MSE.
// The URLs are relative to the page, so the page works at / (retake open) and at a server's /p/<name>/.
import type { Version } from './types';

export const HLS_MIME = 'application/vnd.apple.mpegurl';

export type SourcePlan =
  { kind: 'file'; url: string } | { kind: 'native-hls'; url: string } | { kind: 'hls.js'; url: string };

export const isPlaylist = (name: string | undefined | null): boolean => /\.m3u8$/i.test(name ?? '');
type SourceVersion = Pick<Version, 'label' | 'name' | 'stream' | 'media'>;

/** The URL and the player for a version (`null` = the current version, before /api/info answered). */
export function planSource(v: SourceVersion | null, canPlayHls: boolean): SourcePlan {
  if (!v) return { kind: 'file', url: 'video' };
  const media = v.media ?? `video/${encodeURIComponent(v.label)}`;
  if (isPlaylist(v.name)) {
    const url = `${media}/${encodeURIComponent(v.name)}`;
    // Chromium advertises native HLS but fails when an EVENT playlist runs out of rendered segments.
    // Use hls.js for streams, falling back to the native player below when MSE is unavailable (e.g. older iOS).
    return canPlayHls && !v.stream ? { kind: 'native-hls', url } : { kind: 'hls.js', url };
  }
  return { kind: 'file', url: media };
}

/** What VideoSource needs of hls.js (kept narrow so tests can fake it). */
export interface HlsInstance {
  loadSource(url: string): void;
  attachMedia(media: HTMLMediaElement): void;
  on(event: string, cb: (event: string, data: { fatal?: boolean; type?: string; details?: string }) => void): void;
  destroy(): void;
}
export interface HlsClass {
  new (config?: Record<string, unknown>): HlsInstance;
  isSupported(): boolean;
  readonly Events: { ERROR: string };
}

const loadHlsJs = async (): Promise<HlsClass> => (await import('hls.js')).default as unknown as HlsClass;

/**
 * hls.js settings. A streamed render's playlist is an EVENT playlist that is still growing (spec/bridge.md,
 * "Streaming"), which hls.js plays as live: `startPosition: 0` starts it at the beginning instead of near the live
 * edge, and the defaults left alone never jump to the edge later (no max latency, no catch-up rate) and keep the
 * duration finite (the playlist's length so far; the page uses the expected one while it renders). At the end of
 * what's rendered the video waits for the next segments. Safari's own player starts at 0 through the playlist's
 * `#EXT-X-START:TIME-OFFSET=0`.
 */
export const HLS_CONFIG = { enableWorker: true, startPosition: 0 } as const;

type Media = Pick<HTMLVideoElement, 'canPlayType' | 'addEventListener' | 'removeEventListener'> & {
  src: string;
  removeAttribute(name: string): void;
  load(): void;
};

/**
 * Loads versions into one <video>. `load()` resolves on `loadedmetadata` and rejects on a media error (or a fatal
 * hls.js error). Fatal errors also go to `onFatal`, like the <video> element's own `error` event.
 */
export class VideoSource {
  #hls: HlsInstance | null = null;
  #pending: ((err: Error) => void) | null = null;
  plan: SourcePlan | null = null;

  constructor(
    private readonly video: Media,
    private readonly opts: { loadHls?: () => Promise<HlsClass>; onFatal?: (msg: string) => void } = {},
  ) {}

  /** Whether the browser plays HLS playlists itself. */
  canPlayNativeHls(): boolean {
    return this.video.canPlayType(HLS_MIME) !== '';
  }

  /** Tear down hls.js (it detaches its MediaSource and empties the element). */
  #dropHls(): void {
    if (!this.#hls) return;
    const h = this.#hls;
    this.#hls = null;
    h.destroy();
  }

  load(v: SourceVersion | null): Promise<void> {
    this.#pending?.(new Error('superseded'));
    const plan = planSource(v, this.canPlayNativeHls());
    this.#dropHls();
    this.plan = plan;
    const video = this.video;
    return new Promise<void>((res, rej) => {
      const settle = (err?: Error) => {
        video.removeEventListener('loadedmetadata', ok);
        video.removeEventListener('error', bad);
        if (this.#pending === fail) this.#pending = null;
        if (err) rej(err);
        else res();
      };
      const ok = () => settle();
      const bad = () => settle(new Error(`could not load ${plan.url}`));
      const fail = (err: Error) => settle(err);
      this.#pending = fail;
      video.addEventListener('loadedmetadata', ok);
      video.addEventListener('error', bad);
      if (plan.kind !== 'hls.js') {
        video.src = plan.url;
        return;
      }
      (this.opts.loadHls ?? loadHlsJs)()
        .then((Hls) => {
          if (this.plan !== plan) return; // another load started meanwhile
          if (!Hls.isSupported()) {
            video.src = plan.url; // no MSE either: let the element try (and report the error)
            return;
          }
          const hls = new Hls({ ...HLS_CONFIG });
          this.#hls = hls;
          hls.on(Hls.Events.ERROR, (_e, data) => {
            if (!data.fatal || this.#hls !== hls) return;
            const msg = `hls: ${data.type ?? 'error'} ${data.details ?? ''}`.trim();
            if (this.#pending === fail) fail(new Error(msg));
            this.opts.onFatal?.(msg);
          });
          hls.loadSource(plan.url);
          hls.attachMedia(video as unknown as HTMLMediaElement);
        })
        .catch((err: unknown) => {
          const e = err instanceof Error ? err : new Error(String(err));
          if (this.#pending === fail) fail(e);
          this.opts.onFatal?.(e.message);
        });
    });
  }

  /** Shows no version at all, only the element's poster: a streamed version with nothing rendered yet. A pending
   * load is cancelled (it rejects as superseded). */
  clear(): void {
    this.#pending?.(new Error('superseded'));
    this.#dropHls();
    this.plan = null;
    this.video.removeAttribute('src');
    this.video.load();
  }

  destroy(): void {
    this.#pending?.(new Error('destroyed'));
    this.#dropHls();
  }
}
