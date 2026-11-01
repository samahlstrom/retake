import { describe, expect, it } from 'vitest';
import type { AutomationState, EditingTask, EditingWorker } from './automation.svelte';
import {
  activeTask,
  agentStatus,
  commentStatus,
  dispatcher,
  pausedReason,
  sendButton,
  statusText,
  unsent,
  workerOnline,
  type AgentStatus,
  type SendInput,
} from './status';

const task = (state: string, over: Partial<EditingTask> = {}): EditingTask => ({
  id: 't1',
  notes: [1],
  version: 'v6',
  state,
  agent: 'claude',
  worker: 'pc',
  attempt: 1,
  events: [],
  ...over,
});
const note = (mark: 'answered' | 'question' | 'open' = 'open') => ({ mark, kind: 'note' as const });

describe('comment status: four states the reviewer sees', () => {
  it('no task is Open', () => {
    expect(commentStatus(note())).toEqual({ state: 'open', paused: null });
  });
  it('queued, running and stopping are Working', () => {
    for (const s of ['queued', 'running', 'stopping'])
      expect(commentStatus(note(), task(s))).toEqual({ state: 'working', paused: null });
  });
  it('a question is Question, from the task or from the notes (MCP)', () => {
    expect(commentStatus(note(), task('question')).state).toBe('question');
    expect(commentStatus(note('question')).state).toBe('question');
    expect(commentStatus({ mark: 'question', kind: 'question' }).state).toBe('question');
  });
  it('completed, answered and accepted are Done', () => {
    expect(commentStatus(note(), task('completed')).state).toBe('done');
    expect(commentStatus(note('answered')).state).toBe('done');
    expect(commentStatus(note('answered'), task('running')).state).toBe('done');
  });
  it('paused is Open, with the reason once', () => {
    expect(commentStatus(note(), task('paused', { error: 'Usage limit reached for Claude Code' }))).toEqual({
      state: 'open',
      paused: 'Paused: usage limit',
    });
  });
  it('a cancelled task leaves the comment Open, and sendable again', () => {
    expect(commentStatus(note(), task('cancelled'))).toEqual({ state: 'open', paused: null });
    expect(unsent(note(), task('cancelled'))).toBe(true);
    expect(activeTask(task('cancelled'))).toBeNull();
  });
});

describe('paused reasons are short and plain', () => {
  it.each([
    ['', 'Paused'],
    ['Usage limit reached; try again at 3pm', 'Paused: usage limit'],
    ['rate limit exceeded', 'Paused: usage limit'],
    ['Worker disconnected; retry or switch agent', 'Paused: computer offline'],
    ['The render failed: retake-edit exited 1', 'Paused: render failed'],
    ['Agent crashed. See the log', 'Paused: agent crashed'],
    [
      'Something went very wrong in a way that takes far too many words to say here',
      'Paused: something went very wrong in a way that…',
    ],
  ])('%j → %j', (error, text) => {
    expect(pausedReason({ error })).toBe(text);
  });
});

describe('unsent notes are what Send N counts', () => {
  it('open notes without a live task', () => {
    expect(unsent(note())).toBe(true);
    expect(unsent(note(), task('queued'))).toBe(false);
    expect(unsent(note(), task('paused'))).toBe(false);
    expect(unsent(note('answered'))).toBe(false);
    expect(unsent(note('question'))).toBe(false);
    expect(unsent({ mark: 'open', kind: 'question' })).toBe(false);
  });
});

const worker = (over: Partial<EditingWorker> = {}): EditingWorker => ({
  id: 'pc',
  name: 'sam-pc',
  seen: 1000,
  agents: [
    { id: 'claude', name: 'Claude Code' },
    { id: 'codex', name: 'Codex' },
  ],
  ...over,
});
const automation = (over: Partial<AutomationState> = {}): AutomationState => ({
  config: { enabled: false, worker: 'pc', agent: 'codex' },
  workers: [worker()],
  tasks: [],
  renders: [],
  ...over,
});

describe('the agent and its computer', () => {
  it('online from the server (C5) or a poll in the last 10 s', () => {
    expect(workerOnline(worker({ seen: 995 }), 1000)).toBe(true);
    expect(workerOnline(worker({ seen: 980 }), 1000)).toBe(false);
    expect(workerOnline(worker({ seen: 0, online: true }), 1000)).toBe(true);
    expect(workerOnline(worker({ seen: 1000, online: false }), 1000)).toBe(false);
  });
  it('names the configured agent on its machine', () => {
    const s = agentStatus(automation(), 1001);
    expect(s).toMatchObject({ available: true, local: null, online: true, agent: 'Codex', working: false });
    expect(s.worker?.name).toBe('sam-pc');
    expect(statusText(s, true)).toBe('Codex');
  });
  it('falls back to an online machine and its first agent when nothing is configured', () => {
    const s = agentStatus(automation({ config: { enabled: false, worker: '', agent: '' } }), 1001);
    expect(s).toMatchObject({ online: true, agent: 'Claude Code' });
  });
  it('says working while a task runs, and whether auto-send is on (C2)', () => {
    const s = agentStatus(
      automation({ tasks: [task('running')], config: { enabled: true, worker: 'pc', agent: 'claude' } }),
      1001,
    );
    expect(s).toMatchObject({ working: true, auto: true });
  });
  it('reads local (C1) and the local blocked reason', () => {
    const s = agentStatus(automation({ local: true, blocked: 'Install Claude Code or Codex to edit' }), 1001);
    expect(s).toMatchObject({ local: true, blocked: 'Install Claude Code or Codex to edit' });
    expect(statusText(s, true)).toBe('Agent unavailable');
  });
  it('no automation route: nothing to show', () => {
    expect(agentStatus(null, 0).available).toBe(false);
  });
  it('words for no computer, offline, and other people', () => {
    const none = agentStatus(automation({ workers: [], local: false }), 1001);
    expect(statusText(none, true)).toBe('Connect computer');
    expect(statusText(none, false)).toBe('No agent');
    const off = agentStatus(automation({ local: false }), 5000);
    expect(off.online).toBe(false);
    expect(statusText(off, true)).toBe('Computer offline');
    expect(statusText(off, false)).toBe('Agent offline');
  });
});

