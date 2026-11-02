import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Anchor, Comments, Recorded } from '../types';
import { FakeFiles, HttpError } from '../testing/fake';
import { sidecarText } from '../lib/v0';
import { Doc } from './doc.svelte';
import { audioExt, resume, talkOut, TALK_OUT_TODO, type TalkOutDeps } from './talkout';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

const DOC = 'v0/jump_v0.md';
const started = new Date(2026, 10, 1, 19, 2, 11).toISOString();
const rec = (o: Partial<Recorded> = {}): Recorded => ({
  blob: new Blob(['opus'], { type: 'audio/webm' }),
  type: 'audio/webm;codecs=opus',
  duration: 4.2,
  started,
  ...o,
});
const words = (text: string) =>
  JSON.stringify({ words: text.split(' ').map((w, i) => ({ w: ` ${w}`, s: i, e: i + 1, p: 0.9 })) });

async function setup(o: { doc?: string; files?: Record<string, string>; transcribe?: TalkOutDeps['transcribe'] } = {}) {
  const files = new FakeFiles({ [DOC]: o.doc ?? '# Jump\n\n', ...o.files });
  const added: { text: string; anchor?: Anchor }[] = [];
  const toasts: string[] = [];
  const comments: Comments = {
    list: async () => [],
    add: async (c) => {
      added.push(c);
      return { id: added.length, kind: 'note', status: 'open', text: c.text, t0: 0 };
    },
    send: async () => {},
    answer: async () => {},
  };
  const doc = new Doc(files, DOC);
  await doc.ready;
  const deps: TalkOutDeps = {
    files,
    comments,
    transcribe: o.transcribe ?? (async () => ({ text: '  So the idea is\nthe jump. ', words: [] })),
    toast: (t) => toasts.push(t),
    doc,
  };
  return { files, added, toasts, doc, deps };
}

describe('a talk-out', () => {
  it('keeps the audio, puts the words in at the caret as a plain paragraph, writes the sidecar and adds a To do', async () => {
    const t = await setup({ doc: '# Jump\n\nFirst thought.\n\n## Later\n' });
    const caret = t.doc.text.indexOf('First thought.') + 3;
    await talkOut(rec(), t.deps, { pos: caret, was: t.doc.text });
    expect(t.files.store.get('v0/voice/20261101-190211.webm')?.blob?.size).toBe(4);
    expect(t.files.writes.find((w) => w.path.endsWith('.webm'))?.ifMatch).toBe('*');
    expect(t.files.text(DOC)).toBe('# Jump\n\nFirst thought.\n\nSo the idea is the jump.\n\n## Later\n');
    expect(t.files.text('v0/voice/20261101-190211.md')).toBe(
      sidecarText({ audio: '20261101-190211.webm', inserted: true, text: 'So the idea is the jump.' }),
    );
    expect(t.added).toEqual([
      {
        text: TALK_OUT_TODO,
        anchor: { file: DOC, line: 5, heading: 'Jump', quote: 'So the idea is the jump.' },
      },
    ]);
    expect(TALK_OUT_TODO).toBe('Work this talk-out into the document.');
    expect(t.toasts).toEqual([]);
  });

  it('goes at the end when the text had no caret yet', async () => {
    const t = await setup({ doc: '# Jump\n\nOne.' });
    await talkOut(rec(), t.deps, null);
    expect(t.files.text(DOC)).toBe('# Jump\n\nOne.\n\nSo the idea is the jump.\n');
  });

  it('without transcription keeps the audio, puts nothing in and shows the hint', async () => {
    const t = await setup({
      transcribe: async () => {
        throw new HttpError(500, 'transcription failed', { hint: 'Set up [transcribe] in config.toml' });
      },
    });
    await talkOut(rec(), t.deps, null);
    expect(t.files.store.has('v0/voice/20261101-190211.webm')).toBe(true);
    expect(t.files.store.has('v0/voice/20261101-190211.md')).toBe(false);
    expect(t.files.text(DOC)).toBe('# Jump\n\n');
    expect(t.toasts).toEqual(['Set up [transcribe] in config.toml']);
    expect(t.added).toEqual([]);
  });

  it('takes the next name when one exists in the same second', async () => {
    const t = await setup();
    t.files.put('v0/voice/20261101-190211.webm', '', new Blob(['x']));
    await talkOut(rec(), t.deps, null);
    expect(t.files.store.has('v0/voice/20261101-190211-2.webm')).toBe(true);
    expect(t.files.text('v0/voice/20261101-190211-2.md')).toContain('Audio: 20261101-190211-2.webm');
  });

  it("doesn't put words in twice when another page already took them", async () => {
    const t = await setup({ files: { 'v0/voice/20261101-190211.md': 'Audio: x\nInserted: no\n\nSo.\n' } });
    await talkOut(rec(), t.deps, null);
    expect(t.files.text(DOC)).toBe('# Jump\n\n');
    expect(t.added).toEqual([]);
  });

  it('says so when the audio could not be kept', async () => {
    const t = await setup();
    t.files.failWith = new HttpError(503, 'editing machine is offline');
    await talkOut(rec(), t.deps, null);
    expect(t.toasts).toEqual(["The talk-out wasn't saved: The editing computer is offline"]);
    expect(t.added).toEqual([]);
  });

  it('is named for its type', () => {
    expect(audioExt('audio/webm;codecs=opus')).toBe('webm');
    expect(audioExt('audio/ogg')).toBe('ogg');
    expect(audioExt('audio/mp4')).toBe('m4a');
    expect(audioExt('')).toBe('webm');
  });
});

