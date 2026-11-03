// The first-run setup page (spec/setup.md): its state from GET api/setup, the install lines per OS, and the words.
// Pure parts, so they're unit-tested; Setup.svelte draws them.

export interface SetupAgent {
  id: string;
  name: string;
}

export type PlaceId = 'herdr' | 'tmux' | 'background';

export interface Place {
  id: PlaceId;
  available: boolean;
  bundled: boolean;
}

export interface SetupState {
  folder: string;
  folder_path: string;
  folder_exists: boolean;
  /** Videos in the folder, up to 99; null when it isn't a folder. */
  videos: number | null;
  agents: SetupAgent[];
  agent: string;
  open_in: PlaceId;
  places: Place[];
  ffmpeg: boolean;
  os: string;
  config: string;
}

/** The end-to-end tests read the page's state here (window.__setup). */
export interface SetupHook {
  loaded: boolean;
  state: SetupState | null;
  done: boolean;
}

/** An agent test's answer (POST api/setup/test, api/settings/test). */
export interface TryResult {
  ok: boolean;
  reply?: string;
  error?: string;
  seconds: number;
}

/** What an install line is for, and the line to copy. */
export interface InstallStep {
  agent: string;
  install: string;
  signIn: string;
}

const PLACES: PlaceId[] = ['herdr', 'tmux', 'background'];
const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const list = (v: unknown): Record<string, unknown>[] =>
  Array.isArray(v) ? (v.filter((x) => x && typeof x === 'object') as Record<string, unknown>[]) : [];

/** GET api/setup's answer, with whatever is missing or wrong filled in. */
export function normalizeState(b: unknown): SetupState {
  const o = b && typeof b === 'object' ? (b as Record<string, unknown>) : {};
  const places = list(o.places)
    .filter((p) => PLACES.includes(p.id as PlaceId))
    .map((p) => ({ id: p.id as PlaceId, available: p.available === true, bundled: p.bundled === true }));
  if (!places.some((p) => p.id === 'background')) places.push({ id: 'background', available: true, bundled: false });
  const openIn = places.some((p) => p.id === o.open_in && p.available) ? (o.open_in as PlaceId) : 'background';
  const agents = list(o.agents).map((a) => ({ id: str(a.id), name: str(a.name) || str(a.id) }));
  return {
    folder: str(o.folder),
    folder_path: str(o.folder_path),
    folder_exists: o.folder_exists === true,
    videos: typeof o.videos === 'number' ? o.videos : null,
    agents,
    agent: agents.some((a) => a.id === o.agent) ? str(o.agent) : (agents[0]?.id ?? ''),
    open_in: openIn,
    places,
    ffmpeg: o.ffmpeg !== false,
    os: str(o.os) || 'linux',
    config: str(o.config),
  };
}

/** How many videos the folder has, in words. */
export function videosLine(s: Pick<SetupState, 'videos' | 'folder_exists'>): string {
  if (!s.folder_exists || s.videos === null) return 'Not made yet. Retake makes it when you’re done.';
  if (s.videos === 0) return 'No videos in it yet. That’s fine.';
  if (s.videos >= 99) return '99+ videos';
  return s.videos === 1 ? '1 video' : `${s.videos} videos`;
}

/** The agents' makers' own install lines, then how to sign in (spec/setup.md). */
export function installSteps(os: string): InstallStep[] {
  if (os === 'windows')
    return [
      {
        agent: 'Claude Code',
        install: 'irm https://claude.ai/install.ps1 | iex',
        signIn: 'Then run claude and sign in.',
      },
      {
        agent: 'Codex',
        install: 'npm install -g @openai/codex',
        signIn: 'Then run codex and choose Sign in with ChatGPT.',
      },
    ];
  return [
    {
      agent: 'Claude Code',
      install: 'curl -fsSL https://claude.ai/install.sh | bash',
      signIn: 'Then run claude once and sign in.',
    },
    {
      agent: 'Codex',
      install: 'curl -fsSL https://chatgpt.com/codex/install.sh | sh',
      signIn: 'Then run codex and choose Sign in with ChatGPT.',
    },
  ];
}

/** Where to paste a line, by OS. */
export function terminalName(os: string): string {
  if (os === 'darwin') return 'Terminal (in Applications › Utilities)';
  if (os === 'windows') return 'PowerShell';
  return 'a terminal';
}

/** How to get ffmpeg when it's missing. */
export function ffmpegLine(os: string): string {
  if (os === 'darwin') return 'brew install ffmpeg';
  if (os === 'windows') return 'winget install ffmpeg';
  return 'sudo apt install ffmpeg';
}

/** A place's name and its one line. */
export function placeText(p: Place): { label: string; note: string } {
  switch (p.id) {
    case 'herdr':
      return {
        label: 'In herdr',
        note: p.bundled
          ? 'Watch each agent work and type to it. Comes with Retake.'
          : 'Watch each agent work and type to it.',
      };
    case 'tmux':
      return { label: 'In tmux', note: 'A window per video in the tmux session “retake”.' };
    default:
      return { label: 'In the background', note: 'Simplest. You see what it did.' };
  }
}

/** The places to offer: herdr and the background always (herdr greyed out when it's missing), tmux when it's there. */
export function placesShown(s: SetupState): Place[] {
  return s.places.filter((p) => p.id !== 'tmux' || p.available);
}

/** A test's one line: "Works · 4 s", or what's wrong. */
export function tryLine(r: TryResult): string {
  if (r.ok) return `Works · ${r.seconds < 1 ? '<1' : Math.round(r.seconds)} s`;
  return r.error || 'It didn’t answer.';
}

/** POST a JSON body; resolves to the answer, or throws the server's one-line reason. */
export async function post<T>(url: string, body: unknown, f: typeof fetch = fetch): Promise<T> {
  let r: Response;
  try {
    r = await f(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  } catch {
    throw new Error('Retake isn’t answering. Is it still running?');
  }
  const b = (await r.json().catch(() => ({}))) as { error?: unknown };
  if (!r.ok) throw new Error(typeof b.error === 'string' && b.error ? b.error : 'That didn’t work');
  return b as T;
}

/** Waits until the home answers at this address (after Done, or a new folder in Settings): `url` answers OK and `ready`
 * says yes. Resolves false after `ms`. */
export async function waitForHome(
  f: typeof fetch = fetch,
  ms = 30_000,
  every = 300,
  ready: (r: Response) => Promise<boolean> = () => Promise.resolve(true),
  url = 'api/home',
): Promise<boolean> {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try {
      const r = await f(url, { cache: 'no-store' });
      if (r.ok && (await ready(r))) return true;
    } catch {
      // between the setup server and the home: try again
    }
    await new Promise((res) => setTimeout(res, every));
  }
  return false;
}
