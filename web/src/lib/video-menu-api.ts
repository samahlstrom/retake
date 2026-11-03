// The requests behind a video's ⋯ (spec/team.md, "Sharing a video from the page"; spec/home.md, "A video's ⋯";
// spec/automation.md, "An agent for one video"). `api` is the video's base (`p/<name>/` on the home, '' on its page),
// `home` the home's.
import { normalizeShares, type AgentState, type Reach, type ShareLink, type TrashInfo } from './video-menu';

/** A refused request's own words, else a plain line. */
async function fail(r: Response, fallback: string): Promise<never> {
  const b = (await r.json().catch(() => ({}))) as { error?: unknown };
  throw new Error(typeof b.error === 'string' && b.error ? b.error : fallback);
}

async function post<T>(url: string, body: unknown, fallback: string): Promise<T> {
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body ?? {}),
  });
  if (!r.ok) return fail(r, fallback);
  return (await r.json().catch(() => ({}))) as T;
}

export async function listShares(api: string): Promise<{ links: ShareLink[]; reach: Reach }> {
  const r = await fetch(`${api}api/shares`);
  if (!r.ok) return fail(r, 'Couldn’t read the links');
  return normalizeShares(await r.json());
}

export interface Made {
  url: string;
  link: ShareLink;
  reach: Reach;
}

export function makeShare(api: string, body: { label: string; role: string; expires: string }): Promise<Made> {
  return post<Made>(`${api}api/shares`, body, 'Couldn’t make a link');
}

export function revokeShare(api: string, id: number): Promise<unknown> {
  return post(`${api}api/shares/${id}/revoke`, {}, 'Couldn’t revoke it');
}

/** GET api/automation, as far as the agent list needs it; null when it can't be read. */
export async function loadAgents(api: string): Promise<AgentState | null> {
  try {
    const r = await fetch(`${api}api/automation`);
    return r.ok ? ((await r.json()) as AgentState) : null;
  } catch {
    return null;
  }
}

export function setVideoAgent(api: string, agent: string): Promise<unknown> {
  return post(`${api}api/automation/agent`, { agent }, 'Couldn’t change the agent');
}

export async function trashInfo(home: string, name: string): Promise<TrashInfo> {
  const r = await fetch(`${home}api/videos/${encodeURIComponent(name)}/trash`);
  if (!r.ok) return fail(r, 'Couldn’t read the folder');
  return (await r.json()) as TrashInfo;
}

export function moveToTrash(home: string, name: string): Promise<{ id: string; path: string }> {
  return post(`${home}api/videos/${encodeURIComponent(name)}/trash`, {}, 'Couldn’t move it to the Trash');
}

export function restoreFromTrash(home: string, id: string): Promise<{ name: string }> {
  return post(`${home}api/trash/${encodeURIComponent(id)}/restore`, {}, 'Couldn’t put it back');
}

export function revealFolder(home: string, name: string): Promise<unknown> {
  return post(`${home}api/videos/${encodeURIComponent(name)}/reveal`, {}, 'Couldn’t open the folder');
}

/** Copies text: the clipboard API, else a selection (plain http on a LAN address has no clipboard API). */
export async function copyText(text: string, el?: HTMLInputElement | null): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    if (!el) return false;
    el.select();
    return document.execCommand('copy');
  }
}
