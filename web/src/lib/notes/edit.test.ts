import { describe, expect, it, vi } from 'vitest';
import { canEdit, editKey, editUrl, NoteEdit } from './edit.svelte';
import { toRow } from './view';
import type { EditingTask } from '../automation.svelte';
import type { NoteItem } from './types';

const row = { id: 4, kind: 'note' as const, mark: 'open' as const, editable: true, text: 'louder music', time: '0:07' };
const open = { state: 'open' as const };
const task = (over: Partial<EditingTask> = {}): EditingTask =>
  ({
    id: 't',
    notes: [4, 5],
    state: 'running',
    agent: 'fake',
    worker: 'w',
    attempt: 1,
    events: [],
    ...over,
  }) as EditingTask;

describe('canEdit: a comment still to do, the agent not on it', () => {
  it('an open comment the server says this page may edit', () => {
    expect(canEdit(row, open)).toBe(true);
    expect(canEdit({ ...row, editable: false }, open)).toBe(false);
    expect(canEdit({ ...row, kind: 'question' }, open)).toBe(false);
  });
  it('a quiet Working one (sent, the agent not there yet), not the one it is on', () => {
    expect(canEdit(row, { state: 'working' }, task())).toBe(true);
    expect(canEdit(row, { state: 'working', now: true }, task({ working: { note: 4, at: 1 } }))).toBe(false);
  });
  it('not one the agent says it is on or marked done, even before the list knows', () => {
    expect(canEdit(row, { state: 'working' }, task({ working: { note: 4, at: 1 } }))).toBe(false);
    expect(canEdit(row, { state: 'working' }, task({ marked: [{ note: 4, change: 'Louder', at: 1 }] }))).toBe(false);
    expect(canEdit(row, { state: 'working' }, task({ done: [4] }))).toBe(false);
    // Another note's marks don't matter; a queued task has none.
    expect(canEdit(row, { state: 'working' }, task({ working: { note: 5, at: 1 } }))).toBe(true);
    expect(canEdit(row, { state: 'working' }, task({ state: 'queued' }))).toBe(true);
  });
  it('not a question, not Done', () => {
    expect(canEdit(row, { state: 'question' })).toBe(false);
    expect(canEdit(row, { state: 'done' })).toBe(false);
    expect(canEdit({ ...row, mark: 'answered' }, open)).toBe(false);
    expect(canEdit({ ...row, mark: 'question' }, open)).toBe(false);
  });
  it('the row carries the server’s edited and editable', () => {
    const item: NoteItem = { id: 4, kind: 'note', status: 'open', t0: 7, text: 'x', edited: true, editable: true };
    const ctx = { versions: [], shown: 'v1' };
    expect(toRow(item, false, ctx)).toMatchObject({ edited: true, editable: true });
    expect(toRow({ ...item, edited: undefined, editable: undefined }, false, ctx)).toMatchObject({
      edited: false,
      editable: false,
    });
  });
});

describe('editKey', () => {
  const k = (key: string, shiftKey = false, isComposing = false) => editKey({ key, shiftKey, isComposing });
  it('Enter saves, Shift+Enter is a new line, Esc cancels, typing is typing', () => {
    expect(k('Enter')).toBe('save');
    expect(k('Enter', true)).toBeNull();
    expect(k('Escape')).toBe('cancel');
    expect(k('a')).toBeNull();
    expect(k('Enter', false, true)).toBeNull(); // an IME's Enter picks a word
  });
});

/** A fake server for POST api/notes/<id>/edit. */
function server(status = 200, error = '') {
  const calls: { url: string; body: unknown }[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    calls.push({ url: String(input), body: JSON.parse(String(init.body)) });
    return new Response(JSON.stringify(status === 200 ? { ok: true } : { error }), { status });
  });
  return { fetch: fetch as unknown as typeof globalThis.fetch, calls };
}

describe('NoteEdit: the card’s edit state', () => {
  it('starts with the words, saves the new ones, and ends once the list has them', async () => {
    const s = server();
    const order: string[] = [];
    const e = new NoteEdit({ fetch: s.fetch, onSaved: async () => void order.push('refreshed') });
    e.start(row);
    expect(e.id).toBe(4);
    expect(e.text).toBe('louder music');
    expect(e.editing(4)).toBe(true);
    expect(e.editing(5)).toBe(false);
    expect(e.changed).toBe(false);
    e.text = '  a little louder music  ';
    expect(e.changed).toBe(true);
    const saving = e.save();
    expect(e.busy).toBe(true);
    expect(await saving).toBe(true);
    expect(s.calls).toEqual([{ url: editUrl(4), body: { text: 'a little louder music' } }]);
    expect(order).toEqual(['refreshed']);
    expect(e.id).toBeNull();
    expect(e.busy).toBe(false);
  });

  it('unchanged words, or Cancel: nothing is sent', async () => {
    const s = server();
    const e = new NoteEdit({ fetch: s.fetch });
    e.start(row);
    e.text = 'louder music ';
    expect(await e.save()).toBe(true);
    expect(e.id).toBeNull();
    e.start(row);
    e.text = 'something else';
    e.cancel();
    expect(e.id).toBeNull();
    expect(e.text).toBe('');
    expect(s.calls).toEqual([]);
  });

  it('a refusal keeps the field with what was typed, and says why', async () => {
    const s = server(409, "this comment can't be edited now: the agent is working on it");
    const said: string[] = [];
    const e = new NoteEdit({ fetch: s.fetch, onError: (m) => said.push(m) });
    e.start(row);
    e.text = 'quieter';
    expect(await e.save()).toBe(false);
    expect(e.id).toBe(4);
    expect(e.text).toBe('quieter');
    expect(said).toEqual(["This comment can't be edited now: the agent is working on it"]);
  });

  it('a network failure says so', async () => {
    const said: string[] = [];
    const e = new NoteEdit({
      fetch: (async () => {
        throw new TypeError('offline');
      }) as unknown as typeof fetch,
      onError: (m) => said.push(m),
    });
    e.start(row);
    e.text = 'quieter';
    expect(await e.save()).toBe(false);
    expect(said).toEqual(['Could not save the comment. Try again.']);
  });

  it('on a phone it is in the comment box, not the card; starting another drops the first', () => {
    const e = new NoteEdit();
    e.start(row, true);
    expect(e.inBox).toBe(true);
    expect(e.editing(4)).toBe(false);
    expect(e.editing(4, true)).toBe(true);
    expect(e.time).toBe('0:07');
    e.text = 'changed';
    e.start({ id: 5, text: 'shorter intro', time: '0:08' });
    expect(e.id).toBe(5);
    expect(e.text).toBe('shorter intro');
    expect(e.inBox).toBe(false);
  });
});
