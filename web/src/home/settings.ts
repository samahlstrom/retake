// The home's settings page (spec/home.md, "Settings"): GET and POST api/settings, for the owner. Pure parts, so
// they're unit-tested; Settings.svelte draws them.
import type { Machine } from '../lib/home';

export interface SettingsAgent {
  id: string;
  name: string;
}

export interface SettingsExt {
  id: string;
  name: string;
  description?: string;
  /** Turned on in the config (local) or by the server (hosted). */
  on: boolean;
  /** In effect now; differs from `on` until retake restarts. */
  loaded: boolean;
}

export interface Settings {
  hosted: boolean;
  agents: SettingsAgent[];
  /** The default agent; "" = each computer's first. */
  agent: string;
  /** The agent to switch to when its usage runs out; "" = pause and ask. */
  fallback: string;
  open_in: OpenIn;
  open_in_editable: boolean;
  /** Where agents can run on this computer, in order (tmux only where it's installed, so never on Windows). */
  open_in_choices: OpenIn[];
  extensions: SettingsExt[];
  extensions_editable: boolean;
  theme: { folder: string; used: boolean; name?: string };
  /** The config file (local). */
  config?: string;
  machines: Machine[];
  /** The videos folder (local; spec/setup.md), and whether retake start can switch it. */
  folder: string;
  folder_editable: boolean;
  /** Whether the agents can be tested here (POST api/settings/test). */
  test: boolean;
  /** How a link made in the page reaches people (local only; spec/home.md, "Settings"). */
  sharing?: Sharing;
}

/** Settings' Sharing: whether a link may start a Cloudflare tunnel, whether cloudflared is here, its install line. */
export interface Sharing {
  tunnel: 'cloudflare' | 'off';
  cloudflared: boolean;
  install: string;
}

export const SHARE_TUNNEL: { value: Sharing['tunnel']; label: string }[] = [
  { value: 'cloudflare', label: 'Through a Cloudflare tunnel' },
  { value: 'off', label: 'On my network only' },
];

export type OpenIn = 'background' | 'herdr' | 'tmux';

/** A change: only the fields given change. */
export interface SettingsChange {
  agent?: string;
  fallback?: string;
  open_in?: OpenIn;
  extensions?: Record<string, boolean>;
  folder?: string;
  share_tunnel?: Sharing['tunnel'];
}

export const OPEN_IN: { value: OpenIn; label: string; note: string }[] = [
  { value: 'background', label: 'In the background', note: 'You see the result.' },
  { value: 'herdr', label: 'In herdr', note: 'A workspace per video that you can watch and type into.' },
  { value: 'tmux', label: 'In tmux', note: 'A window per video in the tmux session “retake”.' },
];

const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const list = (v: unknown): Record<string, unknown>[] =>
  Array.isArray(v) ? (v.filter((x) => x && typeof x === 'object') as Record<string, unknown>[]) : [];

/** GET api/settings's answer, with whatever is missing or wrong filled in. */
export function normalizeSettings(b: unknown): Settings {
  const o = b && typeof b === 'object' ? (b as Record<string, unknown>) : {};
  const openIn = OPEN_IN.some((x) => x.value === o.open_in) ? (o.open_in as OpenIn) : 'background';
  const theme = o.theme && typeof o.theme === 'object' ? (o.theme as Record<string, unknown>) : {};
  return {
    hosted: o.hosted === true,
    agents: list(o.agents).map((a) => ({ id: str(a.id), name: str(a.name) || str(a.id) })),
    agent: str(o.agent),
    fallback: str(o.fallback),
    open_in: openIn,
    open_in_editable: o.open_in_editable === true,
    open_in_choices: openInChoices(o.open_in_choices, openIn),
    extensions: list(o.extensions).map((e) => ({
      id: str(e.id),
      name: str(e.name) || str(e.id),
      description: str(e.description) || undefined,
      on: e.on === true,
      loaded: e.loaded === true,
    })),
    extensions_editable: o.extensions_editable === true,
    theme: { folder: str(theme.folder), used: theme.used === true, name: str(theme.name) || undefined },
    config: str(o.config) || undefined,
    machines: list(o.machines).map((m) => ({
      id: str(m.id),
      name: str(m.name),
      online: m.online === true,
      agents: list(m.agents).map((a) => ({ id: str(a.id), name: str(a.name) })),
    })),
    folder: str(o.folder),
    folder_editable: o.folder_editable === true,
    test: o.test === true,
    ...sharingOf(o.sharing),
  };
}

