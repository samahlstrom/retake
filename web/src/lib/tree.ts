// The home's folder tree (spec/home.md): finding a place in it, the rows the left column shows (opened, filtered),
// the status dots, and the URL hash. Pure functions; the state lives in home/home.svelte.ts.
import { videoName, type Card, type TreeNode } from './home';

/** A status dot: has questions, to review, being edited; "" for none (reviewed). */
export type Dot = 'question' | 'to_review' | 'being_edited' | '';

export const DOT_TEXT: Record<Exclude<Dot, ''>, string> = {
  question: 'Has questions',
  to_review: 'To review',
  being_edited: 'Being edited',
};

/** A video's dot. */
export function dotOf(c: Pick<Card, 'questions' | 'status'>): Dot {
  if (c.questions > 0) return 'question';
  if (c.status === 'to_review' || c.status === 'being_edited') return c.status;
  return '';
}

const RANK: Record<Dot, number> = { question: 3, to_review: 2, being_edited: 1, '': 0 };

/** The most pressing dot of some videos. */
export function topDot(cards: Card[]): Dot {
  let best: Dot = '';
  for (const c of cards) {
    const d = dotOf(c);
    if (RANK[d] > RANK[best]) best = d;
  }
  return best;
}

/** The node at a path, or null. */
export function findNode(tree: TreeNode, path: string): TreeNode | null {
  if (!path) return tree;
  let n: TreeNode | undefined = tree;
  for (const name of path.split('/')) {
    const want: string = n.path ? `${n.path}/${name}` : name;
    n = n.children.find((c) => c.path === want);
    if (!n) return null;
  }
  return n;
}

/** Every video in a node and the nodes in it. */
export function videosUnder(n: TreeNode): Card[] {
  const out = [...n.videos];
  for (const c of n.children) out.push(...videosUnder(c));
  return out;
}

/** The folders above a path, top first: "a/b/c" gives ["a", "a/b"]. */
export function ancestors(path: string): string[] {
  const parts = path ? path.split('/') : [];
  return parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join('/'));
}

/** Whether a path is the node at `place` or inside it ("" holds everything). */
export const within = (path: string, place: string): boolean =>
  !place || path === place || path.startsWith(`${place}/`);

/** The selection in a URL hash: "#/a/b%20c" is "a/b c"; anything else is the top. */
export function parseHash(hash: string): string {
  const h = hash.replace(/^#/, '');
  if (!h.startsWith('/')) return '';
  const parts = h
    .slice(1)
    .split('/')
    .filter((p) => p !== '');
  return parts
    .map((p) => {
      try {
        return decodeURIComponent(p);
      } catch {
        return p;
      }
    })
    .join('/');
}

/** The URL hash of a selection ("" for the top). */
export const hashOf = (path: string): string => (path ? `#/${path.split('/').map(encodeURIComponent).join('/')}` : '');

/** One row of the left column. */
export interface Row {
  kind: 'folder' | 'project' | 'video';
  /** Unique among the rows: the node's path, or "v:" and the video's name. */
  key: string;
  /** The node's path; for a video, its project's node. */
  path: string;
  title: string;
  depth: number;
  /** Something to open (a folder or project with something in it). */
  canOpen: boolean;
  open: boolean;
  /** A video's own dot; a closed folder's or project's most pressing one inside; else "". */
  dot: Dot;
  /** The parent row's key, or "" at the top. */
  parent: string;
  card?: Card;
}

const has = (n: TreeNode): boolean => n.videos.length > 0 || n.children.length > 0;

/**
 * The rows the column shows: the top's own videos, then its folders and projects, each followed by what's in it
 * when it's open. With a filter (case ignored), a row stays when its name or one of the names inside it contains
 * the text (a video's also by its folders below its project), and the rows on the way to a match are open; a
 * folder or project whose own name matches shows what's in it as usual.
 */
export function rows(tree: TreeNode, expanded: ReadonlySet<string>, filter = ''): Row[] {
  const q = filter.trim().toLowerCase();
  const matches = (s: string): boolean => s.toLowerCase().includes(q);
  // A video matches by its name, or by the folders it's in below its project (shorts/one-item).
  const videoMatches = (v: Card): boolean =>
    matches(videoName(v)) || (v.path.startsWith(`${v.project}/`) && matches(v.path.slice(v.project.length + 1)));
  const keep = new Map<TreeNode, boolean>();
  const kept = (n: TreeNode): boolean => {
    let k = keep.get(n);
    if (k === undefined) {
      k = matches(n.title) || n.videos.some(videoMatches) || n.children.some(kept);
      keep.set(n, k);
    }
    return k;
  };
  const out: Row[] = [];
  const video = (c: Card, node: TreeNode, depth: number, parent: string) =>
    out.push({
      kind: 'video',
      key: `v:${c.name}`,
      path: node.path,
      title: videoName(c),
      depth,
      canOpen: false,
      open: false,
      dot: dotOf(c),
      parent,
      card: c,
    });
  const walk = (n: TreeNode, depth: number, filtering: boolean, parent: string) => {
    for (const c of n.children) {
      if (filtering && !kept(c)) continue;
      const self = filtering && matches(c.title);
      const open = filtering ? !self || expanded.has(c.path) : expanded.has(c.path);
      out.push({
        kind: c.project ? 'project' : 'folder',
        key: c.path,
        path: c.path,
        title: c.title,
        depth,
        canOpen: has(c),
        open: open && has(c),
        dot: open ? '' : topDot(videosUnder(c)),
        parent,
      });
      if (!open) continue;
      for (const v of c.videos) if (!filtering || self || videoMatches(v)) video(v, c, depth + 1, c.path);
      walk(c, depth + 1, filtering && !self, c.path);
    }
  };
  for (const v of tree.videos) if (!q || videoMatches(v)) video(v, tree, 0, '');
  walk(tree, 0, !!q, '');
  return out;
}

/** Where a review page's back link goes: the remembered place when the video is in it, else the video's project. */
export function backPlace(remembered: string | null, card: Pick<Card, 'path' | 'project'>): string {
  if (remembered !== null && within(card.path, remembered)) return remembered;
  return card.project;
}

/** The localStorage key of a home's tree state (selection, opened folders, column), by the home's path; the review
 * page reads it for its back link. */
export const navKey = (homePath: string): string => `retake.nav:${homePath.replace(/index\.html$/, '')}`;

/** A home's remembered selection, or null when there is none. */
export function rememberedPlace(storage: Pick<Storage, 'getItem'> | null, homePath: string): string | null {
  try {
    const s = JSON.parse(storage?.getItem(navKey(homePath)) ?? 'null') as { selected?: unknown } | null;
    return typeof s?.selected === 'string' ? s.selected : null;
  } catch {
    return null;
  }
}