describe('talk-outs whose words came back after the page left', () => {
  it('go in at the end on the next open, oldest first, each with its To do and an inserted sidecar', async () => {
    const t = await setup({
      doc: '# Jump\n\nTyped.\n',
      files: {
        'v0/voice/20261101-190211.webm': '',
        'v0/voice/20261101-190211.words.json': words('First talk'),
        'v0/voice/20261101-190300.webm': '',
        'v0/voice/20261101-190300.words.json': words('Second talk'),
        'v0/voice/20261101-190400.webm': '',
        'v0/voice/20261101-190400.words.json': words('Already in'),
        'v0/voice/20261101-190400.md': sidecarText({ audio: '20261101-190400.webm', inserted: true, text: 'x' }),
        'v0/voice/20261101-190500.webm': '', // its transcription failed: nothing to put in
      },
    });
    expect(await resume(t.deps)).toBe(2);
    expect(t.files.text(DOC)).toBe('# Jump\n\nTyped.\n\nFirst talk\n\nSecond talk\n');
    expect(t.added.map((a) => [a.text, a.anchor?.quote])).toEqual([
      [TALK_OUT_TODO, 'First talk'],
      [TALK_OUT_TODO, 'Second talk'],
    ]);
    expect(t.files.text('v0/voice/20261101-190211.md')).toContain('Inserted: yes');
    // Opening again finds nothing left.
    expect(await resume(t.deps)).toBe(0);
    expect(t.files.text(DOC)).toBe('# Jump\n\nTyped.\n\nFirst talk\n\nSecond talk\n');
  });

  it('a sidecar that says "Inserted: no" whose words are already in only gets marked', async () => {
    const t = await setup({
      doc: '# Jump\n\nFirst talk\n',
      files: {
        'v0/voice/20261101-190211.webm': '',
        'v0/voice/20261101-190211.words.json': words('First talk'),
        'v0/voice/20261101-190211.md': sidecarText({
          audio: '20261101-190211.webm',
          inserted: false,
          text: 'First talk',
        }),
      },
    });
    expect(await resume(t.deps)).toBe(1);
    expect(t.files.text(DOC)).toBe('# Jump\n\nFirst talk\n');
    expect(t.added).toEqual([]);
    expect(t.files.text('v0/voice/20261101-190211.md')).toContain('Inserted: yes');
  });
});
