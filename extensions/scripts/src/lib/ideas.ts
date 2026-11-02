// The ideas page and New idea (spec/scripts.md): which videos are ideas, the projects a new one can go in, and
// making its folder.
import type { Card, Ctx } from '../types';
import { messageOf, statusOf } from './errors';
import { nth, slug } from './slug';
import { docPathFor } from './v0';

/** A folder of the home's tree, as far as New idea reads it. */
export interface Folder {
  title: string;
  path: string;
  project: boolean;
  children?: Folder[];
}

/** The ideas: videos whose only version is the kind's v0 (`no_cut`), most recently edited first. */
export function ideasOf<C extends Pick<Card, 'title' | 'kind' | 'kinds' | 'status' | 'changed'>>(
  home: { projects: C[] },
  kind: string,
): C[] {
  return home.projects
    .filter((c) => c.kind === kind || (c.status === 'no_cut' && (c.kinds ?? []).includes(kind)))
    .sort((a, b) => (b.changed ?? 0) - (a.changed ?? 0) || a.title.localeCompare(b.title));
}

export interface Project {
  /** Its folder, relative to the home's ("" for the home's own folder). */
  path: string;
  /** What the select shows: its path, or the home's title for the top. */
  label: string;
}

/** The tree's projects, in the tree's order. A server without a tree: the cards' projects. */
export function projectsOf(home: {
  title?: string;
  projects: Pick<Card, 'project'>[];
  tree?: Folder | null;
}): Project[] {
  const out: Project[] = [];
  const seen = new Set<string>();
  const add = (path: string, title: string) => {
    if (seen.has(path)) return;
    seen.add(path);
    out.push({ path, label: path || title || 'This folder' });
  };
  const walk = (n: Folder) => {
    if (n.project) add(n.path, n.title);
    for (const c of n.children ?? []) walk(c);
  };
  if (home.tree) walk(home.tree);
  if (!out.length) for (const c of home.projects) add(c.project, home.title ?? '');
  if (!out.length) add('', home.title ?? '');
  return out;
}

const within = (path: string, place: string): boolean => !place || path === place || path.startsWith(`${place}/`);

/** The project New idea starts on: the one selected in the tree (or the one the selection is in, or the first one in
 * a selected folder), else the top one. "" when there are none. */
export function defaultProject(projects: Project[], selected: string | null): string {
  if (selected !== null) {
    const exact = projects.find((p) => p.path === selected);
    if (exact) return exact.path;
    const holding = projects
      .filter((p) => p.path && within(selected, p.path))
      .sort((a, b) => b.path.length - a.path.length)[0];
    if (holding) return holding.path;
    const inside = projects.find((p) => p.path && within(p.path, selected));
    if (inside) return inside.path;
  }
  return projects[0]?.path ?? '';
}

/** The home's tree selection, as the home keeps it (`retake.nav:<home path>` in localStorage), or null. */
export function rememberedSelection(storage: Pick<Storage, 'getItem'> | null, homePath: string): string | null {
  try {
    const s = JSON.parse(storage?.getItem(`retake.nav:${homePath.replace(/index\.html$/, '')}`) ?? 'null') as {
      selected?: unknown;
    } | null;
    return typeof s?.selected === 'string' ? s.selected : null;
  } catch {
    return null;
  }
}

/** Hosted with no editing computer connected: New idea can't make a folder. */
export function offline(home: object): boolean {
  const h = home as { user?: unknown; machines?: unknown };
  return (
    !!h.user &&
    Array.isArray(h.machines) &&
    !h.machines.some((m) => !!m && typeof m === 'object' && (m as { online?: unknown }).online === true)
  );
}

/** A new idea's document: its title and a blank line, where the caret goes. */
export const ideaFile = (title: string): string => `# ${title.replace(/\s+/g, ' ').trim()}\n\n`;

/** Makes `<project>/<slug>/v0/<slug>_v0.md`, trying `-2`, `-3`, … while the folder exists (409). */
export async function createIdea(
  ctx: Pick<Ctx, 'createVideo'>,
  o: { project: string; title: string },
  tries = 50,
): Promise<{ name: string; url: string }> {
  const base = slug(o.title);
  for (let n = 1; ; n++) {
    try {
      const name = nth(base, n);
      return await ctx.createVideo({ project: o.project, name, files: { [docPathFor(name)]: ideaFile(o.title) } });
    } catch (e) {
      const exists = statusOf(e) === 409 || /\bexists\b/i.test(messageOf(e));
      if (!exists || n >= tries) throw e;
    }
  }
}

// What a new idea's page does when it opens, handed over in sessionStorage (the review page is another page load).
export const TALK_KEY = 'scripts.talk';
export const NEW_KEY = 'scripts.new';

/** Remembers that the video's page should start a talk-out (`talk`) or put the caret under the title. */
export function handOff(
  storage: Pick<Storage, 'setItem'> | null,
  video: string,
  talk: boolean,
  now = Date.now(),
): void {
  try {
    storage?.setItem(talk ? TALK_KEY : NEW_KEY, JSON.stringify({ video, at: now }));
  } catch {
    /* no storage: the page opens as usual */
  }
}

/** Whether a hand-off for this video is waiting (made in the last 2 minutes); reading it removes it. */
export function takeHandOff(
  storage: Pick<Storage, 'getItem' | 'removeItem'> | null,
  key: string,
  video: string,
  now = Date.now(),
): boolean {
  try {
    const raw = storage?.getItem(key);
    if (!raw) return false;
    const h = JSON.parse(raw) as { video?: unknown; at?: unknown };
    if (h.video !== video) return false;
    storage?.removeItem(key);
    return typeof h.at === 'number' && now - h.at < 120_000;
  } catch {
    return false;
  }
}

/** The browser's storage, or null where it's blocked. */
export function storageOf(kind: 'local' | 'session'): Storage | null {
  try {
    return kind === 'local' ? globalThis.localStorage : globalThis.sessionStorage;
  } catch {
    return null;
  }
}
