// Watching a streamed version (spec/bridge.md, "Streaming"): the server renders an incomplete stream only while a
// page shows it. While this page shows one (and is visible), it says so: POST api/versions/<label>/watch?viewer=<id>
// at once and every 10 s. When it shows another version, is hidden or goes away, it says it stopped with
// navigator.sendBeacon(api/versions/<label>/unwatch?viewer=<id>), which still goes out while the page unloads.
import type { StreamState } from './types';

export const WATCH_EVERY_MS = 10_000;

export interface WatchDeps {
  /** POST a watch (default: fetch). Resolves to the answer's body, or null. */
  post?: (url: string) => Promise<unknown>;
  /** Send an unwatch that survives the page going away (default: navigator.sendBeacon). */
  beacon?: (url: string) => boolean;
  setInterval?: (fn: () => void, ms: number) => ReturnType<typeof setInterval>;
  clearInterval?: (t: ReturnType<typeof setInterval>) => void;
  /** Whether the page is visible now (default: document.visibilityState). */
  visible?: () => boolean;
  /** The viewer id (default: a random one per page). */
  viewer?: string;
  /** Each watch answer's state of the version. */
  onStream?: (label: string, s: StreamState) => void;
}

const randomId = (): string =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);

const defaultPost = async (url: string): Promise<unknown> => {
  const r = await fetch(url, { method: 'POST' });
  return r.ok ? r.json() : null;
};

const defaultBeacon = (url: string): boolean =>
  typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function'
    ? navigator.sendBeacon(url)
    : (void fetch(url, { method: 'POST', keepalive: true }).catch(() => {}), true);

export class Watcher {
  readonly viewer: string;
  /** The version the page shows that needs watching (null: none). */
  #label: string | null = null;
  /** Whether the server has been told we watch #label (and not that we stopped). */
  #on = false;
  #timer: ReturnType<typeof setInterval> | null = null;

  constructor(private readonly deps: WatchDeps = {}) {
    this.viewer = deps.viewer ?? randomId();
  }

  get label(): string | null {
    return this.#label;
  }

  #url(label: string, what: 'watch' | 'unwatch'): string {
    return `api/versions/${encodeURIComponent(label)}/${what}?viewer=${encodeURIComponent(this.viewer)}`;
  }

  #visible(): boolean {
    if (this.deps.visible) return this.deps.visible();
    return typeof document === 'undefined' || document.visibilityState !== 'hidden';
  }

  #ping(): void {
    const label = this.#label;
    if (!label) return;
    this.#on = true;
    (this.deps.post ?? defaultPost)(this.#url(label, 'watch'))
      .then((body) => {
        const b = body as { label?: string; stream?: StreamState } | null;
        if (b?.stream && label === this.#label) this.deps.onStream?.(label, b.stream);
      })
      .catch(() => {});
  }

  #start(): void {
    if (!this.#label || this.#timer || !this.#visible()) return;
    this.#ping();
    this.#timer = (this.deps.setInterval ?? setInterval)(() => this.#ping(), WATCH_EVERY_MS);
  }

  #stop(): void {
    if (this.#timer) (this.deps.clearInterval ?? clearInterval)(this.#timer);
    this.#timer = null;
    if (this.#on && this.#label) (this.deps.beacon ?? defaultBeacon)(this.#url(this.#label, 'unwatch'));
    this.#on = false;
  }

  /** The page now shows `label`, an incomplete stream (null: something else, or it's complete now). */
  watch(label: string | null): void {
    if (label === this.#label) {
      this.#start(); // no-op while on; starts again after a hidden spell
      return;
    }
    this.#stop();
    this.#label = label;
    this.#start();
  }

  /** The page became visible or hidden. */
  visibility(visible: boolean): void {
    if (visible) this.#start();
    else this.#stop();
  }

  /** The page is going away (pagehide): the server hears it at once instead of when the lease runs out. */
  leave(): void {
    this.#stop();
  }
}
