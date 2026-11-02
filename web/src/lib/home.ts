// The home screen's data (spec/home.md): GET /api/home's cards, their status words, and where the review page goes
// next. Shared by the home page (home/) and the review page's "All videos" and "Next video".

/** `no_cut`: the video's only versions are an extension's kind (an idea: spec/extensions.md, "Version kinds"). */
export type Status = 'to_review' | 'reviewed' | 'being_edited' | 'no_cut';

/** A version's streaming state, when the streaming side reports one. */
export interface Stream {
  state: string; // pending | rendering | complete
  done: number;
  total: number;
}

/** One video on the home (the routes call it a project: one review page). `url` and `thumb` are relative to the
 * home. */
export interface Card {
  name: string;
  /** Its folder's name. */
  title: string;
  /** Its own name: the newest version's file name without the version; "" before it has one. */
  video: string;
  /** The folder that holds it, and its project's folder, relative to the home's folder ("" for that folder). */
  path: string;
  project: string;
  url: string;
  version: string;
  versions: number;
  duration: number | null;
  pushed: string;
  status: Status;
  takes: number;
  open: number;
  /** Notes the agent asked about and waits on. */
  questions: number;
  stream: Stream | null;
  thumb: string;
  /** With `status: "no_cut"`: the extension kind of its only version (`ignition.doc`). */
  kind?: string;
  /** The extension kinds among its versions. */
  kinds?: string[];
  /** Unix milliseconds of its newest owned-file change or version. */
  changed?: number;
}

/** A folder of the home's tree (spec/home.md): one that organizes, or a project with its videos. */
export interface TreeNode {
  title: string;
  path: string;
  project: boolean;
  videos: Card[];
  children: TreeNode[];
}

/** A computer that edits (C5). In local mode, the server's own worker, online. */
export interface Machine {
  id: string;
  name: string;
  online: boolean;
  agents: { id: string; name: string }[];
}

export interface HomeResponse {
  title: string;
  folder?: string;
  role: string;
  projects: Card[];
  /** The folders, projects and videos. An older server sends none: then every video is at the top. */
  tree: TreeNode;
  workspace?: Workspace;
  /** C5: the editing computers. Absent on an older server. */
  machines?: Machine[];
  /** The signed-in person on a hosted server (the team server). Its presence means hosted mode. */
  user?: { email: string; name: string };
}

/** Hosted (the team server, signed in): an owner there connects their computer from the home when none is online.
 * Local mode never shows it (the computer is the server). */
export const needsComputer = (h: Pick<HomeResponse, 'user' | 'role' | 'machines'>): boolean =>
  !!h.user && h.role === 'owner' && Array.isArray(h.machines) && !h.machines.some((m) => m.online);

export interface WorkspaceProject {
  id: string;
  title: string;
  groups: { id: string; title: string }[];
}

export interface Workspace {
  revision: number;
  projects: WorkspaceProject[];
  placements: { video: string; project: string; group: string }[];
}

export const STATUS_TEXT: Record<Status, string> = {
  to_review: 'To review',
  reviewed: 'Reviewed',
  being_edited: 'Being edited',
  no_cut: 'No cut yet',
};

export const NO_MORE = 'No more videos to review';

/** A card's one status: rendering wins (it can't be reviewed yet), then a question waiting on an answer, then the
 * review status. `tone` picks its dot. */
export function cardStatus(c: Pick<Card, 'status' | 'stream'> & { questions?: number }): {
  text: string;
  tone: string;
} {
  const stream = streamText(c.stream);
  if (stream) return { text: stream, tone: 'rendering' };
  if (c.questions) return { text: 'Has questions', tone: 'question' };
  return { text: STATUS_TEXT[c.status], tone: c.status };
}

/** The name a video goes by: a video's folder's name (deadlock/item-shop is "item-shop"); for a project's own video
 * (its folder is its project), its family's name, else the folder's. */
export const videoName = (c: Pick<Card, 'video' | 'title' | 'name' | 'path' | 'project'>): string =>
  (c.path !== c.project ? c.title : c.video || c.title) || c.name;

const squash = (s: string): string => s.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '');

/** "Project › video", or only the project when the two are one name but for case and punctuation (item-shop and
 * itemshop). */
export function crumb(project: string, video: string): string[] {
  if (!project) return [video];
  if (!video || squash(project) === squash(video)) return [project];
  return [project, video];
}

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
    const path = str(c.path);
    projects.push({
      name,
      title: str(c.title) || name,
      video: str(c.video),
      path,
      project: typeof c.project === 'string' ? c.project : path,
      url: str(c.url) || `p/${encodeURIComponent(name)}/`,
      version: str(c.version),
      versions: num(c.versions),
      duration: typeof c.duration === 'number' && isFinite(c.duration) ? c.duration : null,
      pushed: str(c.pushed),
      status: (STATUSES.has(str(c.status)) ? c.status : 'to_review') as Status,
      takes: num(c.takes),
      open: num(c.open),
      questions: num(c.questions),
      stream: s && str(s.state) ? { state: str(s.state), done: num(s.done), total: num(s.total) } : null,
      thumb: str(c.thumb),
      ...(str(c.kind) ? { kind: str(c.kind) } : {}),
      ...(Array.isArray(c.kinds)
        ? { kinds: (c.kinds as unknown[]).filter((k): k is string => typeof k === 'string') }
        : {}),
      ...(typeof c.changed === 'number' && isFinite(c.changed) ? { changed: c.changed } : {}),
    });
  }
  const title = str(b.title);
  const byName = new Map(projects.map((c) => [c.name, c]));
  const tree = normalizeNode(b.tree, byName, 0) ?? { title, path: '', project: false, videos: projects, children: [] };
  tree.title ||= title;
  const result: HomeResponse = { title, folder: str(b.folder) || undefined, role: str(b.role), projects, tree };
  const w = b.workspace as Workspace | undefined;
  if (w && Array.isArray(w.projects) && Array.isArray(w.placements)) result.workspace = w;
  if (b.user && typeof b.user === 'object') {
    const u = b.user as Record<string, unknown>;
    result.user = { email: str(u.email), name: str(u.name) };
  }
  if (Array.isArray(b.machines)) {
    result.machines = (b.machines as unknown[]).flatMap((x) => {
      if (!x || typeof x !== 'object') return [];
      const m = x as Record<string, unknown>;
      const agents = Array.isArray(m.agents) ? (m.agents as Record<string, unknown>[]) : [];
      return [
        {
          id: str(m.id),
          name: str(m.name) || str(m.id),
          online: m.online === true,
          agents: agents.filter((a) => a && typeof a === 'object').map((a) => ({ id: str(a.id), name: str(a.name) })),
        },
      ];
    });
  }
  return result;
}

/** A tree node from /api/home, made safe: its videos are the cards of `projects` (by name), so both agree. */
function normalizeNode(x: unknown, cards: Map<string, Card>, depth: number): TreeNode | null {
  if (!x || typeof x !== 'object' || depth > 64) return null;
  const n = x as Record<string, unknown>;
  const videos = Array.isArray(n.videos) ? (n.videos as unknown[]) : [];
  const children = Array.isArray(n.children) ? (n.children as unknown[]) : [];
  return {
    title: str(n.title),
    path: str(n.path),
    project: n.project === true,
    videos: videos.flatMap((v) => {
      const c = v && typeof v === 'object' ? cards.get(str((v as Record<string, unknown>).name)) : undefined;
      return c ? [c] : [];
    }),
    children: children.flatMap((c) => normalizeNode(c, cards, depth + 1) ?? []),
  };
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
