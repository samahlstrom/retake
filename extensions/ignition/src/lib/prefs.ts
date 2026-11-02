// What Ignition keeps for itself, per video, in the browser's localStorage (spec/ignition.md: "the doc that was open,
// the caret"). Never on the server, never in the files.

export const TABS = ['idea', 'outline', 'script', 'refs', 'voice'] as const;
export type Tab = (typeof TABS)[number];

/** The documents behind the first three tabs. */
export const DOCS: Record<'idea' | 'outline' | 'script', string> = {
  idea: 'v0/idea.md',
  outline: 'v0/outline.md',
  script: 'v0/script.md',
};

export interface Prefs {
  tab: Tab;
  /** A document of refs/ open under the Refs tab (refs/links.md), or null for the list. */
  ref: string | null;
  /** The caret in each document, by path. */
  carets: Record<string, number>;
}

const keyOf = (video: string): string => `ignition.editor:${video}`;

export function storageOf(kind: 'local' | 'session'): Storage | null {
  try {
    return kind === 'local' ? globalThis.localStorage : globalThis.sessionStorage;
  } catch {
    return null;
  }
}

export function loadPrefs(storage: Pick<Storage, 'getItem'> | null, video: string): Prefs {
  const out: Prefs = { tab: 'idea', ref: null, carets: {} };
  try {
    const p = JSON.parse(storage?.getItem(keyOf(video)) ?? 'null') as Partial<Record<keyof Prefs, unknown>> | null;
    if (!p) return out;
    if (TABS.includes(p.tab as Tab)) out.tab = p.tab as Tab;
    if (typeof p.ref === 'string' && /^v0\/refs\/[^/]+\.md$/.test(p.ref)) out.ref = p.ref;
    if (p.carets && typeof p.carets === 'object') {
      for (const [k, v] of Object.entries(p.carets)) if (typeof v === 'number' && v >= 0) out.carets[k] = Math.floor(v);
    }
  } catch {
    /* a bad entry: start fresh */
  }
  return out;
}

export function savePrefs(storage: Pick<Storage, 'setItem'> | null, video: string, p: Prefs): void {
  try {
    storage?.setItem(keyOf(video), JSON.stringify(p));
  } catch {
    /* full or blocked: it's only a convenience */
  }
}

/** What the ask box calls a document: "the script", "links.md". */
export function docName(path: string): string {
  const base = path.split('/').pop() ?? path;
  return path.startsWith('v0/refs/') ? base : `the ${base.replace(/\.md$/, '')}`;
}
