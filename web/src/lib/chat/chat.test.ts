import { describe, expect, it } from 'vitest';
import {
  activity,
  applyEvent,
  blocks,
  EMPTY,
  imageName,
  normalizeView,
  openQuestion,
  pickDoc,
  sendState,
  statusLine,
  summary,
  SUMMARY_PARTS,
  type ChatBlock,
  type ChatView,
} from './thread';
import { renderMarkdown, safeHref } from './markdown';
import type { AutomationState } from '../automation.svelte';

const view = (over: Partial<ChatView> = {}): ChatView => ({ ...EMPTY, ...over });
const msg = (id: number, role: string, text: string, more: Record<string, unknown> = {}) =>
  ({ id, role, text, created: '', ...more }) as never;

describe('the thread', () => {
  it('reads GET api/chat safely', () => {
    const v = normalizeView({
      messages: [
        msg(1, 'person', 'hi'),
        { id: 'x' },
        msg(2, 'nope', 'x'),
        msg(3, 'question', 'Which?', { options: [{ text: 'A', image: { url: 'u' } }, { bad: 1 }] }),
      ],
      turn: { id: 't', state: 'running', agent: 'claude' },
      busy: 'The agent is busy with v3’s edits',
      sent: { file: 'v0/a_v0.md', sha256: 'abc', note: 4, target: 'v1', at: 1, open: false },
    });
    expect(v.messages.map((m) => m.id)).toEqual([1, 3]);
    expect(v.messages[1]!.options).toEqual([{ text: 'A', image: { url: 'u' } }]);
    expect(v.turn).toEqual({ id: 't', state: 'running', agent: 'claude' });
    expect(v.sent?.target).toBe('v1');
    expect(normalizeView(null)).toEqual(EMPTY);
  });

  it('adds words at the offset it has, and asks for the thread again on a gap', () => {
    let v: ChatView | null = view({ messages: [msg(1, 'person', 'hi')] });
    v = applyEvent(v, { why: 'message', message: msg(2, 'agent', 'I’ll ', { streaming: true }) })!;
    v = applyEvent(v, { why: 'text', id: 2, at: 5, text: 'add it.' })!;
    expect(v.messages[1]!.text).toBe('I’ll add it.');
    // A repeat of words it has changes nothing; a gap reads the thread again.
    expect(applyEvent(v, { why: 'text', id: 2, at: 5, text: 'add it.' })).toBe(v);
    expect(applyEvent(v, { why: 'text', id: 2, at: 30, text: 'more' })).toBeNull();
    expect(applyEvent(v, { why: 'text', id: 9, at: 0, text: 'x' })).toBeNull();
    // The message's own event (the end of its streaming) keeps the longer text it already has.
    v = applyEvent(v, { why: 'message', message: msg(2, 'agent', 'I’ll ', { streaming: true }) })!;
    expect(v.messages[1]!.text).toBe('I’ll add it.');
    v = applyEvent(v, { why: 'message', message: msg(2, 'agent', 'I’ll add it.') })!;
    expect(v.messages[1]!.streaming).toBe(false);
    // In id order, wherever it arrives.
    v = applyEvent(v, { why: 'message', message: msg(5, 'tool', 'Editing the script') })!;
    v = applyEvent(v, { why: 'message', message: msg(4, 'info', 'x') })!;
    expect(v.messages.map((m) => m.id)).toEqual([1, 2, 4, 5]);
    v = applyEvent(v, { why: 'turn', turn: { id: 't', state: 'queued' } })!;
    expect(v.turn?.state).toBe('queued');
    v = applyEvent({ ...v, busy: 'busy' }, { why: 'turn', turn: null })!;
    expect(v.turn).toBeNull();
    expect(v.busy).toBe('');
  });

  it('offers a question’s choices until it is answered or the person says something', () => {
    const q = msg(3, 'question', 'Which look?');
    expect(openQuestion(view({ messages: [msg(1, 'person', 'x'), q, msg(4, 'agent', 'Done.')] }))).toBe(3);
    expect(openQuestion(view({ messages: [q, msg(4, 'person', 'Neon')] }))).toBeNull();
    expect(openQuestion(view({ messages: [msg(3, 'question', 'Which?', { answer: 'Neon' })] }))).toBeNull();
  });

  it('says under the thread only what a Send started; a turn has its own line', () => {
    const a = {
      config: { enabled: false, worker: 'w', agent: 'claude' },
      workers: [],
      renders: [],
      tasks: [],
    } as AutomationState;
    const online = { cuts: [] as string[], online: true };
    expect(statusLine(view(), a, online)).toBe('');
    const building = {
      ...a,
      tasks: [
        {
          id: 'j',
          notes: [1],
          version: 'v0',
          target: 'v1',
          state: 'running',
          agent: 'claude',
          worker: 'w',
          attempt: 1,
          events: [],
        },
      ],
    } as AutomationState;
    expect(statusLine(view(), building, online)).toBe('Building v1…');
    expect(statusLine(view(), building, { cuts: ['v1'], online: true })).toBe('Updating v1…');
    expect(
      statusLine(
        view(),
        { ...building, tasks: [{ ...building.tasks[0]!, state: 'queued' }] },
        { cuts: [], online: false },
      ),
    ).toBe('Building v1 once the editing computer is online');
    // While a turn is on, its activity line says it all.
    expect(statusLine(view({ turn: { id: 't', state: 'running' } }), building, online)).toBe('');
    expect(
      statusLine(view({ turn: { id: 't', state: 'queued' }, busy: 'The agent is busy with v1’s edits' }), a, online),
    ).toBe('');
  });

  it('shows a turn as one block, its activity line live while it runs and then a summary', () => {
    const t = (id: number, role: string, text: string, turn = 't1', more: Record<string, unknown> = {}) =>
      msg(id, role, text, { turn, ...more });
    const messages = [
      t(1, 'person', 'make the hook shorter'),
      t(2, 'agent', 'Sure. '),
      t(3, 'tool', 'Reading the script'),
      t(4, 'person', 'and add a link', '', { waiting: true }),
      t(5, 'tool', 'Editing the script'),
      t(6, 'agent', 'Done.', 't1', { streaming: true }),
    ];
    const running = view({ messages, turn: { id: 't1', state: 'running' } });
    const bs = blocks(running);
    // The reply is one block even around the message typed while it ran, which comes after it.
    expect(bs.map((b) => b.key)).toEqual(['p1', 'tt1', 'p4']);
    const reply = bs[1] as Extract<ChatBlock, { kind: 'agent' }>;
    expect(reply.messages.map((m) => m.id)).toEqual([2, 3, 5, 6]);
    // The agent writes after a tool: Working…; a tool runs: that tool, with an ellipsis.
    expect(activity(running, reply)).toEqual({ state: 'live', text: 'Working…', moving: true });
    const editing = { ...reply, messages: reply.messages.slice(0, 3) };
    expect(activity(running, editing).text).toBe('Editing the script…');
    expect(activity(running, { turn: 't1', messages: [t(9, 'tool', 'Reading example.org/…')] }).text).toBe(
      'Reading example.org/…',
    );
    // Ended: the summary, quiet.
    const ended = { ...running, turn: null };
    expect(activity(ended, reply)).toEqual({
      state: 'done',
      text: 'Edited the script · read the script',
      moving: false,
    });
    // No tools: no line.
    expect(activity(ended, { turn: 't1', messages: [t(2, 'agent', 'Hi.')] }).text).toBe('');
    // A turn that hasn't said anything has an empty block at the end, for its line.
    const queued = view({ messages: [t(1, 'person', 'hi', 't2')], turn: { id: 't2', state: 'queued' } });
    const qb = blocks(queued);
    expect(qb.map((b) => b.key)).toEqual(['p1', 'tt2']);
    expect(activity(queued, qb[1] as Extract<ChatBlock, { kind: 'agent' }>)).toEqual({
      state: 'live',
      text: 'Waiting for the agent…',
      moving: true,
    });
    // Agent messages with no turn group while they follow each other.
    expect(
      blocks(
        view({ messages: [msg(1, 'agent', 'a'), msg(2, 'tool', 'x'), msg(3, 'person', 'p'), msg(4, 'info', 'i')] }),
      ).map((b) => b.key),
    ).toEqual(['a1', 'p3', 'a4']);
  });

  it('says why a live turn waits, still, and Stopping… while it stops', () => {
    const b = { turn: 't', messages: [] };
    const q = (over: Partial<ChatView>) => view({ turn: { id: 't', state: 'queued' }, ...over });
    expect(activity(q({ busy: 'The agent is busy with v3’s edits' }), b)).toEqual({
      state: 'live',
      text: 'The agent is busy with v3’s edits',
      moving: false,
    });
    expect(activity(q({ offline: true }), b).text).toBe('The editing computer is offline');
    expect(activity(q({}), b, false)).toMatchObject({ text: 'The editing computer is offline', moving: false });
    expect(activity(q({ blocked: 'No agent found.' }), b)).toMatchObject({ text: 'No agent found.', moving: false });
    expect(activity(view({ turn: { id: 't', state: 'stopping' } }), b)).toMatchObject({
      text: 'Stopping…',
      moving: true,
    });
    // Another turn's block isn't live.
    expect(activity(q({}), { turn: 'old', messages: [msg(1, 'tool', 'Running a command')] })).toEqual({
      state: 'done',
      text: 'Ran a command',
      moving: false,
    });
  });

  it('sums a turn up in at most three parts, what changed first', () => {
    expect(summary([])).toBe('');
    expect(summary(['Working', 'Something new'])).toBe('');
    expect(
      summary([
        'Running a command',
        'Editing the script',
        'Running a command',
        'Editing the script',
        'Running a command',
      ]),
    ).toBe('Edited the script · ran 3 commands');
    expect(summary(['Editing notes.md', 'Editing notes.md'])).toBe('Edited notes.md');
    expect(summary(['Editing notes.md', 'Editing plan.md'])).toBe('Edited 2 files');
    expect(summary(['Saving an image', 'Saving an image', 'Looking at an image'])).toBe(
      'Saved 2 images · looked at an image',
    );
    expect(summary(['Reading example.org/…'])).toBe('Read example.org/…');
    expect(summary(['Reading example.org/…', 'Reading youtube.com/…', 'Reading a page'])).toBe('Read 3 pages');
    expect(summary(['Searching for "neon signs"', 'Searching the web'])).toBe('Searched the web 2 times');
    expect(summary(['Searching for "neon signs"'])).toBe('Searched the web');
    expect(summary(['Downloading from youtube.com'])).toBe('Downloaded from youtube.com');
    expect(summary(['Downloading a file'])).toBe('Downloaded a file');
    expect(summary(['Downloading from a.com', 'Downloading a file'])).toBe('Downloaded 2 files');
    expect(summary(['Reading notes.md', 'Reading plan.md', 'Reading the script'])).toBe(
      'Read the script · read 2 files',
    );
    expect(summary(['Looking through the files', 'Looking through the files'])).toBe('Looked through the files');
    // At most three parts: what changed, then the web, then commands; what it read is left out.
    expect(
      summary([
        'Reading the script',
        'Looking through the files',
        'Running a command',
        'Searching the web',
        'Editing the script',
        'Saving an image',
      ]),
    ).toBe('Edited the script · saved an image · searched the web');
    expect(SUMMARY_PARTS).toBe(3);
  });

  it('knows when Send has nothing new', () => {
    const doc = { path: 'v0/a_v0.md', sha256: 'abc' };
    const sent = { file: 'v0/a_v0.md', sha256: 'abc', note: 1, target: 'v1', at: 1, open: false };
    expect(sendState(view(), doc)).toEqual({ ok: true, why: '' });
    expect(sendState(view({ sent }), doc)).toEqual({ ok: false, why: 'Nothing changed since the last Send' });
    expect(sendState(view({ sent: { ...sent, open: true } }), doc).ok).toBe(true);
    expect(sendState(view({ sent }), { ...doc, sha256: 'def' }).ok).toBe(true);
    expect(sendState(view(), null).ok).toBe(false);
  });

  it('names images and finds the document', () => {
    expect(imageName({ name: 'Neon Sign.PNG', type: 'image/png' }, '20261102-101500', 1)).toBe('neon-sign.png');
    expect(imageName({ name: 'image.png', type: 'image/png' }, '20261102-101500', 2)).toBe(
      'pasted-20261102-101500-2.png',
    );
    expect(imageName({ type: 'image/jpeg' }, 's', 1)).toBe('pasted-s.jpg');
    expect(pickDoc(['v0/b_v0.md', 'v0/jump_v0.md', 'v0/images/x.png'], 'jump')).toBe('v0/jump_v0.md');
    expect(pickDoc(['v0/b_v0.md', 'v0/a_v0.md'], 'jump')).toBe('v0/a_v0.md');
    expect(pickDoc(['v0/idea.md'], 'jump')).toBeNull();
  });
});