describe('only the owner sends', () => {
  it('owner, or an older server without roles', () => {
    expect(dispatcher('owner', true)).toBe(true);
    expect(dispatcher(null, true)).toBe(true);
    expect(dispatcher('reviewer', true)).toBe(false);
    expect(dispatcher('approver', true)).toBe(false);
    expect(dispatcher('viewer', false)).toBe(false);
  });
});

describe('the bar button', () => {
  const online: AgentStatus = agentStatus(automation({ local: true }), 1001);
  const hostedNone: AgentStatus = agentStatus(automation({ workers: [], local: false }), 1001);
  const oldNone: AgentStatus = agentStatus(automation({ workers: [] }), 1001);
  const base: SendInput = {
    role: 'owner',
    canRecord: true,
    status: online,
    takeOpen: false,
    unsent: 0,
    finishing: false,
    saved: false,
    sent: false,
  };
  const b = (over: Partial<SendInput>) => sendButton({ ...base, ...over });

  it('Send N to agent counts the open comments not yet sent', () => {
    expect(b({ unsent: 12 })).toEqual({ label: 'Send 12 to agent', action: 'send', primary: true });
    expect(b({ unsent: 1 }).label).toBe('Send 1 to agent');
  });
  it('a take in progress sends even before its notes are transcribed', () => {
    expect(b({ takeOpen: true })).toEqual({ label: 'Send to agent', action: 'send', primary: true });
    expect(b({ takeOpen: true, unsent: 3 }).label).toBe('Send 3 to agent');
  });
  it('nothing new: says Sent after a send, explains on a click instead of failing', () => {
    expect(b({ sent: true, saved: true })).toEqual({ label: 'Sent', action: 'none', primary: true });
    expect(b({})).toEqual({ label: 'Send to agent', action: 'none', primary: true });
  });
  it('Sending… while finishing', () => {
    expect(b({ finishing: true, takeOpen: true })).toMatchObject({ label: 'Sending…', action: 'none' });
  });
  it('no computer (hosted): the same button opens the way to connect one', () => {
    expect(b({ status: hostedNone, unsent: 4 })).toEqual({
      label: 'Send 4 to agent',
      action: 'connect',
      primary: true,
    });
    expect(b({ status: hostedNone, takeOpen: true })).toMatchObject({ action: 'connect' });
    expect(b({ status: hostedNone, saved: true })).toMatchObject({ label: 'Saved', action: 'connect' });
    expect(b({ status: hostedNone, finishing: true, takeOpen: true }).label).toBe('Saving…');
  });
  it('local mode that cannot edit finishes; the status next to it says why', () => {
    const blocked = agentStatus(
      automation({ local: true, blocked: 'No agent found. Install Claude Code or Codex.' }),
      1001,
    );
    expect(b({ status: blocked, unsent: 2, takeOpen: true })).toEqual({
      label: 'Finish review',
      action: 'finish',
      primary: false,
    });
    expect(statusText(blocked, true)).toBe('Agent unavailable');
  });
  it('an older server with no worker keeps Finish review', () => {
    expect(b({ status: oldNone, takeOpen: true })).toEqual({
      label: 'Finish review',
      action: 'finish',
      primary: false,
    });
    expect(b({ status: oldNone, saved: true })).toMatchObject({ label: 'Saved', action: 'finish' });
  });
  it('no automation at all keeps Finish review', () => {
    expect(b({ status: agentStatus(null, 0), takeOpen: true })).toMatchObject({
      label: 'Finish review',
      action: 'finish',
    });
  });
  it('reviewers finish; the owner (or auto-send) takes it from there', () => {
    expect(b({ role: 'reviewer', unsent: 5, takeOpen: true })).toEqual({
      label: 'Finish review',
      action: 'finish',
      primary: false,
    });
    expect(b({ role: 'reviewer', saved: true })).toMatchObject({ label: 'Saved' });
    expect(b({ role: 'reviewer', finishing: true })).toMatchObject({ label: 'Saving…' });
  });
});
