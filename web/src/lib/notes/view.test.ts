import { describe, expect, it } from 'vitest';
import type { NoteItem, NotesResponse } from './types';
import {
  activeKey,
  emptyText,
  heading,
  markOf,
  MARKS,
  presence,
  sidebarRows,
  toggleLabel,
  visible,
  type Row,
} from './view';

const versions = [{ label: 'v6' }, { label: 'v7' }, { label: 'v8' }];
const item = (over: Partial<NoteItem>): NoteItem => ({
  id: 1,
  kind: 'note',
  status: 'open',
  t0: 10,
  t1: 12,
  text: 'The title comes in too late',
  by: 'Ana',
  ...over,
});

/** v8 is the newest: open questions, this session's note, and things that must not show. */
const newest: NotesResponse = {
  version: 'v8',
  newest: true,
  items: [
    item({
      id: 21,
      kind: 'question',
      status: 'question',
      t0: 36.7,
      t1: 36.7,
      text: 'Blue or green?',
      by: 'agent',
      options: ['v8', 'v7', 'v9'],
      note: 4,
    }),
    item({ id: 22, kind: 'question', status: 'question', t0: 4.2, t1: 4.2, text: 'Keep the zoom?', by: 'agent' }),
    item({ id: 9, kind: 'note', status: 'open', t0: 20, t1: 23, text: 'Music is too loud here' }),
    item({ id: 3, kind: 'note', status: 'answered', answered_in: 'v8', change: 'Title on the beat' }),
    item({ id: 5, kind: 'note', status: 'retracted', text: 'scrap that' }),
    item({ id: 23, kind: 'question', status: 'answered', t0: 8, text: 'Answered already' }),
  ],
};

/** v7, an older version: its notes in their final state. */
const older: NotesResponse = {
  version: 'v7',
  newest: false,
  items: [
    item({ id: 3, status: 'answered', t0: 30, answered_in: 'v8', change: 'Title now lands on the first beat' }),
    item({
      id: 4,
      status: 'question',
      t0: 36.7,
      text: 'Start the ladder lower',
      question: 'Blue or green?',
      options: ['v8'],
    }),
    item({ id: 6, status: 'accepted', t0: 5, answered_in: 'v8', change: 'Cut the pause' }),
    item({ id: 7, status: 'retracted', t0: 6, text: 'never mind' }),
    item({ id: 8, status: 'open', t0: 50, text: 'A late note' }),
    item({ id: 10, status: 'answered', t0: 60, answered_in: 'v11', change: 'In a version that is gone' }),
  ],
};

const keys = (rows: Row[]) => rows.map((r) => r.key);

