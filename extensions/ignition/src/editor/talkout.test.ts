import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Anchor, Comments, Recorded } from '../types';
import { FakeFiles, HttpError } from '../testing/fake';
import { marker } from '../lib/doc';
import { Doc } from './doc.svelte';
import { audioExt, talkOut, TALK_OUT_TODO, type TalkOutDeps } from './talkout';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

const started = new Date(2026, 10, 1, 19, 2, 11).toISOString();
const rec = (o: Partial<Recorded> = {}): Recorded => ({
  blob: new Blob(['opus'], { type: 'audio/webm' }),
  type: 'audio/webm;codecs=opus',
  duration: 4.2,
  started,
  ...o,
});

function setup(o: { idea?: string; transcribe?: TalkOutDeps['transcribe']; question?: number | null } = {}) {
  const files = new FakeFiles({ 'v0/idea.md': o.idea ?? '# Jump\n\n' });
  const added: { text: string; anchor?: Anchor }[] = [];
  const answers: [number, string][] = [];
  const toasts: string[] = [];
  const comments: Comments = {
    list: async () => [],
    add: async (c) => {
      added.push(c);
      return { id: added.length, kind: 'note', status: 'open', text: c.text, t0: 0 };
    },
    send: async () => {},
    answer: async (q, text) => {
      answers.push([q, text]);
    },
  };
  const doc = new Doc(files, 'v0/idea.md');
  const deps: TalkOutDeps = {
    files,
    comments,
    transcribe: o.transcribe ?? (async () => ({ text: '  So the idea is\nthe jump. ', words: [] })),
    toast: (t) => toasts.push(t),
    target: () => ({ doc, pos: Number.MAX_SAFE_INTEGER }),
    question: () => o.question ?? null,
  };
  return { files, added, answers, toasts, doc, deps };
}

describe('a talk-out', () => {
  it('keeps the audio, lands at the caret, adds a To do and fills in its words', async () => {
    const t = setup();
    await t.doc.ready;
    await talkOut(rec(), t.deps);
    const rel = 'voice/20261101-190211.webm';
    expect(t.files.store.get('v0/voice/20261101-190211.webm')?.blob?.size).toBe(4);
    expect(t.files.writes.find((w) => w.path.endsWith('.webm'))?.ifMatch).toBe('*');
    expect(t.files.text('v0/voice/20261101-190211.md')).toBe('So the idea is the jump.\n');
    expect(t.files.text('v0/idea.md')).toBe(`# Jump\n\n${marker(rel)}\nSo the idea is the jump.\n`);
    expect(t.added).toEqual([
      { text: TALK_OUT_TODO, anchor: { file: 'v0/idea.md', line: 3, heading: 'Jump', quote: marker(rel) } },
    ]);
    expect(t.toasts).toEqual([]);
  });

  it('without transcription keeps the audio and says why in the block and a toast', async () => {
    const t = setup({
      transcribe: async () => {
        throw new HttpError(500, 'transcription failed', { hint: 'Set up [transcribe] in config.toml' });
      },
    });
    await talkOut(rec(), t.deps);
    expect(t.files.store.has('v0/voice/20261101-190211.webm')).toBe(true);
    expect(t.files.store.has('v0/voice/20261101-190211.md')).toBe(false);
    expect(t.files.text('v0/idea.md')).toContain('(not transcribed: Set up [transcribe] in config.toml)');
    expect(t.toasts).toEqual(['Set up [transcribe] in config.toml']);
    expect(t.added).toHaveLength(1);
  });

  it('answers the selected question instead', async () => {
    const t = setup({ question: 31 });
    const answered: number[] = [];
    t.deps.answered = (q) => answered.push(q);
    await talkOut(rec(), t.deps);
    expect(t.answers).toEqual([[31, 'So the idea is the jump.']]);
    expect(answered).toEqual([31]);
    expect(t.added).toEqual([]);
    expect(t.files.text('v0/idea.md')).toBe('# Jump\n\n');
    expect(t.files.store.has('v0/voice/20261101-190211.webm')).toBe(true);
    expect(t.toasts).toEqual(['Answered']);
  });

  it('does not answer with nothing when the words did not come', async () => {
    const t = setup({
      question: 31,
      transcribe: async () => {
        throw new Error('no transcriber');
      },
    });
    await talkOut(rec(), t.deps);
    expect(t.answers).toEqual([]);
    expect(t.toasts).toEqual(['no transcriber']);
  });

  it('takes the next name when one exists in the same second', async () => {
    const t = setup();
    t.files.put('v0/voice/20261101-190211.webm', '', new Blob(['x']));
    await talkOut(rec(), t.deps);
    expect(t.files.store.has('v0/voice/20261101-190211-2.webm')).toBe(true);
    expect(t.files.text('v0/idea.md')).toContain(marker('voice/20261101-190211-2.webm'));
  });

  it('says so when the audio could not be kept', async () => {
    const t = setup();
    t.files.failWith = new HttpError(503, 'editing machine is offline');
    await talkOut(rec(), t.deps);
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
