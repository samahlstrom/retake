// The home screen's state (spec/home.md): GET api/home on load, every REFRESH_MS while the page is visible, and when
// it comes back into view (a tab switched back, or Back from a review); and the folder tree's selection, opened
// folders and column, kept in the URL hash and localStorage.
import { SvelteSet } from 'svelte/reactivity';
import { normalizeHome, type Card, type HomeResponse, type TreeNode } from '../lib/home';
import { ancestors, findNode, hashOf, navKey, parseHash, videosUnder } from '../lib/tree';

export const REFRESH_MS = 15_000;

/** The column's width: at first, and its bounds. */
export const NAV_WIDTH = 264;
export const NAV_MIN = 200;
export const NAV_MAX = 480;
/** Up to this wide, the column is a drawer. */
export const PHONE_QUERY = '(max-width: 760px)';

/** The test hook (`window.__home`). */
export interface HomeHook {
  readonly loaded: boolean;
  readonly projects: Card[];
  readonly error: string;
  readonly selected: string;
}

/** What is kept in localStorage, per home. */
interface Saved {
  selected: string;
  expanded: string[];
  width: number;
  hidden: boolean;
}

export interface HomeDeps {
  fetch?: typeof fetch;
  /** localStorage, or null where there is none (tests pass a stand-in). */
  storage?: Pick<Storage, 'getItem' | 'setItem'> | null;
  /** The home's path, for the storage key (default: this page's). */
  path?: string;
}

const clampWidth = (w: number): number => Math.round(Math.min(NAV_MAX, Math.max(NAV_MIN, w)));

function defaultStorage(): Pick<Storage, 'getItem' | 'setItem'> | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

export class Home {
  /** The first answer came. */
  loaded = $state(false);
  res = $state.raw<HomeResponse>({
    title: '',
    role: '',
    projects: [],
    tree: { title: '', path: '', project: false, videos: [], children: [] },
  });
  /** Why the last read failed, or "". */
  error = $state('');
  /** The selected folder or project's path; "" is the top. */
  selected = $state('');
  /** The opened folders and projects. */
  readonly expanded = new SvelteSet<string>();
  /** The tree's filter text. */
  filter = $state('');
  /** The column's width (desktop), and whether it's hidden. */
  width = $state(NAV_WIDTH);
  hidden = $state(false);
  /** The page is phone-sized: the column is a drawer. */
  phone = $state(false);
  /** The drawer is open (phone). */
  drawer = $state(false);
  /** Thumbnails that failed to load: their card shows the empty frame. */
  readonly broken = new SvelteSet<string>();

  get tree(): TreeNode {
    return this.res.tree;
  }
  /** The selected node; the top when the selection is gone. */
  get node(): TreeNode {
    return findNode(this.tree, this.selected) ?? this.tree;
  }
  /** The videos the main area shows: the selected node's and those in it, in the home's order. */
  get videos(): Card[] {
    const mine = videosUnder(this.node).map((c) => c.name);
    return this.res.projects.filter((c) => mine.includes(c.name));
  }
  /** The selected node and the folders above it, top first (for the breadcrumb). */
  get trail(): TreeNode[] {
    const out = [this.tree];
    for (const p of [...ancestors(this.node.path), this.node.path]) {
      if (!p) continue;
      const n = findNode(this.tree, p);
      if (n) out.push(n);
    }
    return out;
  }

