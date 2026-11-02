import { describe, expect, it } from 'vitest';
import type { AutomationState, EditingTask, EditingWorker } from './automation.svelte';
import {
  activeTask,
  agentStatus,
  batchLine,
  commentStatus,
  managesAgent,
  pausedReason,
  pendingNotes,
  sendButton,
  statusText,
  unsent,
  waitingLine,
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
    expect(commentStatus(note())).toEqual({ state: 'open', paused: null, waiting: null });
  });
  it('queued, running and stopping are Working', () => {
    for (const s of ['queued', 'running', 'stopping'])
      expect(commentStatus(note(), task(s))).toEqual({ state: 'working', paused: null, waiting: null });
  });
  it('a question the agent asked from inside its running task is Question while it works on', () => {
    expect(commentStatus(note('question'), task('running')).state).toBe('question');
    expect(commentStatus(note('question'), task('stopping')).state).toBe('question');
    expect(commentStatus(note('open'), task('running')).state).toBe('working'); // answered: Working again
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
      waiting: null,
    });
  });
  it('a cancelled task leaves the comment Open, and sendable again', () => {
    expect(commentStatus(note(), task('cancelled'))).toEqual({ state: 'open', paused: null, waiting: null });
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
  it('says working while a task runs', () => {
    const s = agentStatus(
      automation({ tasks: [task('running')], config: { enabled: true, worker: 'pc', agent: 'claude' } }),
      1001,
    );
    expect(s).toMatchObject({ working: true });
  });
  it('a batch is one group: the comments the agent works on, and the ones on their way into the same version', () => {
    const running = task('running', { notes: [1, 2, 3, 4], done: [1], dropped: [4] });
    expect(pendingNotes(running)).toEqual([2, 3]);
    const s = agentStatus(automation({ tasks: [running, task('queued', { id: 't2', notes: [5, 6] })] }), 1001);
    expect(s).toMatchObject({ working: true, batch: 2, next: 2 });
    expect(batchLine(s)).toBe('Agent working on 2 comments · 2 more coming');
    expect(batchLine({ ...s, batch: 1, next: 0 })).toBe('Agent working on 1 comment');
    // Nothing runs: no line, however much waits (the comments say Working on their own).
    const waiting = agentStatus(automation({ tasks: [task('queued', { notes: [5, 6] })] }), 1001);
    expect(waiting).toMatchObject({ working: false, batch: 0, next: 0 });
    expect(batchLine(waiting)).toBeNull();
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
    expect(statusText(none, false)).toBe('Agent offline');
    const off = agentStatus(automation({ local: false }), 5000);
    expect(off.online).toBe(false);
    expect(statusText(off, true)).toBe('Computer offline');
    expect(statusText(off, false)).toBe('Agent offline');
  });
});

describe('only the owner runs the agent; everyone who records sends', () => {
  it('the owner, or an older server without roles, chooses the agent and connects computers', () => {
    expect(managesAgent('owner')).toBe(true);
    expect(managesAgent(null)).toBe(true);
    expect(managesAgent('reviewer')).toBe(false);
    expect(managesAgent('approver')).toBe(false);
    expect(managesAgent('viewer')).toBe(false);
  });
});

const online: AgentStatus = agentStatus(automation({ local: true }), 1001);
const hostedNone: AgentStatus = agentStatus(automation({ workers: [], local: false }), 1001);
const hostedOff: AgentStatus = agentStatus(automation({ local: false }), 5000);
const blocked: AgentStatus = agentStatus(
  automation({ local: true, blocked: 'No agent found. Install Claude Code or Codex.' }),
  1001,
);
const unknown: AgentStatus = agentStatus(null, 0);

