// A video's ⋯ (spec/home.md, "A video's ⋯"; spec/notes.md, "The video's ⋯"): which items show for whom, the share
// sheet's choices and words, Move to Trash's line, and the agent list with its check. Shared by the home's cards and
// the review page; the requests are in video-menu-api.ts.

/** A video the ⋯ acts on. */
export interface VideoTarget {
  /** Its name (/p/<name>/). */
  name: string;
  /** How the page names it: the card's title, or the review page's video. */
  title: string;
  /** Its API's base, relative to the page and ending in '/': `p/<name>/` on the home, '' on its review page. */
  api: string;
  /** The home's base for the home's own routes (Move to Trash, Show in folder), or null outside a home. */
  home: string | null;
}

/** Who looks, and where. */
export interface MenuContext {
  /** The owner (locally the person at the computer; hosted a project owner or an admin). */
  owner: boolean;
  /** An approver's link or membership: Share… too (spec/team.md, "Sharing a video from the page"). */
  approver?: boolean;
  /** A local home (retake open <folder>, retake start), not the team server or a share link. */
  local: boolean;
  /** The editing computer offers an agent (else no agent list). null: not known yet (it's read when the menu opens). */
  agents: boolean | null;
  /** The version menu's Download when the server offers it (a hook: absent until then). */
  download?: { label: string } | null;
  /** The items this page shows: the home's cards have all five, the review page Share, Agent and Download. */
  page: 'card' | 'review';
}

export interface MenuItems {
  share: boolean;
  agent: boolean;
  download: boolean;
  reveal: boolean;
  trash: boolean;
}

/** What the ⋯ lists; none true: no ⋯ at all. */
export function menuItems(c: MenuContext): MenuItems {
  const card = c.page === 'card';
  return {
    share: c.owner || !!c.approver,
    agent: c.owner && c.agents !== false,
    download: !!c.download,
    reveal: card && c.owner && c.local,
    trash: card && c.owner && c.local,
  };
}

export const anyItem = (m: MenuItems): boolean => m.share || m.agent || m.download || m.reveal || m.trash;

// ---------------------------------------------------------------- the share sheet

export type Access = 'reviewer' | 'viewer';
export type Expiry = '7d' | '30d' | 'never';

export const ACCESS: { value: Access; label: string }[] = [
  { value: 'reviewer', label: 'Can comment' },
  { value: 'viewer', label: 'Can watch' },
];

export const EXPIRES: { value: Expiry; label: string }[] = [
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: 'never', label: 'Never' },
];

/** One of the video's links (GET api/shares). */
export interface ShareLink {
  id: number;
  role: string;
  label: string;
  created: string;
  expires: string;
}

/** How a link reaches people (spec/team.md, "How a link reaches people"). */
export interface Reach {
  kind: string;
  host: string;
  note: string;
}

/** The access's words for a role. */
export const accessWord = (role: string): string => (role === 'viewer' ? 'Can watch' : 'Can comment');

/** "Nov 9" (this year) or "Nov 9, 2027". */
export function shortDate(iso: string, now: Date = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const opts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' };
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return d.toLocaleDateString('en-US', opts);
}

/** A link's line in the sheet: who, access, until when. */
export function linkLine(l: ShareLink, now: Date = new Date()): { who: string; detail: string } {
  const until = l.expires ? `until ${shortDate(l.expires, now)}` : 'never expires';
  return { who: l.label || 'Anyone with the link', detail: `${accessWord(l.role)} · ${until}` };
}

/** What Create link says while it works: a tunnel takes a moment to start. */
export const creatingWord = (r: Reach | null): string =>
  r?.kind === 'tunnel' && !r.host ? 'Starting a tunnel…' : 'Creating…';

/** Normalizes GET api/shares. */
export function normalizeShares(b: unknown): { links: ShareLink[]; reach: Reach } {
  const o = b && typeof b === 'object' ? (b as Record<string, unknown>) : {};
  const str = (v: unknown) => (typeof v === 'string' ? v : '');
  const r = o.reach && typeof o.reach === 'object' ? (o.reach as Record<string, unknown>) : {};
  const links = (Array.isArray(o.links) ? o.links : [])
    .filter((x): x is Record<string, unknown> => !!x && typeof x === 'object' && typeof x.id === 'number')
    .map((x) => ({
      id: x.id as number,
      role: str(x.role),
      label: str(x.label),
      created: str(x.created),
      expires: str(x.expires),
    }));
  return { links, reach: { kind: str(r.kind), host: str(r.host), note: str(r.note) } };
}

// ---------------------------------------------------------------- Move to Trash

/** GET api/videos/<name>/trash. */
export interface TrashInfo {
  name: string;
  title: string;
  path: string;
  size: number;
  files: number;
  versions: number;
  ok: boolean;
  why?: string;
}

/** "2.4 GB", "820 MB", "12 KB", "0 KB". */
export function fmtSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const n = v >= 100 || v === 0 ? Math.round(v).toString() : v >= 10 ? v.toFixed(0) : v.toFixed(1).replace(/\.0$/, '');
  return `${n} ${units[i]}`;
}

/** The confirm's line under the folder: "6 versions · 2.4 GB". */
export function trashLine(t: Pick<TrashInfo, 'versions' | 'size'>): string {
  const versions = t.versions === 1 ? '1 version' : `${t.versions} versions`;
  return `${versions} · ${fmtSize(t.size)}`;
}

/** How long Undo stays after a move. */
export const UNDO_MS = 8000;

// ---------------------------------------------------------------- the agent for this video

interface AgentLike {
  id: string;
  name: string;
}
interface WorkerLike {
  id: string;
  agents: AgentLike[];
}
/** What the agent list needs of GET api/automation. */
export interface AgentState {
  config: { worker: string; agent: string };
  workers: WorkerLike[];
  video_agent?: string;
}

export interface AgentChoices {
  /** The agents the editing computer offers, the one in use checked. */
  agents: (AgentLike & { checked: boolean })[];
  /** The default agent's name, for "Use my default (…)". */
  defaultName: string;
  /** Checked when the video has no agent of its own (or one no computer offers any more). */
  useDefault: boolean;
}

/** The agent list: the selected worker's agents (before one is selected, every worker's, once each). */
export function agentChoices(a: AgentState | null): AgentChoices {
  const out: AgentLike[] = [];
  for (const w of a?.workers ?? []) {
    if (a!.config.worker && w.id !== a!.config.worker) continue;
    for (const ag of w.agents ?? [])
      if (!out.some((x) => x.id === ag.id)) out.push({ id: ag.id, name: ag.name || ag.id });
  }
  const mine = a?.video_agent ?? '';
  const own = !!mine && out.some((x) => x.id === mine);
  const def = a?.config.agent || out[0]?.id || '';
  return {
    agents: out.map((x) => ({ ...x, checked: own && x.id === mine })),
    defaultName: out.find((x) => x.id === def)?.name ?? def,
    useDefault: !own,
  };
}
