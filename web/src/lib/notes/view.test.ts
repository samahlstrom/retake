import { describe, expect, it } from 'vitest';
import type { NoteItem, NotesResponse } from './types';
import {
  activeKey,
  anchorText,
  earlierOpen,
  emptyText,
  forVersion,
  heading,
  historyLabels,
  intoVersion,
  markOf,
  MARKS,
  presence,
  rowVersions,
  sidebarRows,
  sketchFrame,
  todoByVersion,
  toggleLabel,
  toRow,
  typedStrokes,
  versionOf,
  versionStrip,
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

  it("newest version: the parent's notes carried over, at their mapped times, tagged with what became of them", () => {
    const carried = (as: string, was: number) => ({ from: 'v7', as, t0: was, t1: was + 1 });
    const res: NotesResponse = {
      version: 'v8',
      newest: true,
      items: [
        item({
          id: 2,
          t0: 1,
          t1: 2,
          status: 'answered',
          change: 'kept',
          answered_in: 'v8',
          carried: carried('unchanged', 1),
        }),
        item({
          id: 1,
          t0: 8.7,
          t1: 9.2,
          status: 'answered',
          change: 'pip 40%',
          answered_in: 'v8',
          carried: carried('check', 9),
        }),
        item({
          id: 5,
          kind: 'question',
          status: 'question',
          t0: 12.7,
          text: 'Lighter card?',
          note: 3,
          carried: carried('changed', 13),
        }),
        item({ id: 4, t0: 14, status: 'answered', change: 'not carried', answered_in: 'v8' }),
      ],
    };
    const rows = sidebarRows(res, { versions, shown: 'v8' });
    expect(rows.map((r) => [r.id, r.t0, r.mark, r.carried])).toEqual([
      [2, 1, 'answered', 'unchanged · from v7'],
      [1, 8.7, 'answered', 'answered, check it · from v7'],
      [5, 12.7, 'question', 'context changed · from v7'],
    ]);
    expect(rows[1]).toMatchObject({ change: 'pip 40%', see: null, reopen: false }); // v8 is on screen
    expect(sidebarRows(newest, { versions, shown: 'v8' }).every((r) => r.carried === null)).toBe(true);
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

it('continuous review retains resolved comments and their author', () => {
  const res: NotesResponse = {
    version: 'v9',
    newest: true,
    scope: 'video',
    items: [
      { id: 1, kind: 'note', status: 'answered', t0: 3, text: 'Fix this', by: 'Reviewer', answered_in: 'v9' },
      { id: 2, kind: 'note', status: 'retracted', t0: 4, text: 'Withdrawn' },
    ],
  };
  const result = sidebarRows(res, { versions: [], shown: 'v9' });
  expect(result).toHaveLength(1);
  expect(result[0]).toMatchObject({ by: 'Reviewer', mark: 'answered', reopen: true });
});

describe("an agent's question from inside its task", () => {
  const ask = {
    id: 31,
    task: 'task-1',
    t: 83.4,
    from: 80,
    to: 86,
    version: 'v7',
    text: 'Which logo?',
    visuals: { boxes: [{ box: [0.1, 0.2, 0.3, 0.2] as [number, number, number, number], label: 'this one' }] },
  };
  const res: NotesResponse = {
    version: 'v8',
    newest: true,
    scope: 'review',
    items: [item({ id: 12, status: 'question', question: 'Which logo?', ask })],
  };
  it('rides on its comment, with its own time as m:ss', () => {
    const [row] = sidebarRows(res, { versions, shown: 'v8' });
    expect(row!.kind).toBe('note');
    expect(row!.mark).toBe('question');
    expect(row!.ask).toMatchObject({ id: 31, task: 'task-1', t: 83.4, time: '1:23', text: 'Which logo?' });
    expect(row!.answer).toBeNull(); // the card under the comment answers it, not a separate question row
    expect(toggleLabel([row!], true)).toBe('1 question');
  });
  it('goes once the note is answered (open again)', () => {
    const [row] = sidebarRows({ ...res, items: [item({ id: 12, status: 'open', ask })] }, { versions, shown: 'v8' });
    expect(row!.ask).toBeNull();
  });
});

describe("a drawing's thumbnail frame", () => {
  const fam = [{ label: 'v1' }, { label: 'v2', poster: 'video/v2/index.poster.jpg' }, { label: 'v3', relay: true }];
  const snapshot = 'api/notes/4/snapshot?m=1';
  it("is the note's snapshot, the still of its moment, when the server has one", () => {
    const [row] = sidebarRows(
      { version: 'v1', newest: true, items: [item({ id: 4, version: 'v1', snapshot })] },
      { versions, shown: 'v1' },
    );
    expect(row!.snapshot).toBe(snapshot);
    expect(sketchFrame(row!, fam, 'v1', true)).toBe(snapshot);
    expect(sketchFrame(row!, fam, 'v1', false)).toBe(snapshot);
  });
  it('else the first frame of the version it was left on, not the one on screen', () => {
    expect(sketchFrame({ version: 'v1' }, fam, 'v2', true)).toBe('api/poster?v=v1');
    expect(sketchFrame({ version: 'v2' }, fam, 'v1', true)).toBe('video/v2/index.poster.jpg'); // a stream's own
    expect(sketchFrame({ snapshot: null }, fam, 'v1', true)).toBe('api/poster?v=v1'); // no version: the shown one
  });
  it('else none: no poster route, a version played from another computer, or an unknown version', () => {
    expect(sketchFrame({ version: 'v1' }, fam, 'v1', false)).toBeNull();
    expect(sketchFrame({ version: 'v3' }, fam, 'v3', true)).toBeNull();
    expect(sketchFrame({ version: 'v9' }, fam, 'v1', true)).toBeNull();
  });
  it("a question's row never has a snapshot", () => {
    const [row] = sidebarRows(
      { version: 'v1', newest: true, items: [item({ id: 5, kind: 'question', status: 'question', snapshot })] },
      { versions, shown: 'v1' },
    );
    expect(row!.snapshot).toBeNull();
  });
});

describe('comments belong to the version they were made on', () => {
  // Sam's item-shop case, small: v7 is the newest; v3 and v6 still have open comments, v6 one that v7 fixed.
  const family = ['v1', 'v2', 'v3', 'v4', 'v5', 'v6', 'v7'].map((label) => ({ label }));
  const res: NotesResponse = {
    version: 'v7',
    newest: true,
    scope: 'video',
    items: [
      item({ id: 1, version: 'v3', t0: 20, text: 'Cuts off too early' }),
      item({ id: 2, version: 'v3', t0: 34, text: 'Add the rank icons' }),
      item({ id: 3, version: 'v6', t0: 13, text: 'Drawing' }),
      item({ id: 4, version: 'v6', t0: 40, status: 'answered', answered_in: 'v7', change: 'Shorter intro' }),
      item({ id: 5, version: 'v7', t0: 5, text: 'Louder here' }),
      item({ id: 6, version: 'v7', t0: 9, status: 'retracted', text: 'scrap that' }),
      // The agent's question about a v3 comment (the thread itself was asked on v7) belongs to v3.
      item({ id: 40, kind: 'question', status: 'question', version: 'v7', note: 2, t0: 34, text: 'Which icons?' }),
      // A question about the version as a whole belongs to its own version.
      item({ id: 41, kind: 'question', status: 'question', version: 'v7', t0: 2, text: 'Keep the zoom?' }),
    ],
  };
  const rows = sidebarRows(res, { versions: family, shown: 'v7' });
  const todo = (r: Row) => r.mark !== 'answered';

  it('a version shows its own comments and the questions about them, nothing else', () => {
    expect(keys(forVersion(rows, 'v7'))).toEqual(['question:41', 'note:5']);
    expect(keys(forVersion(rows, 'v3'))).toEqual(['note:1', 'note:2', 'question:40']);
    expect(keys(forVersion(rows, 'v6'))).toEqual(['note:3', 'note:4']);
    expect(forVersion(rows, 'v1')).toEqual([]);
    // Each keeps its own time on its own version: nothing is moved onto the newest clock.
    expect(forVersion(rows, 'v3').map((r) => r.t0)).toEqual([20, 34, 34]);
  });

  it('a row the server did not tag (an older server) shows on whatever is on screen', () => {
    const plain = sidebarRows({ ...res, items: [item({ id: 9 })] }, { versions: family, shown: 'v2' });
    expect(keys(forVersion(plain, 'v2'))).toEqual(['note:9']);
    expect(keys(forVersion(plain, 'v5'))).toEqual(['note:9']);
    expect(rowVersions(plain).size).toBe(0);
  });

  it("a draft's comments belong to the version it heads for", () => {
    expect(versionOf('v8-draft2')).toBe('v8');
    expect(versionOf('v8')).toBe('v8');
    const drafts = sidebarRows(
      {
        ...res,
        items: [item({ id: 11, version: 'v8-draft1' }), item({ id: 12, version: 'v8-draft2' }), item({ id: 13 })],
      },
      { versions: family, shown: 'v8-draft2' },
    );
    expect(keys(forVersion(drafts, 'v8-draft2'))).toEqual(['note:11', 'note:12', 'note:13']);
    expect(keys(forVersion(drafts, 'v8'))).toEqual(['note:11', 'note:12', 'note:13']);
    // Their drawings: the version's own takes first, then its other drafts'.
    expect(historyLabels(drafts, 'v8-draft2')).toEqual(['v8-draft2', 'v8-draft1']);
    expect(historyLabels(rows, 'v3')).toEqual(['v3']);
  });

  it('counts what is still to do on each version', () => {
    const counts = todoByVersion(rows, todo, 'v7');
    expect(Object.fromEntries(counts)).toEqual({ v3: 3, v6: 1, v7: 2 });
  });

  it('the strip: every version oldest first, the shown one on, with its count; none for a single version', () => {
    const counts = todoByVersion(rows, todo, 'v7');
    const strip = versionStrip(family, 'v7', counts);
    expect(strip.map((e) => [e.version, e.todo, e.on])).toEqual([
      ['v1', 0, false],
      ['v2', 0, false],
      ['v3', 3, false],
      ['v4', 0, false],
      ['v5', 0, false],
      ['v6', 1, false],
      ['v7', 2, true],
    ]);
    expect(strip.every((e) => e.label === e.version && !e.drafting)).toBe(true);
    expect(versionStrip([{ label: 'v1' }], 'v1', counts)).toEqual([]);
  });

  it("the agent's drafts are one entry, the live version, that shows the newest revision", () => {
    const withDrafts = [...family, { label: 'v8-draft2' }, { label: 'v8-draft3' }];
    const strip = versionStrip(withDrafts, 'v8-draft2', new Map([['v8', 1]]));
    expect(strip).toHaveLength(8);
    expect(strip.at(-1)).toEqual({
      version: 'v8',
      name: 'v8',
      label: 'v8-draft3',
      drafting: true,
      todo: 1,
      on: true,
      into: 'v9',
    });
    // Updating v8 in place: its newest revision is the live one, not the v8 it had.
    const updating = versionStrip([...family, { label: 'v8' }, { label: 'v8-draft4' }], 'v8', new Map());
    expect(updating.at(-1)).toMatchObject({ version: 'v8', label: 'v8-draft4', drafting: true, on: true });
    // A single version and its draft: that's two, so there is a strip.
    expect(versionStrip([{ label: 'v1' }, { label: 'v2-draft1' }], 'v1', new Map()).map((e) => e.version)).toEqual([
      'v1',
      'v2',
    ]);
  });

  it("an extension's kind of version comes first, as `v0 · Script`, and is never an earlier cut", () => {
    const name = (v: { label: string; kind?: string }) => (v.kind ? `${v.label} · Script` : v.label);
    const fam = [{ label: 'v1' }, { label: 'v0', kind: 'scripts.doc' }, { label: 'v2' }];
    const counts = new Map([
      ['v0', 2],
      ['v1', 1],
    ]);
    const strip = versionStrip(fam, 'v2', counts, name);
    expect(strip.map((e) => [e.version, e.name, e.todo, e.on, e.kind])).toEqual([
      ['v0', 'v0 · Script', 2, false, 'scripts.doc'],
      ['v1', 'v1', 1, false, undefined],
      ['v2', 'v2', 0, true, undefined],
    ]);
    expect(earlierOpen(strip).map((e) => e.version)).toEqual(['v1']); // v0's script isn't an earlier cut
    const onV0 = versionStrip(fam, 'v0', counts, name);
    expect(onV0.find((e) => e.on)?.version).toBe('v0');
    expect(earlierOpen(onV0)).toEqual([]);
    // v0 alone: a single version, no strip; without a namer it reads its label.
    expect(versionStrip([{ label: 'v0', kind: 'scripts.doc' }], 'v0', counts, name)).toEqual([]);
    expect(versionStrip(fam, 'v2', counts)[0]!.name).toBe('v0');
  });

  it("a typed comment (an extension version's) has no moment: its anchor, and a question about a document its place", () => {
    const anchor = { file: 'v0/script.md', line: 12, heading: 'Cold open', quote: 'So the idea' };
    const [typed, keyed, plain] = [
      toRow(item({ id: 1, version: 'v0', t0: 0, t1: 0, anchor, key: 'doc/1' }), false, { versions: [], shown: 'v0' }),
      toRow(item({ id: 2, version: 'v0', t0: 0, t1: 0, key: 'doc/2' }), false, { versions: [], shown: 'v0' }),
      toRow(item({ id: 3 }), false, { versions: [], shown: 'v8' }),
    ];
    expect([typed.typed, typed.anchor]).toEqual([true, anchor]);
    expect([keyed.typed, keyed.anchor]).toEqual([true, null]);
    expect(plain.typed).toBe(false);
    expect(anchorText(anchor)).toBe('Cold open');
    expect(anchorText({ file: 'v0/outline.md', line: 1 })).toBe('outline.md');
    expect(anchorText(null)).toBe('');
    const asked = toRow(
      item({
        id: 4,
        status: 'question',
        work: { id: 't1', state: 'question' } as Row['work'],
        ask: { id: 40, task: 't1', t: 0, version: 'v0', text: 'Which opening?', anchor },
      }),
      false,
      { versions: [], shown: 'v0' },
    );
    expect(asked.ask).toMatchObject({ id: 40, time: 'Cold open', anchor });
  });

  it('"Open on earlier versions" on the latest version (or its draft) only, newest first', () => {
    const counts = todoByVersion(rows, todo, 'v7');
    const line = (shown: string, vs = family) =>
      earlierOpen(versionStrip(vs, shown, counts)).map((e) => [e.version, e.todo]);
    expect(line('v7')).toEqual([
      ['v6', 1],
      ['v3', 3],
    ]);
    expect(line('v6')).toEqual([]); // an older version: the strip says it
    expect(line('v3')).toEqual([]);
    const withDraft = [...family, { label: 'v8-draft1' }];
    expect(line('v8-draft1', withDraft)).toEqual([
      ['v7', 2],
      ['v6', 1],
      ['v3', 3],
    ]);
    expect(line('v7', withDraft)).toEqual([
      ['v6', 1],
      ['v3', 3],
    ]);
    // Nothing earlier to do: no line.
    expect(earlierOpen(versionStrip(family, 'v7', new Map([['v7', 2]])))).toEqual([]);
    expect(earlierOpen([])).toEqual([]);
  });
});

describe('a comment on vN becomes an edit in vN+1', () => {
  it('names the version a comment goes into: v7 → v8, a draft of v8 → v9, nothing for a label that isn’t vN', () => {
    expect(intoVersion('v7')).toBe('v8');
    expect(intoVersion('v8-draft2')).toBe('v9');
    expect(intoVersion('v12')).toBe('v13');
    expect(intoVersion('final')).toBeNull();
    expect(intoVersion(null)).toBeNull();
  });
  it('each row knows it (the server’s `target` wins, with `later`); a typed comment on a document has none', () => {
    const ctx = { versions, shown: 'v8' };
    expect(toRow(item({ version: 'v7' }), false, ctx).into).toBe('v8');
    expect(toRow(item({ version: 'v7', target: 'v8', later: 'v9' }), false, ctx)).toMatchObject({
      into: 'v8',
      later: 'v9',
    });
    expect(toRow(item({ version: 'v7', target: 'v9' }), false, ctx)).toMatchObject({ into: 'v9', later: null });
    expect(toRow(item({}), false, ctx).into).toBe('v9'); // an untagged row is the shown version's
    const doc = item({ version: 'v0', key: 'doc/1', anchor: { file: 'v0/a.md', line: 1 } });
    expect(toRow(doc, false, { versions: [], shown: 'v0' }).into).toBeNull();
  });
  it('the version strip says where each version’s comments go', () => {
    const strip = versionStrip([{ label: 'v6' }, { label: 'v7' }], 'v7', new Map([['v6', 3]]));
    expect(strip.map((e) => [e.version, e.into, e.todo])).toEqual([
      ['v6', 'v7', 3],
      ['v7', 'v8', 0],
    ]);
  });
});

describe('a typed comment on a video', () => {
  const lines = [
    { color: 'yellow', points: [[0.1, 0.2] as [number, number], [0.3, 0.4] as [number, number]], t: 4.25 },
    { color: 'red', points: [[0.5, 0.5] as [number, number]] },
    { color: 'white', points: [] },
  ];
  it('has its moment (it wants a version) and its own lines, each at its time', () => {
    const it = item({ id: 9, version: 'v8', t0: 4, t1: 4, key: 'doc/3', wants: 'version', strokes: lines });
    const row = toRow(it, false, { versions, shown: 'v8' });
    expect(row.typed).toBe(false);
    expect(row.time).toBe('0:04');
    expect(row.strokes).toEqual([
      {
        video_t: 4.25,
        color: 'yellow',
        points: [
          [0.1, 0.2],
          [0.3, 0.4],
        ],
      },
      { video_t: 4, color: 'red', points: [[0.5, 0.5]] },
    ]);
    expect(typedStrokes({ strokes: null, t0: 1, version: 'v8' })).toEqual([]);
    // A comment made from a take has no lines of its own: its drawings come from the take's history.
    expect(toRow(item({ version: 'v8', drawings: ['red'] }), false, { versions, shown: 'v8' }).strokes).toBeUndefined();
  });
});
