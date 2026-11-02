// The sidebar's live state: GET /api/notes for the version on screen, refetched on every `notes` event of
// GET /api/events (SSE), plus job progress from its `job` events. A server without the notes routes (milestone 1, the
// legacy player) leaves `available` false, and the page shows no sidebar at all.
import { applyJob, jobLine } from './jobs';
import type { VersionEvent } from '../types';
import type { Job, NoteItem, NotesResponse } from './types';

const enc = encodeURIComponent;
export const notesUrl = (v: string): string => `api/notes?v=${enc(v)}`;
export const reopenUrl = (id: number): string => `api/notes/${enc(String(id))}/reopen`;
export const doneUrl = (take: string, version: string | null): string =>
  `api/session/${enc(take)}/done?v=${enc(String(version))}`;
export const eventsUrl = (since: number | null): string =>
  since == null ? 'api/events' : `api/events?since=${enc(String(since))}`;

/** Job kinds that are an extension's, not the comments': they never show on the progress line. */
const EXT_JOBS = new Set(['transcribe-file']);

/** localStorage key of the desktop sidebar's open/closed state. */
export const SIDEBAR_KEY = 'retake.sidebar';
/** Retry a closed event stream this long after it closed, at most EVENTS_TRIES times in a row before it ever opened. */
export const EVENTS_RETRY_MS = 15_000;
export const EVENTS_TRIES = 5;

/** What the page needs of an EventSource (tests pass a fake). */
export interface EventSourceLike {
  readonly readyState: number;
  onerror: ((e: Event) => void) | null;
  addEventListener(type: string, cb: (e: MessageEvent) => void): void;
  close(): void;
}

export interface NotesDeps {
  onAutomation?: () => void;
  /** Read the whole family's comments (`scope=video`): the review page keeps each on its own version. */
  continuous?: boolean;
  onLoad?: (label: string) => void;
  fetch?: typeof fetch;
  eventSource?: (url: string) => EventSourceLike;
  storage?: () => Pick<Storage, 'getItem' | 'setItem'> | null;
  /** The family changed (a `version` event), with its data (`{}` when it has none). */
  onVersion?: (e: VersionEvent) => void;
  setTimeout?: (fn: () => void, ms: number) => unknown;
}

const browserStorage = (): Storage | null => {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null; // blocked
  }
};

function parse<T>(data: unknown): T | null {
  try {
    return typeof data === 'string' && data ? (JSON.parse(data) as T) : null;
  } catch {
    return null;
  }
}

/** A /api/notes body, made safe to render. */
export function normalize(body: Partial<NotesResponse> | null, label: string): NotesResponse {
  const items = Array.isArray(body?.items) ? body.items : [];
  return {
    version: typeof body?.version === 'string' ? body.version : label,
    newest: body?.newest === true,
    ...(body?.scope === 'video' || body?.scope === 'review' ? { scope: body.scope } : {}),
    items: items.filter(
      (it): it is NoteItem => !!it && typeof it.id === 'number' && typeof it.t0 === 'number' && isFinite(it.t0),
    ),
  };
}

export class Notes {
  /** The server has the notes routes (a GET /api/notes answered once). */
  available = $state(false);
  /** The sidebar items of the version on screen. */
  res = $state.raw<NotesResponse | null>(null);
  /** Jobs worth showing (running, queued, or failed). */
  jobs = $state.raw<Job[]>([]);
  readonly job = $derived(jobLine(this.jobs));
  /** Desktop: the sidebar column is open. Remembered in localStorage. */
  open = $state(true);
  /** Phone: the list under the controls is open. */
  phoneOpen = $state(false);
  /** The sidebar has had something to show on this page (it then stays, see view.presence). */
  everShown = $state(false);
  deleting = $state(false);
  deleteError = $state('');
  deleted = $state<number[]>([]);

  #label: string | null = null;
  #req = 0;
  #es: EventSourceLike | null = null;
  #since: number | null = null;
  #everOpened = false;
  #tries = 0;
  /** Listeners of the page's events (extensions' `ctx.on`), by event name. */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #listeners = new Map<string, Set<(data: unknown) => void>>();

  constructor(private readonly deps: NotesDeps = {}) {}

