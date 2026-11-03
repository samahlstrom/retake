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
  /** A Working comment that hasn't started: why, in one short line ("Waiting for the editing computer"). */
  waiting: string | null;
  /** The one comment the agent is on now (spec/automation.md, "While the agent works"): first in To do, with the
   * Working dot, the sweep and its line. Every other Working comment reads quietly (only its `→ v5`). */
  now?: boolean;
  /** The line under the comment the agent is on: its words ("" with none), or "Putting it in v5…". Set only with
   * `now`: one at a time. */
  doing?: string | null;
  /** Done by the agent's mark, its edit in the live version before the report: what changed, in its words. */
  change?: string | null;
}

/** A task's notes still to do: not finished in an earlier attempt, nor dropped from the batch. */
export const pendingNotes = (task: Pick<EditingTask, 'notes' | 'done' | 'dropped'>): number[] =>
  task.notes.filter((id) => !task.done?.includes(id) && !task.dropped?.includes(id));

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

/** What the page knows besides the comment and its task: its take was sent and is still being processed (the
 * server's `sending`), and the agent's status (for the waiting line). */
export interface StatusContext {
  sending?: boolean;
  agent?: AgentStatus;
}

/**
 * The state a comment shows. `mark` is the note's own state on the server (answered and accepted are Done), `task`
 * the editing task that owns it, if any. A paused task shows as Open with its reason (it is not the reviewer's job to
 * understand task states); a cancelled or completed-but-reopened one is just Open again. A note of a take that was
 * sent and is still being transcribed is Working already: it has no task yet, but it is on its way.
 */
export function commentStatus(
  row: { mark: 'answered' | 'question' | 'open'; kind: 'note' | 'question'; id?: number },
  task?: EditingTask | null,
  ctx: StatusContext = {},
): CommentStatus {
  const is = (state: CommentState, paused: string | null = null): CommentStatus => ({ state, paused, waiting: null });
  if (row.mark === 'answered') return is('done');
  switch (task?.state) {
    case 'completed':
      return row.mark === 'question' ? is('question') : is('done');
    case 'question':
      return is('question');
    case 'queued':
      return { state: 'working', paused: null, waiting: waitingLine(ctx.agent) };
    case 'running':
    case 'stopping':
      // The agent asked about it from inside the task and works on: the question is the reviewer's to answer.
      return row.mark === 'question' ? is('question') : progress(row, task, ctx);
    case 'paused':
      return is('open', pausedReason(task));
  }
  if (row.mark === 'question') return is('question');
  if (ctx.sending && row.kind === 'note' && !activeTask(task))
    return { state: 'working', paused: null, waiting: waitingLine(ctx.agent) };
  return is('open');
}

/** The one note a running task's agent is on (spec/automation.md, "While the agent works"): its `working` note, else
 * the last note it marked done whose edit is still being put in, else none. */
export function nowNote(task: Pick<EditingTask, 'working' | 'marked'>): number | null {
  if (task.working && !task.marked?.some((m) => m.note === task.working!.note && m.ready)) return task.working.note;
  return task.marked?.findLast((m) => !m.ready)?.note ?? null;
}

/** A comment of a running task, as the agent marks it (spec/automation.md, "Marking progress"): Done once its edit is in
 * the live version; the one the agent is on (`now`) Working with its line, the agent's words or "Putting it in v5…";
 * the rest plain Working, which the list draws quietly. While the editing computer is offline the one being worked
 * on says it's reconnecting. */
function progress(row: { kind: string; id?: number }, task: EditingTask, ctx: StatusContext): CommentStatus {
  const working: CommentStatus = { state: 'working', paused: null, waiting: null };
  if (row.kind !== 'note' || row.id == null) return working;
  const line = (text: string) => (ctx.agent?.available && !ctx.agent.online ? 'Reconnecting to the agent' : text);
  const mark = task.marked?.find((m) => m.note === row.id);
  if (mark?.ready) return { state: 'done', paused: null, waiting: null, change: mark.change.trim() || null };
  if (nowNote(task) !== row.id) return working;
  if (task.working?.note === row.id) return { ...working, now: true, doing: line((task.working.text ?? '').trim()) };
  return { ...working, now: true, doing: line(task.target ? `Putting it in ${task.target}…` : 'Putting it in…') };
}

/** The one quiet line at the top of To do while the agent works on the list's comments and hasn't marked any yet (an
 * agent that doesn't use `retake working`): "The agent is on it · 15 comments". `owners` is the task of each of the
 * list's Working comments. Null with none running, and as soon as its task has a mark (working or done). */
export function onItLine(owners: readonly (EditingTask | null | undefined)[]): string | null {
  const running = owners.filter((t): t is EditingTask => t?.state === 'running' || t?.state === 'stopping');
  if (!running.length || running.some((t) => t.working || t.marked?.length)) return null;
  return `The agent is on it · ${running.length === 1 ? '1 comment' : `${running.length} comments`}`;
}

