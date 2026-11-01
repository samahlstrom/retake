// The home screen's state (spec/home.md): GET api/home on load, every REFRESH_MS while the page is visible, and when
// it comes back into view (a tab switched back, or Back from a review).
import { SvelteSet, SvelteURLSearchParams } from 'svelte/reactivity';
import { normalizeHome, workspaceOf, placementOf, type Workspace, type Card, type HomeResponse } from '../lib/home';

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
  saveError = $state('');
  saving = $state(false);
  selected = $state('');
  get workspace(): Workspace {
    return workspaceOf(this.res);
  }
  /** The project on screen: the one in the URL, or the only one there is (a home with one project opens straight to
   * its videos). */
  get project() {
    const projects = this.workspace.projects;
    if (this.selected) return projects.find((p) => p.id === this.selected);
    return projects.length === 1 ? projects[0] : undefined;
  }
  /** There is one project, so there is no Projects page. */
  get single(): boolean {
    return this.workspace.projects.length === 1;
  }
  videos(project: string, group?: string): Card[] {
    return this.res.projects.filter((v) => {
      const p = placementOf(this.workspace, v.name);
      return p.project === project && (group === undefined || p.group === group);
    });
  }
  /** Thumbnails that failed to load: their card shows the empty frame. */
  readonly broken = new SvelteSet<string>();

  readonly #fetch: typeof fetch;
  #timer: ReturnType<typeof setInterval> | null = null;
  #visible = () => {
    if (document.visibilityState === 'visible') void this.refresh();
  };
  #pageshow = (e: PageTransitionEvent) => {
    if (e.persisted) void this.refresh();
  };
  #location = () => {
    this.selected = new SvelteURLSearchParams(location.search).get('project') ?? '';
  };

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

  /** Persist before changing the visible layout; a conflict never overwrites somebody else's organization. */
  async save(change: (layout: Workspace) => void): Promise<boolean> {
    if (this.saving) return false;
    this.saving = true;
    this.saveError = '';
    const layout = structuredClone(this.workspace);
    change(layout);
    try {
      const r = await this.#fetch('api/workspace', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(layout),
      });
      if (!r.ok) {
        const error = await r.json();
        this.saveError = error.error || 'Could not save your changes. Try again.';
        if (r.status === 409) await this.refresh();
        return false;
      }
      this.res = { ...this.res, workspace: (await r.json()) as Workspace };
      return true;
    } catch {
      this.saveError = 'Could not reach Retake. Your changes were not saved.';
      return false;
    } finally {
      this.saving = false;
    }
  }

  async move(video: string, target: string): Promise<void> {
    const [project, group = ''] = target.split('/');
    await this.save((layout) => {
      layout.placements = layout.placements.filter((p) => p.video !== video);
      layout.placements.push({ video, project, group });
    });
  }

  /** Load now, then keep the cards fresh while the page is on screen. */
  start(): void {
    this.#location();
    void this.refresh();
    this.#timer = setInterval(this.#visible, REFRESH_MS);
    document.addEventListener('visibilitychange', this.#visible);
    addEventListener('pageshow', this.#pageshow);
    addEventListener('popstate', this.#location);
  }

  stop(): void {
    if (this.#timer) clearInterval(this.#timer);
    this.#timer = null;
    document.removeEventListener('visibilitychange', this.#visible);
    removeEventListener('pageshow', this.#pageshow);
    removeEventListener('popstate', this.#location);
  }
}
