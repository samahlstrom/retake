// The home screen's state (spec/home.md): GET api/home on load, every REFRESH_MS while the page is visible, and when
// it comes back into view (a tab switched back, or Back from a review).
import { SvelteSet } from 'svelte/reactivity';
import { normalizeHome, type Card, type HomeResponse } from '../lib/home';

export const REFRESH_MS = 15_000;

/** The test hook (`window.__home`). */
export interface HomeHook {
  readonly loaded: boolean;
  readonly projects: Card[];
  readonly error: string;
}

export interface HomeDeps {
  fetch?: typeof fetch;
}

export class Home {
  /** The first answer came. */
  loaded = $state(false);
  res = $state.raw<HomeResponse>({ title: '', role: '', projects: [] });
  /** Why the last read failed, or "". */
  error = $state('');
  /** Thumbnails that failed to load: their card shows the empty frame. */
  readonly broken = new SvelteSet<string>();

  readonly #fetch: typeof fetch;
  #timer: ReturnType<typeof setInterval> | null = null;

  constructor(deps: HomeDeps = {}) {
    this.#fetch = deps.fetch ?? ((...a) => fetch(...a));
  }

  readonly hook: HomeHook = (() => {
    // eslint-disable-next-line @typescript-eslint/no-this-alias
    const h = this;
    return {
      get loaded() {
        return h.loaded;
      },
      get projects() {
        return h.res.projects;
      },
      get error() {
        return h.error;
      },
    };
  })();

  refresh = async (): Promise<void> => {
    try {
      const r = await this.#fetch('api/home', { cache: 'no-store' });
      if (!r.ok) {
        this.error = r.status === 401 || r.status === 403 ? 'This link can’t open these videos.' : 'Not reachable';
        return;
      }
      this.res = normalizeHome(await r.json());
      this.error = '';
    } catch {
      this.error = 'Review server not reachable';
    } finally {
      this.loaded = true;
    }
  };

  thumbFailed = (name: string): void => {
    this.broken.add(name);
  };

  /** Load now, then keep the cards fresh while the page is on screen. */
  start(): void {
    void this.refresh();
    const tick = () => {
      if (document.visibilityState === 'visible') void this.refresh();
    };
    this.#timer = setInterval(tick, REFRESH_MS);
    document.addEventListener('visibilitychange', tick);
    addEventListener('pageshow', (e) => {
      if (e.persisted) void this.refresh(); // Back from a review: its status may have changed
    });
  }

  stop(): void {
    if (this.#timer) clearInterval(this.#timer);
    this.#timer = null;
  }
}