  /** Listen to an event of GET api/events (`notes`, `job`, `version`, `automation`, `ext.files`, `chat`) with its data. A
   * listener that throws is logged; the page's own handling goes on. */
  on = (name: string, cb: (data: unknown) => void): (() => void) => {
    let set = this.#listeners.get(name);
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
    if (!set) this.#listeners.set(name, (set = new Set()));
    const s = set;
    s.add(cb);
    return () => {
      s.delete(cb);
    };
  };

  #emit(name: string, data: unknown): void {
    for (const cb of [...(this.#listeners.get(name) ?? [])]) {
      try {
        cb(data);
      } catch (err) {
        console.error(`retake: a ${name} event listener failed:`, err);
      }
    }
  }

  #fetch(url: string, init?: RequestInit): Promise<Response> {
    return (this.deps.fetch ?? fetch)(url, init);
  }

  /** Read the remembered open/closed state (in the browser, after the markup is hydrated). */
  restore(): void {
    try {
      const v = (this.deps.storage ?? browserStorage)()?.getItem(SIDEBAR_KEY);
      if (v === 'closed' || v === 'open') this.open = v === 'open';
    } catch {
      /* blocked */
    }
  }

  /** N, or the sidebar's own buttons: open or close it (the phone list is its own toggle). */
  toggle = (phone = false): void => {
    if (phone) {
      this.phoneOpen = !this.phoneOpen;
      return;
    }
    this.open = !this.open;
    try {
      (this.deps.storage ?? browserStorage)()?.setItem(SIDEBAR_KEY, this.open ? 'open' : 'closed');
    } catch {
      /* blocked */
    }
  };

