// What a reviewer sees of the agent: the four comment states, the one primary button in the bar, and the agent's
// status next to it. Pure, so it is unit-tested; the components only draw what these return.
import type { AutomationState, EditingTask, EditingWorker } from './automation.svelte';
import type { Role } from './types';

/** A comment is Open, then Working once sent, then Done; Working can turn into a Question until you answer. */
export type CommentState = 'open' | 'working' | 'question' | 'done';
export const STATE_TEXT: Record<CommentState, string> = {
  open: 'Open',
  working: 'Working',
  question: 'Question',
  done: 'Done',
};

export interface CommentStatus {
  state: CommentState;
  /** A paused task (usage limit, offline machine, a pause by the owner): why, in a few words. It shows as Open. */
  paused: string | null;
}

/** The task that owns a comment, if it is still running or waiting (a finished or cancelled one owns nothing). */
export const activeTask = (task: EditingTask | null | undefined): EditingTask | null =>
  task && task.state !== 'completed' && task.state !== 'cancelled' ? task : null;

/** "Paused: usage limit": the reason a task paused, short, from the user's side. */
export function pausedReason(task: Pick<EditingTask, 'error'>): string {
  const e = (task.error ?? '').trim();
  if (!e) return 'Paused';
  const low = e.toLowerCase();
  if (low.includes('usage limit') || low.includes('rate limit') || low.includes('quota')) return 'Paused: usage limit';
  if (low.includes('disconnect') || low.includes('offline')) return 'Paused: computer offline';
  if (low.includes('render')) return 'Paused: render failed';
  const first = e.split(/[;.\n]/)[0]!.trim();
  const cut = first.slice(0, 44);
  const short = first.length > 44 ? `${cut.slice(0, cut.lastIndexOf(' ') > 20 ? cut.lastIndexOf(' ') : 43)}…` : first;
  return `Paused: ${short.charAt(0).toLowerCase()}${short.slice(1)}`;
}

/**
 * The state a comment shows. `mark` is the note's own state on the server (answered and accepted are Done), `task`
 * the editing task that owns it, if any. A paused task shows as Open with its reason (it is not the reviewer's job to
 * understand task states); a cancelled or completed-but-reopened one is just Open again.
 */
export function commentStatus(
  row: { mark: 'answered' | 'question' | 'open'; kind: 'note' | 'question' },
  task?: EditingTask | null,
): CommentStatus {
  if (row.mark === 'answered') return { state: 'done', paused: null };
  switch (task?.state) {
    case 'completed':
      return row.mark === 'question' ? { state: 'question', paused: null } : { state: 'done', paused: null };
    case 'question':
      return { state: 'question', paused: null };
    case 'queued':
    case 'running':
    case 'stopping':
      return { state: 'working', paused: null };
    case 'paused':
      return { state: 'open', paused: pausedReason(task) };
  }
  if (row.mark === 'question') return { state: 'question', paused: null };
  return { state: 'open', paused: null };
}

/** A note nobody has sent yet: open, a note (not an agent's question), and no task waiting or running on it. */
export const unsent = (row: { mark: string; kind: string }, task?: EditingTask | null): boolean =>
  row.kind === 'note' && row.mark === 'open' && !activeTask(task);

// ---------------------------------------------------------------- the agent and its machine

/** A worker counts as online when it said so (C5's `online`), or polled in the last 10 s. */
export const ONLINE_S = 10;
export function workerOnline(w: EditingWorker, now: number): boolean {
  if (typeof w.online === 'boolean') return w.online;
  return now - w.seen < ONLINE_S;
}

export interface AgentStatus {
  /** The automation route answered (an older server without it: no agent UI at all). */
  available: boolean;
  /** `local` from GET api/automation (C1): true = the worker runs inside this server, false = hosted, null = an
   * older server that doesn't say. */
  local: boolean | null;
  /** The machine edits go to: the configured one, else the first online one. */
  worker: EditingWorker | null;
  online: boolean;
  /** The agent's display name ("Claude Code"), or "". */
  agent: string;
  /** A task is running or stopping right now. */
  working: boolean;
  /** Local mode only: why the in-process worker can't edit (no agent installed, no timeline…), or "". */
  blocked: string;
  /** "Send other reviewers' comments automatically". */
  auto: boolean;
}