/** The places to offer: the server's that the page knows (all of them from an older server), and the one in effect. */
function openInChoices(v: unknown, current: OpenIn): OpenIn[] {
  const known = OPEN_IN.map((x) => x.value);
  const out = Array.isArray(v) ? known.filter((id) => v.includes(id)) : known;
  return out.includes(current) ? out : [...out, current];
}

/** The "Where agents run" menu: the choices offered here, with their labels. */
export function openInOptions(s: Settings): { value: OpenIn; label: string }[] {
  return OPEN_IN.filter((o) => s.open_in_choices.includes(o.value)).map((o) => ({ value: o.value, label: o.label }));
}

function sharingOf(v: unknown): { sharing?: Sharing } {
  if (!v || typeof v !== 'object') return {};
  const o = v as Record<string, unknown>;
  return {
    sharing: {
      tunnel: o.tunnel === 'off' ? 'off' : 'cloudflare',
      cloudflared: o.cloudflared === true,
      install: str(o.install),
    },
  };
}

/** The agent tasks run with: the chosen default when it's offered, else the first agent offered (what a project's
 * first poll picks), else "". */
export function effectiveAgent(s: Settings): string {
  return s.agents.some((a) => a.id === s.agent) ? s.agent : (s.agents[0]?.id ?? '');
}

/** The default agent's choices: the agents offered, and a chosen one that isn't (any more), so it still shows. */
export function agentOptions(s: Settings): { value: string; label: string; disabled?: boolean }[] {
  const out: { value: string; label: string; disabled?: boolean }[] = s.agents.map((a) => ({
    value: a.id,
    label: a.name,
  }));
  if (s.agent && !s.agents.some((a) => a.id === s.agent))
    out.push({ value: s.agent, label: `${s.agent} (not found)`, disabled: true });
  return out;
}

/** The fallback's choices: pause and ask, or switch to another agent than the default. */
export function fallbackOptions(s: Settings): { value: string; label: string }[] {
  return [
    { value: '', label: 'Pause and ask me' },
    ...s.agents.filter((a) => a.id !== effectiveAgent(s)).map((a) => ({ value: a.id, label: `Switch to ${a.name}` })),
  ];
}

/** The change a new default agent makes: a fallback that was that agent goes back to "pause and ask". */
export function agentChange(s: Settings, agent: string): SettingsChange {
  return s.fallback === agent ? { agent, fallback: '' } : { agent };
}

/** Extensions whose state waits for a restart (turned on or off since retake started). */
export const pendingRestart = (s: Settings): SettingsExt[] => s.extensions.filter((e) => e.on !== e.loaded);

/** The computers line: the online ones by name, else that none is. */
export function computersText(s: Settings): string {
  const on = s.machines.filter((m) => m.online).map((m) => m.name);
  if (on.length) return on.join(', ');
  return s.machines.length ? 'None online' : 'None connected yet';
}

/** POST api/settings. Resolves to the new settings, or throws the server's one-line reason. */
export async function saveSettings(change: SettingsChange, f: typeof fetch = fetch): Promise<Settings> {
  let r: Response;
  try {
    r = await f('api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(change),
    });
  } catch {
    throw new Error('The server isn’t answering');
  }
  const body = (await r.json().catch(() => ({}))) as { error?: unknown };
  if (!r.ok) throw new Error(typeof body.error === 'string' && body.error ? body.error : 'Couldn’t save that');
  return normalizeSettings(body);
}

/** GET api/settings, or null with the reason. */
export async function loadSettings(f: typeof fetch = fetch): Promise<{ settings: Settings | null; error: string }> {
  try {
    const r = await f('api/settings', { cache: 'no-store' });
    if (r.status === 403) return { settings: null, error: 'Only the owner changes the settings.' };
    if (!r.ok) return { settings: null, error: 'This server has no settings page.' };
    return { settings: normalizeSettings(await r.json()), error: '' };
  } catch {
    return { settings: null, error: 'The server isn’t answering' };
  }
}
