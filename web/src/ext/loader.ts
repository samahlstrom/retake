// Extensions on the page (spec/extensions.md, "Loading and mounting"): GET api/ext once per page, and an extension's
// module imported the first time one of its slots shows, never before. A module that doesn't load, or a mount that
// throws, shows one quiet line in its slot and is logged once; the rest of the page never notices.
import type { Module, SlotName } from './retake-ext';
import { makeCtx, type Host } from './ctx';

export const SLOTS: readonly SlotName[] = ['page', 'version', 'sidebar-tab', 'review-panel'];

/** The module's export for each slot. */
export const EXPORTS: Record<SlotName, keyof Module> = {
  page: 'page',
  version: 'version',
  'sidebar-tab': 'sidebarTab',
  'review-panel': 'reviewPanel',
};

/** A kind of version an extension adds: `id` is the full id (`ignition.doc`), `kind` its own (`doc`). */
export interface ExtKind {
  id: string;
  kind: string;
  folder: string;
  marker: string;
  label: string;
  status: string;
  record: boolean;
}

/** One `[[slots]]` entry: `kind` is a full kind id or ''. */
export interface ExtSlotDef {
  slot: string;
  kind: string;
  title: string;
  when: string;
}

export interface ExtNav {
  title: string;
  /** A path in the extension's folder (`ui/spark.svg`), or ''. */
  icon: string;
  list: string;
  action: { title: string; route: string } | null;
}

/** One extension of GET api/ext. */
export interface ExtInfo {
  id: string;
  name: string;
  version: string;
  /** `ext/<id>/`, relative to the page's root. */
  base: string;
  module: string;
  style: string;
  nav: ExtNav | null;
  kinds: ExtKind[];
  slots: ExtSlotDef[];
  /** False when the video's editing computer lacks it (hosted). */
  machine: boolean;
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const obj = (v: unknown): Record<string, unknown> | null =>
  v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
const list = (v: unknown): Record<string, unknown>[] =>
  Array.isArray(v) ? v.map(obj).filter((x): x is Record<string, unknown> => !!x) : [];

/** A GET api/ext body, made safe: unknown fields are dropped, an extension without an id or module is left out. */
export function normalizeExts(body: unknown): ExtInfo[] {
  const out: ExtInfo[] = [];
  for (const e of list(obj(body)?.extensions)) {
    const id = str(e.id);
    if (!/^[a-z][a-z0-9-]{0,31}$/.test(id) || out.some((x) => x.id === id)) continue;
    const nav = obj(e.nav),
      action = obj(nav?.action);
    out.push({
      id,
      name: str(e.name) || id,
      version: str(e.version),
      base: str(e.base) || `ext/${id}/`,
      module: str(e.module),
      style: str(e.style),
      nav: nav
        ? {
            title: str(nav.title) || str(e.name) || id,
            icon: str(nav.icon),
            list: str(nav.list),
            action: action && str(action.route) !== '' ? { title: str(action.title), route: str(action.route) } : null,
          }
        : null,
      kinds: list(e.kinds).map((k) => ({
        id: str(k.id) || `${id}.${str(k.kind)}`,
        kind: str(k.kind) || str(k.id).split('.').pop() || '',
        folder: str(k.folder) || 'v0',
        marker: str(k.marker),
        label: str(k.label),
        status: str(k.status),
        record: k.record === true,
      })),
      slots: list(e.slots)
        .filter((s) => (SLOTS as readonly string[]).includes(str(s.slot)))
        .map((s) => ({ slot: str(s.slot), kind: str(s.kind), title: str(s.title), when: str(s.when) || 'always' })),
      machine: e.machine !== false,
    });
  }
  return out;
}

export interface LoaderDeps {
  fetch?: typeof fetch;
  /** import(): tests pass a stand-in. */
  importer?: (url: string) => Promise<unknown>;
  /** What relative URLs resolve against (default: the document's base, so a module's URL never depends on where
   * the page's own script lives). */
  base?: () => string;
  /** Where api/ext and ext/ are, relative to the page ('' for this page's own). */
  root?: string;
  log?: (...a: unknown[]) => void;
}

const nativeImport = (url: string): Promise<unknown> => import(/* @vite-ignore */ url);
const docBase = (): string => (typeof document === 'undefined' ? 'http://localhost/' : document.baseURI);

export class ExtLoader {
  #list: Promise<ExtInfo[]> | null = null;
  readonly #modules = new Map<string, Promise<Module>>();
  readonly #logged = new Set<string>();

