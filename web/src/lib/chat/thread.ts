// The chat with the agent (spec/chat.md): the thread as GET api/chat gives it, and the `chat` events that keep it
// current. Pure, so it is unit-tested.
import type { AutomationState } from '../automation.svelte';

export type ChatRole = 'person' | 'agent' | 'tool' | 'question' | 'info';

export interface ChatOption {
  text: string;
  image?: { url?: string } | null;
}

export interface ChatMessage {
  id: number;
  role: ChatRole;
  by?: string;
  text: string;
  created: string;
  turn?: string;
  /** A person's: paths in the video's folder (v0/images/x.png). A question's: URLs. */
  images?: string[];
  voice?: string;
  options?: ChatOption[];
  answer?: string;
  answer_to?: number;
  anchor?: { file: string; line: number; heading?: string } | null;
  streaming?: boolean;
  waiting?: boolean;
}

export interface ChatTurn {
  id: string;
  state: 'queued' | 'running' | 'stopping';
  agent?: string;
}

export interface ChatSent {
  file: string;
  sha256: string;
  note: number;
  target: string;
  at: number;
  /** Its comment is open again with no task running it: a Send resends it. */
  open: boolean;
}

export interface ChatView {
  messages: ChatMessage[];
  turn: ChatTurn | null;
  busy: string;
  offline: boolean;
  blocked: string;
  sent: ChatSent | null;
}

export const EMPTY: ChatView = { messages: [], turn: null, busy: '', offline: false, blocked: '', sent: null };

const ROLES: readonly string[] = ['person', 'agent', 'tool', 'question', 'info'];
const str = (v: unknown): string => (typeof v === 'string' ? v : '');

/** A message from the server, made safe to render; null when it isn't one. */
export function message(v: unknown): ChatMessage | null {
  const m = v as Partial<ChatMessage> | null;
  if (!m || typeof m !== 'object' || typeof m.id !== 'number' || !ROLES.includes(m.role as string)) return null;
  const strings = (a: unknown): string[] =>
    Array.isArray(a) ? a.filter((x): x is string => typeof x === 'string') : [];
  const options = Array.isArray(m.options)
    ? m.options
        .filter((o): o is ChatOption => !!o && typeof o.text === 'string')
        .map((o) => (o.image?.url ? { text: o.text, image: { url: o.image.url } } : { text: o.text }))
    : [];
  return {
    id: m.id,
    role: m.role as ChatRole,
    by: str(m.by),
    text: str(m.text),
    created: str(m.created),
    turn: str(m.turn),
    images: strings(m.images),
    voice: str(m.voice),
    options,
    answer: str(m.answer),
    ...(typeof m.answer_to === 'number' ? { answer_to: m.answer_to } : {}),
    anchor: m.anchor && typeof m.anchor.file === 'string' ? m.anchor : null,
    streaming: m.streaming === true,
    waiting: m.waiting === true,
  };
}

function turn(v: unknown): ChatTurn | null {
  const t = v as Partial<ChatTurn> | null;
  if (!t || typeof t.id !== 'string' || !['queued', 'running', 'stopping'].includes(t.state as string)) return null;
  return { id: t.id, state: t.state as ChatTurn['state'], agent: str(t.agent) };
}

/** GET api/chat's body, made safe to render. */
export function normalizeView(body: unknown): ChatView {
  const b = (body ?? {}) as Record<string, unknown>;
  const s = b.sent as Partial<ChatSent> | null;
  return {
    messages: (Array.isArray(b.messages) ? b.messages : []).map(message).filter((m): m is ChatMessage => !!m),
    turn: turn(b.turn),
    busy: str(b.busy),
    offline: b.offline === true,
    blocked: str(b.blocked),
    sent:
      s && typeof s === 'object' && typeof s.sha256 === 'string'
        ? {
            file: str(s.file),
            sha256: s.sha256,
            note: typeof s.note === 'number' ? s.note : 0,
            target: str(s.target),
            at: typeof s.at === 'number' ? s.at : 0,
            open: s.open === true,
          }
        : null,
  };
}

/** UTF-16 length, as the server counts a text event's `at`. */
const len = (s: string): number => s.length;

/**
 * A `chat` event applied to the thread: a message added or changed, words added to one, the turn. Null when the
 * page must read the thread again (words at an offset it doesn't have: it missed some).
 */