export function agentStatus(a: AutomationState | null, now: number): AgentStatus {
  if (!a) {
    return {
      available: false,
      local: null,
      worker: null,
      online: false,
      agent: '',
      working: false,
      blocked: '',
      auto: false,
    };
  }
  const workers = a.workers ?? [];
  const configured = workers.find((w) => w.id === a.config?.worker) ?? null;
  const worker =
    configured && workerOnline(configured, now)
      ? configured
      : (workers.find((w) => workerOnline(w, now)) ?? configured ?? workers[0] ?? null);
  const online = !!worker && workerOnline(worker, now);
  const agents = worker?.agents ?? [];
  const agent = agents.find((p) => p.id === a.config?.agent && worker?.id === a.config?.worker) ?? agents[0] ?? null;
  const blocked = (typeof a.blocked === 'string' && a.blocked) || worker?.blocked || '';
  return {
    available: true,
    local: typeof a.local === 'boolean' ? a.local : null,
    worker,
    online,
    agent: agent?.name ?? '',
    working: (a.tasks ?? []).some((t) => t.state === 'running' || t.state === 'stopping'),
    blocked,
    auto: !!a.config?.enabled,
  };
}

/** What the agent status next to the button says. */
export function statusText(s: AgentStatus, owner: boolean): string {
  if (s.online && !s.blocked) return s.agent || 'Agent';
  if (s.local && s.blocked) return 'Agent unavailable';
  if (s.worker) return owner ? 'Computer offline' : 'Agent offline';
  return owner ? 'Connect computer' : 'No agent';
}

// ---------------------------------------------------------------- the primary button

export interface SendInput {
  role: Role | null;
  canRecord: boolean;
  status: AgentStatus;
  /** The reviewer recorded something this take and hasn't finished it yet. */
  takeOpen: boolean;
  /** Open notes not yet sent to the agent. */
  unsent: number;
  finishing: boolean;
  /** The last action finished this take (`doneSaved`), and whether it also sent it. */
  saved: boolean;
  sent: boolean;
}

/**
 * - `send`: finish the take with `send: true` and submit the other open notes.
 * - `finish`: finish the take (the reviewer's own job; the owner or auto-send takes it from there).
 * - `connect`: there is no machine to send to: finish the take if one is open, then show how to connect one.
 * - `none`: nothing new to send; say so.
 */
export type SendAction = 'send' | 'finish' | 'connect' | 'none';

export interface SendButton {
  label: string;
  action: SendAction;
  /** It's the dispatcher's Send (filled) rather than a reviewer's Finish. */
  primary: boolean;
}

/** Only the owner sends (C2: other reviewers' finished reviews go automatically, when the owner turns that on). An
 * older server without roles is the owner's own `retake open`. */
export const dispatcher = (role: Role | null, canRecord: boolean): boolean =>
  canRecord && (role === null || role === 'owner');

export function sendButton(i: SendInput): SendButton {
  const s = i.status;
  const canSend = s.available && s.online && !s.blocked;
  // A server that says where the worker runs (C1) gets the Send button even with nothing connected: it opens the way
  // to connect one. An older server that doesn't say keeps the old Finish review until a worker shows up.
  const sends = dispatcher(i.role, i.canRecord) && (canSend || (s.available && s.local !== null));
  if (!sends) {
    if (i.finishing) return { label: 'Saving…', action: 'finish', primary: false };
    return { label: i.saved && !i.takeOpen ? 'Saved' : 'Finish review', action: 'finish', primary: false };
  }
  if (i.finishing) return { label: canSend ? 'Sending…' : 'Saving…', action: 'none', primary: true };
  const label = i.unsent > 0 ? `Send ${i.unsent} to agent` : 'Send to agent';
  if (!canSend) {
    if (!i.takeOpen && !i.unsent && i.saved) return { label: 'Saved', action: 'connect', primary: true };
    return { label, action: 'connect', primary: true };
  }
  if (i.takeOpen || i.unsent > 0) return { label, action: 'send', primary: true };
  return { label: i.sent ? 'Sent' : i.saved ? 'Saved' : label, action: 'none', primary: true };
}
