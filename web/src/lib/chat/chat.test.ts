import { describe, expect, it } from 'vitest';
import {
  applyEvent,
  EMPTY,
  imageName,
  normalizeView,
  openQuestion,
  pickDoc,
  sendState,
  statusLine,
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

  it('says what the agent is doing, in one quiet line', () => {
    const a = {
      config: { enabled: false, worker: 'w', agent: 'claude' },
      workers: [],
      renders: [],
      tasks: [],
    } as AutomationState;
    const online = { cuts: [] as string[], online: true };
    expect(statusLine(view(), a, online)).toBe('');
    expect(
      statusLine(view({ turn: { id: 't', state: 'queued' }, busy: 'The agent is busy with v1’s edits' }), a, online),
    ).toBe('The agent is busy with v1’s edits');
    expect(statusLine(view({ turn: { id: 't', state: 'queued' }, offline: true }), a, online)).toBe(
      'The editing computer is offline',
    );
    expect(statusLine(view({ turn: { id: 't', state: 'queued' }, blocked: 'No agent found.' }), a, online)).toBe(
      'No agent found.',
    );
    expect(statusLine(view({ turn: { id: 't', state: 'running' } }), a, online)).toBe('Working…');
    expect(
      statusLine(
        view({ turn: { id: 't', state: 'running' }, messages: [msg(2, 'agent', 'I', { streaming: true })] }),
        a,
        online,
      ),
    ).toBe('');
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
    };
    expect(statusLine(view(), building, online)).toBe('Building v1…');
    expect(statusLine(view(), building, { cuts: ['v1'], online: true })).toBe('Updating v1…');
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