  readonly #fetch: typeof fetch;
  readonly #storage: Pick<Storage, 'getItem' | 'setItem'> | null;
  readonly #key: string;
  #timer: ReturnType<typeof setInterval> | null = null;
  #media: MediaQueryList | null = null;
  #visible = () => {
    if (document.visibilityState === 'visible') void this.refresh();
  };
  #pageshow = (e: PageTransitionEvent) => {
    if (e.persisted) void this.refresh();
  };
  #hashchange = () => {
    this.select(parseHash(location.hash), false);
  };
  #phone = () => {
    this.phone = !!this.#media?.matches;
    if (!this.phone) this.drawer = false;
  };

  constructor(deps: HomeDeps = {}) {
    this.#fetch = deps.fetch ?? ((...a) => fetch(...a));
    this.#storage = deps.storage === undefined ? defaultStorage() : deps.storage;
    this.#key = navKey(deps.path ?? (typeof location === 'undefined' ? '/' : location.pathname));
    this.#load();
    // Known before the first paint, so a phone never shows the desktop column for a moment.
    this.phone = typeof matchMedia === 'function' && matchMedia(PHONE_QUERY).matches;
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
      get selected() {
        return h.selected;
      },
    };
  })();

  /** The remembered state; a broken or missing one leaves the defaults. */
  #load(): void {
    try {
      const s = JSON.parse(this.#storage?.getItem(this.#key) ?? 'null') as Partial<Saved> | null;
      if (!s || typeof s !== 'object') return;
      if (typeof s.selected === 'string') this.selected = s.selected;
      if (Array.isArray(s.expanded)) for (const p of s.expanded) if (typeof p === 'string') this.expanded.add(p);
      if (typeof s.width === 'number' && isFinite(s.width)) this.width = clampWidth(s.width);
      this.hidden = s.hidden === true;
    } catch {
      /* nothing remembered */
    }
  }

  #save(): void {
    const s: Saved = { selected: this.selected, expanded: [...this.expanded], width: this.width, hidden: this.hidden };
    try {
      this.#storage?.setItem(this.#key, JSON.stringify(s));
    } catch {
      /* private mode, or full: the hash still has the selection */
    }
  }

  refresh = async (): Promise<void> => {
    try {
      const r = await this.#fetch('api/home', { cache: 'no-store' });
      if (!r.ok) {
        this.error = r.status === 401 || r.status === 403 ? 'This link can’t open these videos.' : 'Not reachable';
        return;
      }
      this.res = normalizeHome(await r.json());
      this.error = '';
      if (this.selected && !findNode(this.res.tree, this.selected)) this.select(''); // gone: the top
    } catch {
      this.error = 'Review server not reachable';
    } finally {
      this.loaded = true;
    }
  };

  thumbFailed = (name: string): void => {
    this.broken.add(name);
  };

  /** Selects a folder or project ("" for the top) and opens it and the folders above it. The URL hash follows
   * (replaced, so Back doesn't step through selections). */
  select(path: string, writeHash = true): void {
    this.selected = path;
    for (const p of [...ancestors(path), path]) if (p) this.expanded.add(p);
    if (writeHash && typeof history !== 'undefined' && typeof location !== 'undefined') {
      const url = location.pathname + location.search + hashOf(path);
      if (url !== location.pathname + location.search + location.hash) history.replaceState(history.state, '', url);
    }
    if (this.phone) this.drawer = false;
    this.#save();
  }

  /** Opens or closes a folder or project. */
  toggle(path: string, open = !this.expanded.has(path)): void {
    if (open) this.expanded.add(path);
    else this.expanded.delete(path);
    this.#save();
  }

  /** Sets the column's width (kept within its bounds). */
  resize(width: number): void {
    this.width = clampWidth(width);
    this.#save();
  }

  /** ☰: hides or shows the column (desktop), or opens or closes the drawer (phone, where `hidden` doesn't apply). */
  toggleNav(): void {
    if (this.phone) {
      this.drawer = !this.drawer;
      return;
    }
    this.hidden = !this.hidden;
    this.#save();
  }

  /** Load now, then keep the cards fresh while the page is on screen. A hash in the URL wins over the remembered
   * selection. */
  start(): void {
    if (location.hash) this.select(parseHash(location.hash), false);
    else this.select(this.selected); // remembered now, so a review's back link comes back here
    this.#media = typeof matchMedia === 'function' ? matchMedia(PHONE_QUERY) : null;
    this.#phone();
    this.#media?.addEventListener('change', this.#phone);
    void this.refresh();
    this.#timer = setInterval(this.#visible, REFRESH_MS);
    document.addEventListener('visibilitychange', this.#visible);
    addEventListener('pageshow', this.#pageshow);
    addEventListener('hashchange', this.#hashchange);
  }

  stop(): void {
    if (this.#timer) clearInterval(this.#timer);
    this.#timer = null;
    this.#media?.removeEventListener('change', this.#phone);
    document.removeEventListener('visibilitychange', this.#visible);
    removeEventListener('pageshow', this.#pageshow);
    removeEventListener('hashchange', this.#hashchange);
  }
}