export function applyEvent(view: ChatView, ev: unknown): ChatView | null {
  const e = (ev ?? {}) as {
    why?: unknown;
    message?: unknown;
    id?: unknown;
    at?: unknown;
    text?: unknown;
    turn?: unknown;
  };
  switch (e.why) {
    case 'message': {
      const m = message(e.message);
      if (!m) return view;
      const i = view.messages.findIndex((x) => x.id === m.id);
      const messages = [...view.messages];
      if (i >= 0) {
        // Words that came by text events while this was on its way are kept: the longer text wins.
        const old = messages[i]!;
        messages[i] =
          m.streaming && len(old.text) > len(m.text) && old.text.startsWith(m.text) ? { ...m, text: old.text } : m;
      } else {
        const at = messages.findIndex((x) => x.id > m.id);
        if (at < 0) messages.push(m);
        else messages.splice(at, 0, m);
      }
      return { ...view, messages };
    }
    case 'text': {
      if (typeof e.id !== 'number' || typeof e.at !== 'number' || typeof e.text !== 'string') return view;
      const i = view.messages.findIndex((x) => x.id === e.id);
      if (i < 0) return null;
      const m = view.messages[i]!;
      const have = len(m.text);
      if (e.at + len(e.text) <= have) return view; // had them already
      if (e.at !== have) return null;
      const messages = [...view.messages];
      messages[i] = { ...m, text: m.text + e.text };
      return { ...view, messages };
    }
    case 'turn':
      return { ...view, turn: turn(e.turn), ...(e.turn ? {} : { busy: '', offline: false }) };
  }
  return view;
}

/** The newest question that still offers its choices: one without an answer, and nothing the person said since. */
export function openQuestion(view: ChatView): number | null {
  for (let i = view.messages.length - 1; i >= 0; i--) {
    const m = view.messages[i]!;
    if (m.role === 'person') return null;
    if (m.role === 'question') return m.answer ? null : m.id;
  }
  return null;
}

/**
 * The quiet line under the thread, or "": what the agent is doing for the chat ("Replying…"), why it waits (busy with
 * an edit, the computer offline, the local worker blocked), or the build a Send started ("Building v1…").
 */
export function statusLine(
  view: ChatView,
  a: AutomationState | null,
  o: { cuts: readonly string[]; online: boolean },
): string {
  const t = view.turn;
  if (t) {
    if (view.blocked && t.state === 'queued') return view.blocked;
    if (view.busy) return view.busy;
    if (view.offline || (!o.online && t.state === 'queued')) return 'The editing computer is offline';
    if (t.state === 'stopping') return 'Stopping…';
    if (t.state === 'queued') return 'Waiting for the agent…';
    const last = view.messages.at(-1);
    return last?.role === 'agent' && last.streaming ? '' : 'Working…';
  }
  const task = (a?.tasks ?? []).find((j) => j.state === 'running' || j.state === 'stopping' || j.state === 'queued');
  if (task?.target) {
    const verb = o.cuts.includes(task.target) ? 'Updating' : 'Building';
    if (task.state === 'queued' && !o.online) return `${verb} ${task.target} once the editing computer is online`;
    return `${verb} ${task.target}…`;
  }
  return '';
}

/** Send on the chat's version: whether it does anything, and why not. */
export function sendState(view: ChatView, doc: { path: string; sha256: string } | null): { ok: boolean; why: string } {
  if (!doc) return { ok: false, why: 'The script isn’t loaded yet' };
  const s = view.sent;
  if (s && s.file === doc.path && s.sha256 === doc.sha256 && !s.open)
    return { ok: false, why: 'Nothing changed since the last Send' };
  return { ok: true, why: '' };
}

/** A name for a pasted or picked image in v0/images/: its own (cleaned), else `pasted-<stamp>`, with -2, -3… when
 * taken. */
export function imageName(file: { name?: string; type?: string }, stamp: string, n: number): string {
  const ext =
    { 'image/png': '.png', 'image/jpeg': '.jpg', 'image/webp': '.webp', 'image/gif': '.gif' }[file.type ?? ''] ??
    (/\.(png|jpe?g|webp|gif)$/i.exec(file.name ?? '')?.[0].toLowerCase() || '.png');
  const base =
    (file.name ?? '')
      .replace(/\.[^.]*$/, '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 40) || `pasted-${stamp}`;
  return `${base === 'image' ? `pasted-${stamp}` : base}${n > 1 ? `-${n}` : ''}${ext}`;
}

/** yyyymmdd-hhmmss, local time. */
export function stampOf(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
}

/** The document among v0's files: `<folder>_v0.md`, else the first `*_v0.md` (spec/scripts.md). */
export function pickDoc(paths: readonly string[], folder: string): string | null {
  const docs = paths.filter((p) => /^v0\/[^/]+_v0\.md$/.test(p)).sort();
  return docs.find((p) => p === `v0/${folder}_v0.md`) ?? docs[0] ?? null;
}