  constructor(private readonly deps: LoaderDeps = {}) {}

  get root(): string {
    return this.deps.root ?? '';
  }

  /** GET api/ext, once per page. No route (an older server), an error or no network: no extensions. */
  list(): Promise<ExtInfo[]> {
    this.#list ??= (async () => {
      try {
        const r = await (this.deps.fetch ?? fetch)(`${this.root}api/ext`);
        if (!r.ok) {
          await r.body?.cancel().catch(() => {});
          return [];
        }
        return normalizeExts(await r.json());
      } catch {
        return [];
      }
    })();
    return this.#list;
  }

  /** `ext/<id>/<module>?v=<version>`, absolute: import() resolves relative URLs against the importing script. */
  moduleUrl(ext: ExtInfo): string {
    const rel = `${this.root}${ext.base}${ext.module}?v=${encodeURIComponent(ext.version)}`;
    return new URL(rel, (this.deps.base ?? docBase)()).href;
  }

  /** The extension's module, imported once (the first time one of its slots shows). */
  module(ext: ExtInfo): Promise<Module> {
    let p = this.#modules.get(ext.id);
    if (!p) {
      p = ext.module
        ? (this.deps.importer ?? nativeImport)(this.moduleUrl(ext)).then((m) => (m ?? {}) as Module)
        : Promise.reject(new Error('it has no module'));
      this.#modules.set(ext.id, p);
    }
    return p;
  }

  /** Logs a slot's failure to the console, once per extension and message. */
  logOnce(ext: ExtInfo, slot: SlotName, err: unknown): void {
    const key = `${ext.id}\n${message(err)}`;
    if (this.#logged.has(key)) return;
    this.#logged.add(key);
    (this.deps.log ?? console.error)(`retake: the ${ext.name} extension (${slot}) didn't load:`, err);
  }
}

/** An error's message, for the slot's line. */
export function message(err: unknown): string {
  const m = err instanceof Error ? err.message : typeof err === 'string' ? err : '';
  return m.trim() || 'unknown error';
}

/** The one quiet line a failed slot shows. */
export const failedLine = (ext: Pick<ExtInfo, 'name'>, err: unknown): string =>
  `${ext.name} didn't load: ${message(err)}`;

export interface Mounted {
  /** Settles once the module mounted, or its failure line is up. */
  ready: Promise<void>;
  /** Unmounts (the module's cleanup, then the ctx's subscriptions) and empties the element. */
  unmount(): void;
}

/**
 * Mounts `slot` of `ext` into `el`: imports the module (the first time), calls its export with `el` and a fresh ctx,
 * and keeps what it returns. A load error, a missing export or a mount that throws empties `el` and puts the failure
 * line in it.
 */
export function mountSlot(el: HTMLElement, loader: ExtLoader, ext: ExtInfo, slot: SlotName, host: Host): Mounted {
  let cleanup: (() => void) | null = null,
    gone = false;
  const made = makeCtx(ext, slot, host);
  const fail = (err: unknown) => {
    if (gone) return;
    loader.logOnce(ext, slot, err);
    try {
      cleanup?.();
    } catch {
      /* it failed already */
    }
    cleanup = null;
    made.dispose();
    const line = el.ownerDocument.createElement('p');
    line.className = 'ext-error';
    line.setAttribute('role', 'status');
    line.textContent = failedLine(ext, err);
    el.replaceChildren(line);
  };
  const ready = loader.module(ext).then((mod) => {
    if (gone) return;
    const fn = mod[EXPORTS[slot]];
    if (typeof fn !== 'function') throw new Error(`it has no ${EXPORTS[slot]} export`);
    const r = fn(el, made.ctx);
    if (typeof r === 'function') {
      if (gone) r();
      else cleanup = r;
    }
  });
  return {
    ready: ready.catch(fail),
    unmount() {
      if (gone) return;
      gone = true;
      try {
        cleanup?.();
      } catch (err) {
        loader.logOnce(ext, slot, err);
      }
      cleanup = null;
      made.dispose();
      el.replaceChildren();
    },
  };
}