describe('sent comments are Working, and say when they wait for the computer', () => {
  it('a sent take still being transcribed is Working before it has a task', () => {
    expect(commentStatus(note(), null, { sending: true, agent: online })).toEqual({
      state: 'working',
      paused: null,
      waiting: null,
    });
    expect(unsent(note(), null, true)).toBe(false);
    // An agent's question or a done note is not on its way.
    expect(commentStatus({ mark: 'question', kind: 'question' }, null, { sending: true }).state).toBe('question');
    expect(commentStatus(note('answered'), null, { sending: true }).state).toBe('done');
  });
  it('queued with no computer online: one quiet line, not an error', () => {
    expect(commentStatus(note(), task('queued'), { agent: hostedNone })).toEqual({
      state: 'working',
      paused: null,
      waiting: 'Waiting for the editing computer',
    });
    expect(commentStatus(note(), null, { sending: true, agent: hostedOff }).waiting).toBe(
      'Waiting for the editing computer',
    );
    expect(commentStatus(note(), task('queued'), { agent: blocked }).waiting).toBe('Waiting for the agent');
  });
  it('no line once it runs, or while the computer is there', () => {
    expect(commentStatus(note(), task('running'), { agent: hostedNone }).waiting).toBeNull();
    expect(commentStatus(note(), task('queued'), { agent: online }).waiting).toBeNull();
  });
  it('waitingLine: says nothing before the page knows the agent', () => {
    expect(waitingLine(unknown)).toBeNull();
    expect(waitingLine(undefined)).toBeNull();
    expect(waitingLine(online)).toBeNull();
    expect(waitingLine(hostedNone)).toBe('Waiting for the editing computer');
    expect(waitingLine(blocked)).toBe('Waiting for the agent');
  });
});

describe('the bar button', () => {
  const base: SendInput = {
    owner: true,
    status: online,
    takeOpen: false,
    unsent: 0,
    finishing: false,
    saved: false,
    sent: false,
  };
  const b = (over: Partial<SendInput>) => sendButton({ ...base, ...over });

  // Owner, reviewer and approver (a share link, a signed-in member, local or hosted) get the same button; owner only
  // decides whether pressing it also opens the agent menu.
  for (const owner of [true, false]) {
    const who = owner ? 'the owner' : 'a reviewer or approver';
    const o = (over: Partial<SendInput>) => b({ owner, ...over });
    it(`${who}: Send N to agent counts the open comments not yet sent`, () => {
      expect(o({ unsent: 12 })).toEqual({ label: 'Send 12 to agent', action: 'send', opens: false });
      expect(o({ unsent: 1 }).label).toBe('Send 1 to agent');
    });
    it(`${who}: a take in progress sends even before its notes are transcribed`, () => {
      expect(o({ takeOpen: true })).toEqual({ label: 'Send to agent', action: 'send', opens: false });
      expect(o({ takeOpen: true, unsent: 3 }).label).toBe('Send 3 to agent');
    });
    it(`${who}: nothing new says Sent after a send, and explains on a click instead of failing`, () => {
      expect(o({ sent: true, saved: true })).toEqual({ label: 'Sent', action: 'none', opens: false });
      expect(o({ saved: true })).toMatchObject({ label: 'Saved', action: 'none' });
      expect(o({})).toEqual({ label: 'Send to agent', action: 'none', opens: false });
    });
    it(`${who}: Sending… while it finishes`, () => {
      expect(o({ finishing: true, takeOpen: true })).toEqual({ label: 'Sending…', action: 'none', opens: false });
    });
    it(`${who}: with no computer online it still sends (the queue waits for one)`, () => {
      for (const status of [hostedNone, hostedOff]) {
        expect(o({ status, unsent: 4 })).toEqual({ label: 'Send 4 to agent', action: 'send', opens: owner });
        expect(o({ status, takeOpen: true })).toMatchObject({ action: 'send', opens: owner });
        expect(o({ status, sent: true, saved: true })).toMatchObject({ label: 'Sent', action: 'none' });
        expect(o({ status, finishing: true, takeOpen: true }).label).toBe('Sending…');
      }
    });
    it(`${who}: local mode that can't edit saves; the agent menu says why, without popping up`, () => {
      expect(o({ status: blocked, unsent: 2, takeOpen: true })).toEqual({
        label: 'Send 2 to agent',
        action: 'save',
        opens: false,
      });
      expect(o({ status: blocked, saved: true, unsent: 2 })).toMatchObject({ label: 'Saved', action: 'save' });
      expect(o({ status: blocked, finishing: true, takeOpen: true }).label).toBe('Saving…');
    });
    it(`${who}: before the page knows the agent, Send sends`, () => {
      expect(o({ status: unknown, takeOpen: true })).toEqual({ label: 'Send to agent', action: 'send', opens: false });
    });
  }
  it('never says Finish review', () => {
    const states: Partial<SendInput>[] = [{}, { takeOpen: true }, { saved: true }, { finishing: true }, { unsent: 2 }];
    for (const status of [online, hostedNone, hostedOff, blocked, unknown])
      for (const owner of [true, false])
        for (const st of states) expect(b({ owner, status, ...st }).label).not.toMatch(/finish/i);
  });
});
