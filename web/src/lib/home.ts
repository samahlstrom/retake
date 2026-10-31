// The home screen's data (spec/home.md): GET /api/home's cards, their status words, and where the review page goes
// next. Shared by the home page (home/) and the review page's "All videos" and "Next video".

export type Status = 'to_review' | 'reviewed' | 'being_edited';

/** A version's streaming state, when the streaming side reports one. */
export interface Stream {
  state: string; // pending | rendering | complete
  done: number;
  total: number;
}

/** One project on the home. `url` and `thumb` are relative to the home. */
export interface Card {
  name: string;
  title: string;
  url: string;
  version: string;
  versions: number;
  duration: number | null;
  pushed: string;
  status: Status;
  takes: number;
  open: number;
  stream: Stream | null;
  thumb: string;
}

export interface HomeResponse {
  title: string;
  folder?: string;
  role: string;
  projects: Card[];
}

export const STATUS_TEXT: Record<Status, string> = {
  to_review: 'To review',
  reviewed: 'Reviewed',
  being_edited: 'Being edited',
};

export const NO_MORE = 'No more videos to review';

/** The streaming chip: "Rendering 3/12", "Not rendered yet", or nothing once it's complete. */
export function streamText(s: Stream | null | undefined): string | null {
  if (!s) return null;
  if (s.state === 'pending') return 'Not rendered yet';
  if (s.state === 'rendering') return s.total > 0 ? `Rendering ${s.done}/${s.total}` : 'Rendering';
  return null;
}

const STATUSES = new Set<string>(Object.keys(STATUS_TEXT));
const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const num = (v: unknown): number => (typeof v === 'number' && isFinite(v) ? v : 0);

/** A /api/home body, made safe to render. */
export function normalizeHome(body: unknown): HomeResponse {
  const b = (body && typeof body === 'object' ? body : {}) as Record<string, unknown>;
  const list = Array.isArray(b.projects) ? (b.projects as unknown[]) : [];
  const projects: Card[] = [];
  for (const x of list) {
    if (!x || typeof x !== 'object') continue;
    const c = x as Record<string, unknown>;
    const name = str(c.name);
    if (!name) continue;
    const s = c.stream && typeof c.stream === 'object' ? (c.stream as Record<string, unknown>) : null;
    projects.push({
      name,
      title: str(c.title) || name,
      url: str(c.url) || `p/${encodeURIComponent(name)}/`,
      version: str(c.version),
      versions: num(c.versions),
      duration: typeof c.duration === 'number' && isFinite(c.duration) ? c.duration : null,
      pushed: str(c.pushed),
      status: (STATUSES.has(str(c.status)) ? c.status : 'to_review') as Status,
      takes: num(c.takes),
      open: num(c.open),
      stream: s && str(s.state) ? { state: str(s.state), done: num(s.done), total: num(s.total) } : null,
      thumb: str(c.thumb),
    });
  }
  return { title: str(b.title), folder: str(b.folder) || undefined, role: str(b.role), projects };
}

/** Where a review page sits in a home: its path ends in /p/<name>/ (or /p/<name>/index.html). null: not in one. */
export function homeContext(pathname: string): { home: string; project: string } | null {
  const m = /^(.*\/)p\/([^/]+)\/(?:index\.html)?$/.exec(pathname);
  if (!m) return null;
  let project = m[2];
  try {
    project = decodeURIComponent(project);
  } catch {
    /* keep it as it is */
  }
  return { home: m[1], project };
}

/** A project's page, from the home's path. */
export const projectUrl = (home: string, name: string): string => `${home}p/${encodeURIComponent(name)}/`;

/** The next project to review after `current`: the first one in the home's order (to review first, newest push). */
export function nextToReview(cards: Card[], current: string | null): Card | null {
  return cards.find((c) => c.status === 'to_review' && c.name !== current) ?? null;
}

/** How many are left to review. */
export const toReview = (cards: Card[]): number => cards.filter((c) => c.status === 'to_review').length;

/** A length on a card: 0:42, 6:52, 1:02:03. */
export function fmtLength(sec: number | null): string {
  if (sec == null || !(sec > 0)) return '';
  const s = Math.round(sec),
    h = Math.floor(s / 3600),
    m = Math.floor((s % 3600) / 60),
    r = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${r}` : `${m}:${r}`;
}

/** A random id for this page load, sent with the unwatch call. */
export function viewerId(rand: () => number = Math.random): string {
  let s = '';
  for (let i = 0; i < 12; i++) s += '0123456789abcdefghijklmnopqrstuvwxyz'[Math.floor(rand() * 36) % 36];
  return s;
}

/** The unwatch call for a version (spec/home.md, "Close means close"), relative to the review page. */
export const unwatchUrl = (label: string, viewer: string): string =>
  `api/versions/${encodeURIComponent(label)}/unwatch?viewer=${encodeURIComponent(viewer)}`;