  /**
   * Load the items of `label` (the version on screen). What is shown stays until the answer is in (no flash on a C
   * flip), and out-of-order answers are dropped.
   */
  async load(label: string | null): Promise<void> {
    this.#label = label;
    if (!label) return;
    const req = ++this.#req;
    try {
      const r = await this.#fetch(notesUrl(label) + (this.deps.continuous ? '&scope=video' : ''));
      if (req !== this.#req) return;
      if (!r.ok) {
        await r.body?.cancel().catch(() => {});
        this.res = null;
        return;
      }
      const body = parse<NotesResponse>(await r.text());
      if (req !== this.#req) return;
      this.res = normalize(body, label);
      this.deps.onLoad?.(label);
      if (Array.isArray(body?.jobs)) this.jobs = body.jobs.filter((j) => !EXT_JOBS.has(j?.kind));
      if (!this.available) {
        this.available = true;
        this.connect();
      }
    } catch {
      /* server unreachable: keep what is shown */
    }
  }

  refresh = (): Promise<void> => this.load(this.#label);

  /** Listen to GET /api/events: `notes` refetches, `job` updates the progress line, `version` reloads the family.
   * Each event also goes to the listeners of `on` (extensions), and so does `ext.files`. */
  connect(): void {
    if (this.#es) return;
    const make =
      this.deps.eventSource ?? (typeof EventSource === 'undefined' ? null : (u: string) => new EventSource(u));
    if (!make) return;
    const es = make(eventsUrl(this.#since));
    this.#es = es;
    const seen = (e: MessageEvent) => {
      const n = Number.parseInt(e.lastEventId, 10);
      if (Number.isFinite(n)) this.#since = n;
    };
    es.addEventListener('open', () => {
      this.#tries = 0;
      if (this.#everOpened) void this.refresh(); // back after a drop: catch up
      this.#everOpened = true;
    });
    es.addEventListener('notes', (e) => {
      seen(e);
      void this.refresh();
      this.#emit('notes', parse<unknown>(e.data) ?? {});
    });
    es.addEventListener('job', (e) => {
      seen(e);
      const j = parse<Job>(e.data);
      if (j && j.id != null && typeof j.kind === 'string') {
        this.onJob(j);
        this.#emit('job', j);
      }
    });
    es.addEventListener('automation', (e) => {
      seen(e);
      this.deps.onAutomation?.();
      this.#emit('automation', parse<unknown>(e.data) ?? {});
    });
    es.addEventListener('version', (e) => {
      seen(e);
      const d = parse<VersionEvent>(e.data);
      this.deps.onVersion?.(d && typeof d === 'object' ? d : {});
      this.#emit('version', d && typeof d === 'object' ? d : {});
    });
    // An extension's owned files changed (while a page holds its watch lease): only its listeners care.
    es.addEventListener('ext.files', (e) => {
      seen(e);
      const d = parse<{ ext?: unknown; files?: unknown }>(e.data);
      if (d && typeof d.ext === 'string' && Array.isArray(d.files)) this.#emit('ext.files', d);
    });
    // The chat with the agent (spec/chat.md): its messages, words and turn.
    es.addEventListener('chat', (e) => {
      seen(e);
      const d = parse<unknown>(e.data);
      if (d && typeof d === 'object') this.#emit('chat', d);
    });
    es.onerror = () => {
      if (es.readyState !== 2) return; // CONNECTING: the browser retries by itself
      es.close();
      if (this.#es === es) this.#es = null;
      if (!this.#everOpened && ++this.#tries >= EVENTS_TRIES) return; // no events route: stop asking
      (this.deps.setTimeout ?? setTimeout)(() => this.connect(), EVENTS_RETRY_MS);
    };
  }

  disconnect(): void {
    this.#es?.close();
    this.#es = null;
  }

  onJob(j: Job): void {
    if (EXT_JOBS.has(j.kind)) return; // an extension's (a talk-out's transcription): it shows its own progress
    this.jobs = applyJob(this.jobs, j);
    if (j.kind === 'notes' && j.state === 'done') void this.refresh();
  }

  /** Dismiss a shown failure. */
  dismissJob = (): void => {
    this.jobs = this.jobs.filter((j) => j.state !== 'failed');
  };

  /** Reopen an answered note (older versions only). Resolves to whether the server took it. */
  async reopen(id: number): Promise<boolean> {
    try {
      const r = await this.#fetch(reopenUrl(id), { method: 'POST' });
      await r.body?.cancel().catch(() => {});
      if (r.ok) void this.refresh();
      return r.ok;
    } catch {
      return false;
    }
  }

  /** Delete only after the server accepts it; preserve an undo stack for several quick deletions. */
  async deleteComment(id: number, restore = false): Promise<boolean> {
    if (this.deleting) return false;
    this.deleting = true;
    this.deleteError = '';
    try {
      const r = await this.#fetch(`api/notes/${enc(String(id))}/${restore ? 'restore' : 'delete'}`, { method: 'POST' });
      await r.body?.cancel().catch(() => {});
      if (!r.ok) throw new Error('request failed');
      this.deleted = restore ? this.deleted.filter((n) => n !== id) : [...this.deleted.filter((n) => n !== id), id];
      await this.refresh();
      return true;
    } catch {
      this.deleteError = restore
        ? 'Could not restore this comment. Try Undo again.'
        : 'Could not delete this comment. Try again.';
      return false;
    } finally {
      this.deleting = false;
    }
  }

  undoDelete = async (): Promise<void> => {
    const id = this.deleted.at(-1);
    if (id !== undefined) await this.deleteComment(id, true);
  };

  /** Done: the take is closed; the server queues its transcription and notes. Resolves to the HTTP status (0 when
   * unreachable). */
  async postDone(take: string, version: string | null, send = false, agent = ''): Promise<number> {
    try {
      // `send`: queue the take's notes for the agent once they're processed (contract C3), with `agent` the one this
      // send picked (spec/automation.md). An older server ignores the body and does what it always did.
      const body = agent ? { send: true, agent } : { send: true };
      const r = await this.#fetch(
        doneUrl(take, version),
        send
          ? { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
          : { method: 'POST' },
      );
      await r.body?.cancel().catch(() => {});
      return r.status;
    } catch {
      return 0;
    }
  }

  /** For the test hook. */
  get snapshot(): {
    available: boolean;
    version: string | null;
    newest: boolean;
    items: NoteItem[];
    jobs: Job[];
    job: ReturnType<typeof jobLine>;
  } {
    return {
      available: this.available,
      version: this.res?.version ?? this.#label,
      newest: !!this.res?.newest,
      items: this.res?.items ?? [],
      jobs: this.jobs,
      job: this.job,
    };
  }
}