describe('the agent’s Markdown', () => {
  const img = (src: string) => (src.startsWith('images/') ? `api/ext/scripts/files/v0/${src}` : null);

  it('renders a chat reply and escapes the rest', () => {
    const html = renderMarkdown(
      'Added **two** looks to *Cold open*:\n\n- Ink\n- `neon` sign\n\n![Neon](images/neon.png)\n\nSee [the clip](https://example.org/c) or https://x.com/a/1.\n\n<script>alert(1)</script>',
      img,
    );
    expect(html).toContain('<p>Added <strong>two</strong> looks to <em>Cold open</em>:</p>');
    expect(html).toContain('<ul><li>Ink</li><li><code>neon</code> sign</li></ul>');
    expect(html).toContain('<img src="api/ext/scripts/files/v0/images/neon.png" alt="Neon" loading="lazy">');
    expect(html).toContain('<a href="https://example.org/c" target="_blank" rel="noopener noreferrer">the clip</a>');
    expect(html).toContain(
      '<a href="https://x.com/a/1" target="_blank" rel="noopener noreferrer">https://x.com/a/1</a>.',
    );
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
    expect(html).not.toContain('<script>');
  });

  it('keeps unsafe links and images as text', () => {
    const html = renderMarkdown('[x](javascript:alert(1)) ![y](file:///etc/passwd) snake_case_name', img);
    expect(html).not.toContain('href="javascript');
    expect(html).not.toContain('<img');
    expect(html).toContain('snake_case_name');
    expect(safeHref('mailto:a@b.c')).toBe('mailto:a@b.c');
  });

  it('has headings, numbered lists, quotes, code and line breaks', () => {
    const html = renderMarkdown(
      '## Cold open\nline one\nline two\n\n1. a\n2. b\n\n> quoted\n\n```\n<b>x</b>\n```\n---',
    );
    expect(html).toContain('<p class="h"><strong>Cold open</strong></p><p>line one<br>line two</p>');
    expect(html).toContain('<ol><li>a</li><li>b</li></ol>');
    expect(html).toContain('<blockquote><p>quoted</p></blockquote>');
    expect(html).toContain('<pre><code>&lt;b&gt;x&lt;/b&gt;</code></pre><hr>');
  });
});