describe('sidebar rows', () => {
  it('newest version: open questions and session notes only, in time order; nothing answered or retracted', () => {
    const rows = sidebarRows(newest, { versions, shown: 'v8' });
    expect(keys(rows)).toEqual(['question:22', 'note:9', 'question:21']);
    expect(rows.map((r) => [r.time, r.mark, MARKS[r.mark]])).toEqual([
      ['0:04', 'question', '?'],
      ['0:20', 'open', '•'],
      ['0:36', 'question', '?'],
    ]);
  });

  it('a question has Answer (its thread id) and its options as chips; unknown versions are disabled', () => {
    const q = sidebarRows(newest, { versions, shown: 'v8' }).find((r) => r.id === 21)!;
    expect(q.answer).toBe(21);
    expect(q.text).toBe('Blue or green?');
    expect(q.options).toEqual([
      { label: 'v8', known: true, on: true },
      { label: 'v7', known: true, on: false },
      { label: 'v9', known: false, on: false },
    ]);
    expect(q.reopen).toBe(false);
    expect(q.see).toBeNull();
    const note = sidebarRows(newest, { versions, shown: 'v8' }).find((r) => r.id === 9)!;
    expect(note.answer).toBeNull();
    expect(note.t1).toBe(23);
  });

  it('older version: ✓ with the change and "answered in", ? with the question it became; retracted hidden', () => {
    const rows = sidebarRows(older, { versions, shown: 'v7' });
    expect(keys(rows)).toEqual(['note:6', 'note:3', 'note:4', 'note:8', 'note:10']);
    const by = Object.fromEntries(rows.map((r) => [r.id, r]));
    expect(by[3]).toMatchObject({
      mark: 'answered',
      change: 'Title now lands on the first beat',
      answeredIn: 'v8',
      see: 'v8',
      reopen: true,
      answer: null,
      question: null,
    });
    expect(by[4]).toMatchObject({ mark: 'question', question: 'Blue or green?', answer: null, change: null });
    expect(by[4].options).toEqual([{ label: 'v8', known: true, on: false }]);
    expect(by[8]).toMatchObject({ mark: 'open', reopen: false, answeredIn: null });
  });

  it('Reopen is only on answered notes of an older version, not accepted ones', () => {
    const rows = sidebarRows(older, { versions, shown: 'v7' });
    expect(rows.filter((r) => r.reopen).map((r) => r.id)).toEqual([3, 10]);
    expect(rows.find((r) => r.id === 6)).toMatchObject({ mark: 'answered', reopen: false, see: 'v8' });
  });

  it('roles (spec/team.md): no Answer without recording, no Reopen without approving', () => {
    const q = (ctx: { record?: boolean; approve?: boolean }) =>
      sidebarRows(newest, { versions, shown: 'v8', ...ctx }).find((r) => r.id === 21)!;
    expect(q({}).answer).toBe(21);
    expect(q({ record: true, approve: false }).answer).toBe(21);
    expect(q({ record: false }).answer).toBeNull();
    const reopen = (ctx: { record?: boolean; approve?: boolean }) =>
      sidebarRows(older, { versions, shown: 'v7', ...ctx })
        .filter((r) => r.reopen)
        .map((r) => r.id);
    expect(reopen({ approve: true })).toEqual([3, 10]);
    expect(reopen({ record: true, approve: false })).toEqual([]);
    // The rest of a row doesn't change with the role.
    const viewer = sidebarRows(older, { versions, shown: 'v7', record: false, approve: false });
    expect(viewer.find((r) => r.id === 3)).toMatchObject({ mark: 'answered', see: 'v8', reopen: false });
  });

  it('"see it in" needs the answering version in the family and not on screen', () => {
    expect(sidebarRows(older, { versions, shown: 'v7' }).find((r) => r.id === 10)).toMatchObject({
      answeredIn: 'v11',
      see: null,
    });
    // Flipped to v8 while v7 is the base: the notes are still v7's, but v8 is already on screen.
    expect(sidebarRows(older, { versions, shown: 'v8' }).find((r) => r.id === 3)!.see).toBeNull();
  });

  it('is empty without a response, and survives a missing t1', () => {
    expect(sidebarRows(null, { versions, shown: 'v8' })).toEqual([]);
    const rows = sidebarRows(
      { version: 'v8', newest: true, items: [item({ id: 2, kind: 'question', status: 'question', t1: null })] },
      { versions, shown: 'v8' },
    );
    expect(rows[0]).toMatchObject({ t0: 10, t1: 10 });
  });

  it('visible() and markOf() follow the note states', () => {
    expect(visible(item({ status: 'retracted' }), false)).toBe(false);
    expect(visible(item({ status: 'answered' }), true)).toBe(false);
    expect(visible(item({ status: 'accepted' }), true)).toBe(false);
    expect(visible(item({ status: 'answered' }), false)).toBe(true);
    expect(markOf(item({ status: 'accepted' }))).toBe('answered');
    expect(markOf(item({ kind: 'question', status: 'open' }))).toBe('question');
    expect(markOf(item({ status: 'open' }))).toBe('open');
  });
});

describe('active row, heading, toggle and presence', () => {
  const rows = sidebarRows(newest, { versions, shown: 'v8' });

  it('the row under the playhead: from just before it starts to just after it ends', () => {
    expect(activeKey(rows, 0)).toBeNull();
    expect(activeKey(rows, 4.0)).toBe('question:22');
    expect(activeKey(rows, 4.8)).toBe('question:22');
    expect(activeKey(rows, 5.5)).toBeNull();
    expect(activeKey(rows, 22)).toBe('note:9');
    expect(activeKey(rows, 36.6)).toBe('question:21');
  });

  it('heading, empty text and toggle label', () => {
    expect(heading(newest)).toBe('Questions');
    expect(heading(older)).toBe('Notes on v7');
    expect(heading(null)).toBe('');
    expect(emptyText({ ...newest, items: [] })).toBe('No open questions');
    expect(emptyText({ ...older, items: [] })).toBe('No notes on v7');
    expect(toggleLabel(rows, true)).toBe('2 questions');
    expect(toggleLabel(rows.slice(0, 1), true)).toBe('1 question');
    expect(toggleLabel(sidebarRows(older, { versions, shown: 'v7' }), false)).toBe('5 notes');
  });

  it('nothing on screen without the notes routes or anything to show; then the column, or the pill when closed', () => {
    const p = { available: true, content: true, everShown: false, open: true, phoneOpen: false, phone: false };
    expect(presence({ ...p, available: false })).toEqual({ panel: false, toggle: false });
    expect(presence({ ...p, content: false })).toEqual({ panel: false, toggle: false });
    expect(presence(p)).toEqual({ panel: true, toggle: false });
    expect(presence({ ...p, open: false })).toEqual({ panel: false, toggle: true });
    // Once it has shown, an empty version keeps the column (no jump on a C flip).
    expect(presence({ ...p, content: false, everShown: true })).toEqual({ panel: true, toggle: false });
    // Phones: the pill always toggles the list under the controls, closed at first.
    expect(presence({ ...p, phone: true })).toEqual({ panel: false, toggle: true });
    expect(presence({ ...p, phone: true, phoneOpen: true })).toEqual({ panel: true, toggle: true });
  });
});