/** The version a running task of this video makes or updates: the live one (spec/automation.md, "The live version"). */
export const liveTarget = (tasks: readonly EditingTask[] | null | undefined): string | null =>
  (tasks ?? []).find((t) => t.state === 'running' || t.state === 'stopping')?.target ?? null;

/** Why sent work hasn't started, when it's the computer's fault: none is online (hosted), or the built-in one can't
 * edit (local mode, `blocked`). A queue behind another task needs no words. */
export function waitingLine(s: AgentStatus | undefined): string | null {
  if (!s?.available) return null;
  if (s.local && s.blocked) return 'Waiting for the agent';
  if (!s.online) return 'Waiting for the editing computer';
  return null;
}

/** A note Send sends: open, a note (not an agent's question), no task waiting or running on it (a paused one counts:
 * Send resumes it, spec/automation.md "Send resumes a paused batch"), and its take isn't on its way already
 * (`sending`). */
export const unsent = (row: { mark: string; kind: string }, task?: EditingTask | null, sending = false): boolean =>
  row.kind === 'note' && row.mark === 'open' && (!activeTask(task) || task?.state === 'paused') && !sending;

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
  /** The comments the agent works on now (0 when none runs), and the ones sent meanwhile that wait to join that same
   * working version (spec/automation.md, "Batches"). */
  batch: number;
  next: number;
  /** Local mode only: why the in-process worker can't edit (no agent installed, no timeline…), or "". */
  blocked: string;
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
      batch: 0,
      next: 0,
      blocked: '',
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
  const tasks = a.tasks ?? [];
  const running = tasks.find((t) => t.state === 'running' || t.state === 'stopping');
  // Sent meanwhile for the same version (its target): on their way into the agent's work.
  const queued = tasks.filter((t) => t.state === 'queued' && t.target === running?.target);
  return {
    available: true,
    local: typeof a.local === 'boolean' ? a.local : null,
    worker,
    online,
    agent: agent?.name ?? '',
    working: !!running,
    batch: running ? pendingNotes(running).length : 0,
    next: running ? queued.reduce((n, t) => n + pendingNotes(t).length, 0) : 0,
    blocked,
  };
}

/** The one quiet line over Send when it can't reach an agent (spec/automation.md, "No agent menu in the bar"): the
 * local worker's `blocked` reason; on a hosted project with no computer online, for the owner, that one isn't
 * connected (with a link to Settings), and for anyone else that sends still queue. Nothing when Send works. */
export interface SendNotice {
  text: string;
  /** The owner can fix it in Settings (connect a computer). */
  settings: boolean;
}
export function sendNotice(s: AgentStatus, owner: boolean): SendNotice | null {
  if (!s.available) return null;
  if (s.local && s.blocked) return { text: s.blocked, settings: false };
  if (s.online || s.local) return null;
  if (!owner) return { text: 'The editing computer is offline. Send still queues.', settings: false };
  return { text: s.worker ? 'The editing computer is offline' : 'No editing computer connected', settings: true };
}

// ---------------------------------------------------------------- the primary button

export interface SendInput {
  /** Kept for older callers; the button is the same for everyone who can record. */
  owner?: boolean;
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
 * - `send`: finish the take with `send: true` and submit the other open notes. With no computer online they queue
 *   and start when one connects.
 * - `save`: local mode whose built-in worker can't edit (`blocked`): finish the take without sending it. The notice
 *   over Send says what's missing.
 * - `none`: nothing new to send; say so.
 */
export type SendAction = 'send' | 'save' | 'none';

export interface SendButton {
  /** What it says: Send, Sending…, Saving…, Sent or Saved. Never an agent's name, never a count. */
  label: string;
  action: SendAction;
  /** Its tooltip and accessible name: what a press does, with the count ("Send 3 comments to the agent"). */
  title: string;
  /** It would do nothing (a chat's script unchanged since the last Send): dimmed, and a press says `title`. */
  disabled?: boolean;
}

/** Runs the agent: the owner, or an older server without roles (the owner's own `retake open`). Everyone who can
 * record sends; only this one changes the agent and connects computers (in Settings). */
export const managesAgent = (role: Role | null): boolean => role === null || role === 'owner';

/** The bar's one button, the same for everyone who can record (a viewer has none). */
export function sendButton(i: SendInput): SendButton {
  const s = i.status;
  const blocked = !!(s.local && s.blocked);
  const n = i.unsent;
  const what =
    n === 1 ? 'Send 1 comment to the agent' : n > 1 ? `Send ${n} comments to the agent` : 'Send to the agent';
  if (i.finishing) {
    const label = blocked ? 'Saving…' : 'Sending…';
    return { label, action: 'none', title: label };
  }
  if (blocked) return { label: i.saved && !i.takeOpen ? 'Saved' : 'Send', action: 'save', title: `Save. ${s.blocked}` };
  if (i.takeOpen || n > 0) return { label: 'Send', action: 'send', title: what };
  return { label: i.sent ? 'Sent' : i.saved ? 'Saved' : 'Send', action: 'none', title: 'Nothing new to send' };
}
