import { describe, expect, it } from 'vitest';
import type { AutomationState, EditingTask, EditingWorker } from './automation.svelte';
import {
  activeTask,
  agentEdge,
  agentStatus,
  commentStatus,
  liveTarget,
  managesAgent,
  pausedReason,
  pendingNotes,
  sendButton,
  sendNotice,
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
  it('open notes without a live task, and the notes of a paused one (Send resumes it)', () => {
    expect(unsent(note())).toBe(true);
    expect(unsent(note(), task('queued'))).toBe(false);
    expect(unsent(note(), task('running'))).toBe(false);
    expect(unsent(note(), task('paused'))).toBe(true);
    expect(unsent(note(), task('paused'), true)).toBe(false);
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
    expect(sendNotice(s, true)).toBeNull();
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
    const running = task('running', { notes: [1, 2, 3, 4], done: [1], dropped: [4], target: 'v7' });
    expect(pendingNotes(running)).toEqual([2, 3]);
    // Only the comments for the same version are on their way into it; the next version's wait their turn.
    const later = task('queued', { id: 't3', notes: [7], target: 'v8' });
    const s = agentStatus(
      automation({ tasks: [running, task('queued', { id: 't2', notes: [5, 6], target: 'v7' }), later] }),
      1001,
    );
    expect(s).toMatchObject({ working: true, batch: 2, next: 2 });
    const waiting = agentStatus(automation({ tasks: [task('queued', { notes: [5, 6] })] }), 1001);
    expect(waiting).toMatchObject({ working: false, batch: 0, next: 0 });
  });
  it('reads local (C1) and the local blocked reason', () => {
    const s = agentStatus(automation({ local: true, blocked: 'Install Claude Code or Codex to edit' }), 1001);
    expect(s).toMatchObject({ local: true, blocked: 'Install Claude Code or Codex to edit' });
    expect(sendNotice(s, true)).toEqual({ text: 'Install Claude Code or Codex to edit', settings: false });
  });
  it('no automation route: nothing to show', () => {
    expect(agentStatus(null, 0).available).toBe(false);
  });
  it('one line over Send for no computer, offline, and other people; none locally or when online', () => {
    const none = agentStatus(automation({ workers: [], local: false }), 1001);
    expect(sendNotice(none, true)).toEqual({ text: 'No editing computer connected', settings: true });
    expect(sendNotice(none, false)).toEqual({
      text: 'The editing computer is offline. Send still queues.',
      settings: false,
    });
    const off = agentStatus(automation({ local: false }), 5000);
    expect(off.online).toBe(false);
    expect(sendNotice(off, true)).toEqual({ text: 'The editing computer is offline', settings: true });
    expect(sendNotice(off, false)?.settings).toBe(false);
    expect(sendNotice(agentStatus(automation({ local: true }), 5000), true)).toBeNull();
    expect(sendNotice(agentStatus(null, 0), true)).toBeNull();
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

  // Owner, reviewer and approver (a share link, a signed-in member, local or hosted) get the same button: it says
  // Send (never an agent, never a count); the count is in its title.
  for (const owner of [true, false]) {
    const who = owner ? 'the owner' : 'a reviewer or approver';
    const o = (over: Partial<SendInput>) => b({ owner, ...over });
    it(`${who}: Send, with the open comments not yet sent counted in its title`, () => {
      expect(o({ unsent: 12 })).toEqual({ label: 'Send', action: 'send', title: 'Send 12 comments to the agent' });
      expect(o({ unsent: 1 }).title).toBe('Send 1 comment to the agent');
    });
    it(`${who}: a take in progress sends even before its notes are transcribed`, () => {
      expect(o({ takeOpen: true })).toEqual({ label: 'Send', action: 'send', title: 'Send to the agent' });
      expect(o({ takeOpen: true, unsent: 3 }).title).toBe('Send 3 comments to the agent');
    });
    it(`${who}: nothing new says Sent after a send, and explains on a click instead of failing`, () => {
      expect(o({ sent: true, saved: true })).toEqual({ label: 'Sent', action: 'none', title: 'Nothing new to send' });
      expect(o({ saved: true })).toMatchObject({ label: 'Saved', action: 'none' });
      expect(o({})).toEqual({ label: 'Send', action: 'none', title: 'Nothing new to send' });
    });
    it(`${who}: Sending… while it finishes`, () => {
      expect(o({ finishing: true, takeOpen: true })).toEqual({ label: 'Sending…', action: 'none', title: 'Sending…' });
    });
    it(`${who}: with no computer online it still sends (the queue waits for one)`, () => {
      for (const status of [hostedNone, hostedOff]) {
        expect(o({ status, unsent: 4 })).toMatchObject({ label: 'Send', action: 'send' });
        expect(o({ status, takeOpen: true })).toMatchObject({ action: 'send' });
        expect(o({ status, sent: true, saved: true })).toMatchObject({ label: 'Sent', action: 'none' });
        expect(o({ status, finishing: true, takeOpen: true }).label).toBe('Sending…');
      }
    });
    it(`${who}: local mode that can't edit saves, and says why in its title`, () => {
      expect(o({ status: blocked, unsent: 2, takeOpen: true })).toMatchObject({ label: 'Send', action: 'save' });
      expect(o({ status: blocked, unsent: 2, takeOpen: true }).title).toContain(blocked.blocked);
      expect(o({ status: blocked, saved: true, unsent: 2 })).toMatchObject({ label: 'Saved', action: 'save' });
      expect(o({ status: blocked, finishing: true, takeOpen: true }).label).toBe('Saving…');
    });
    it(`${who}: before the page knows the agent, Send sends`, () => {
      expect(o({ status: unknown, takeOpen: true })).toMatchObject({ label: 'Send', action: 'send' });
    });
  }
  it('never names an agent', () => {
    const states: Partial<SendInput>[] = [{}, { takeOpen: true }, { saved: true }, { finishing: true }, { unsent: 2 }];
    for (const status of [online, hostedNone, hostedOff, blocked, unknown])
      for (const st of states) expect(b({ status, ...st }).label).toMatch(/^(Send|Sending…|Saving…|Sent|Saved)$/);
  });
  it('never says Finish review', () => {
    const states: Partial<SendInput>[] = [{}, { takeOpen: true }, { saved: true }, { finishing: true }, { unsent: 2 }];
    for (const status of [online, hostedNone, hostedOff, blocked, unknown])
      for (const owner of [true, false])
        for (const st of states) expect(b({ owner, status, ...st }).label).not.toMatch(/finish/i);
  });
});

// The agent marks its comments as it goes (spec/automation.md, "Marking progress"): one is being worked on, the ones
// whose edit is in the live version are Done, one by one, before the report.
describe('progress, one comment at a time', () => {
  const note = (id: number) => ({ mark: 'open' as const, kind: 'note' as const, id });
  const online: AgentStatus = {
    available: true,
    local: true,
    worker: null,
    online: true,
    agent: 'Claude Code',
    working: true,
    batch: 3,
    next: 0,
    blocked: '',
  };
  const run = task('running', {
    notes: [1, 2, 3, 4],
    target: 'v5',
    working: { note: 3, text: '  slowing the mask fade ', at: 1 },
    marked: [
      { note: 1, change: 'Slowed the fade', at: 1, ready: true, render: 'r2' },
      { note: 2, change: 'Moved the bell', at: 2 },
    ],
  });
  it('the working comment has its line; done ones are Done with their words; the rest work', () => {
    expect(commentStatus(note(1), run, { agent: online })).toMatchObject({ state: 'done', change: 'Slowed the fade' });
    // Marked, but its edit isn't in v5 yet: still Working, being put in.
    expect(commentStatus(note(2), run, { agent: online })).toMatchObject({
      state: 'working',
      doing: 'Putting it in v5…',
    });
    expect(commentStatus(note(3), run, { agent: online })).toMatchObject({
      state: 'working',
      doing: 'slowing the mask fade',
    });
    const rest = commentStatus(note(4), run, { agent: online });
    expect(rest.state).toBe('working');
    expect(rest.doing ?? null).toBeNull();
  });
  it('only one comment is being worked on', () => {
    const doing = [1, 2, 3, 4].filter((id) => commentStatus(note(id), run, { agent: online }).doing != null);
    expect(doing).toEqual([2, 3]); // the one put in now, and the one worked on
    const moved = task('running', { ...run, working: { note: 4, at: 2 } });
    expect(commentStatus(note(3), moved, { agent: online }).doing ?? null).toBeNull();
    expect(commentStatus(note(4), moved, { agent: online }).doing).toBe(''); // no words: the sweep alone
  });
  it('a question wins over a mark, and the report wins over everything', () => {
    expect(commentStatus({ ...note(1), mark: 'question' }, run).state).toBe('question');
    expect(commentStatus({ ...note(4), mark: 'answered' }, run).state).toBe('done');
    // A paused attempt finished nothing: the marks don't count once it isn't running.
    const paused = task('paused', { ...run, state: 'paused', error: 'Usage limit reached' });
    expect(commentStatus(note(1), paused)).toMatchObject({ state: 'open', paused: 'Paused: usage limit' });
  });
  it('the working comment says when the computer is away', () => {
    expect(commentStatus(note(3), run, { agent: { ...online, online: false } }).doing).toBe(
      'Reconnecting to the agent',
    );
  });
  it('the edge light: on while a task of this video runs and not everything waits on a question', () => {
    const state = (q: number[]) => (id: number) => (q.includes(id) ? 'question' : 'working') as 'question' | 'working';
    const o = { recording: false, kind: false };
    expect(agentEdge([run], state([]), o)).toBe(true);
    expect(agentEdge([run], state([2, 3, 4]), o)).toBe(false); // #1 is done, the rest wait on questions
    expect(agentEdge([run], state([2, 3]), o)).toBe(true);
    expect(agentEdge([run], state([]), { recording: true, kind: false })).toBe(false);
    expect(agentEdge([run], state([]), { recording: false, kind: true })).toBe(false);
    expect(agentEdge([task('question', { notes: [1] })], state([]), o)).toBe(false);
    expect(agentEdge([task('completed')], state([]), o)).toBe(false);
    expect(agentEdge(null, state([]), o)).toBe(false);
    // Everything marked done: it still works (the version is being finished).
    const all = task('running', { notes: [1], marked: [{ note: 1, change: 'x', at: 1, ready: true }] });
    expect(agentEdge([all], state([]), o)).toBe(true);
    expect(liveTarget([task('queued', { target: 'v4' }), run])).toBe('v5');
    expect(liveTarget([task('queued', { target: 'v4' })])).toBeNull();
  });
});
