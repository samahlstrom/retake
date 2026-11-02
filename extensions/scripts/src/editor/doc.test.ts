import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeFiles, HttpError, settle } from '../testing/fake';
import { Doc, SAVE_DELAY } from './doc.svelte';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

async function opened(files: FakeFiles, path = 'v0/jump_v0.md'): Promise<Doc> {
  const d = new Doc(files, path);
  await d.ready;
  return d;
}

describe('a document', () => {
  it('opens with what is on disk', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': '# Jump\n\n' });
    const d = await opened(files);
    expect(d.text).toBe('# Jump\n\n');
    expect(d.exists).toBe(true);
    expect(d.sha).toBe(files.store.get('v0/jump_v0.md')!.sha);
    expect([d.dirty, d.exists, d.error]).toEqual([false, true, '']);
  });

  it('that does not exist yet opens empty, and its first save makes it', async () => {
    const files = new FakeFiles();
    const d = await opened(files, 'v0/new_v0.md');
    expect(d.text).toBe('');
    expect(d.exists).toBe(false);
    expect(d.dirty).toBe(false);
    d.input('## Beat one');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(files.writes).toEqual([{ path: 'v0/new_v0.md', ifMatch: '*', text: '## Beat one' }]);
    expect(d.exists).toBe(true);
  });

  it('saves 600 ms after the last keystroke, with If-Match on what it read', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': '# Jump\n' });
    const d = await opened(files);
    const read = d.sha;
    d.input('# Jump\nA');
    expect(d.dirty).toBe(true);
    await vi.advanceTimersByTimeAsync(SAVE_DELAY - 100);
    d.input('# Jump\nAB');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY - 100);
    expect(files.writes).toEqual([]);
    await vi.advanceTimersByTimeAsync(100);
    expect(files.writes).toEqual([{ path: 'v0/jump_v0.md', ifMatch: read, text: '# Jump\nAB' }]);
    expect([d.dirty, d.exists, d.error]).toEqual([false, true, '']);
    const written = files.store.get('v0/jump_v0.md')!.sha;
    expect(d.sha).toBe(written);
    d.input('# Jump\nABC');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(files.writes[1]).toEqual({ path: 'v0/jump_v0.md', ifMatch: written, text: '# Jump\nABC' });
  });

  it('saves at once on blur (flush)', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'a' });
    const d = await opened(files);
    d.input('ab');
    await d.flush();
    expect(files.text('v0/jump_v0.md')).toBe('ab');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY * 2);
    expect(files.writes).toHaveLength(1);
  });

  it('keeps typing that arrives while it saves, and saves it next', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'a' });
    const d = await opened(files);
    const release = files.hold();
    d.input('ab');
    const first = d.flush();
    await settle();
    expect(d.saving).toBe(true);
    d.input('abc');
    void d.save();
    release();
    await first;
    await settle();
    expect(files.text('v0/jump_v0.md')).toBe('abc');
    expect(d.dirty).toBe(false);
  });

  it('asks when the file changed since it was read (409), and Keep mine writes over it', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'mine' });
    const d = await opened(files);
    files.put('v0/jump_v0.md', 'theirs');
    d.input('mine, edited');
    await d.flush();
    expect(d.conflict).toEqual({ sha: files.store.get('v0/jump_v0.md')!.sha });
    expect(!!(d.conflict || d.error)).toBe(true);
    await d.keepMine();
    expect(files.text('v0/jump_v0.md')).toBe('mine, edited');
    expect(d.conflict).toBeNull();
    expect([d.dirty, d.exists, d.error]).toEqual([false, true, '']);
  });

  it('reloads an outside edit in place when nothing is unsaved', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'one\ntwo' });
    const d = await opened(files);
    files.watch((c) => c.forEach((f) => d.outside(f)));
    files.outside('v0/jump_v0.md', 'one\ntwo\nthree');
    await settle();
    expect(d.text).toBe('one\ntwo\nthree');
    expect(d.external).toEqual({ kind: 'reload', old: 'one\ntwo' });
    expect([d.dirty, d.exists, d.error]).toEqual([false, true, '']);
  });

  it('shows "Changed outside Retake" when there is unsaved typing; Reload takes theirs', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'one' });
    const d = await opened(files);
    files.watch((c) => c.forEach((f) => d.outside(f)));
    d.input('one, mine');
    files.outside('v0/jump_v0.md', 'one, theirs');
    expect(d.conflict).toEqual({ sha: files.store.get('v0/jump_v0.md')!.sha });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(files.text('v0/jump_v0.md')).toBe('one, theirs');
    await d.reload(true);
    expect(d.text).toBe('one, theirs');
    expect(d.conflict).toBeNull();
    expect(d.dirty).toBe(false);
  });

  it('ignores its own save coming back through the watch, even mid-save', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'a' });
    const d = await opened(files);
    files.watch((c) => c.forEach((f) => d.outside(f)));
    const release = files.hold();
    d.input('ab');
    const saving = d.flush();
    await settle();
    // The watch reports a sha the page doesn't know yet: it waits for the save's answer.
    d.outside({ sha256: 'unknown-yet' });
    release();
    await saving;
    await settle();
    // After the save the deferred event is compared again: a different sha than ours means it changed again.
    expect(d.text).toBe('ab');
    const own = files.store.get('v0/jump_v0.md')!;
    d.outside({ sha256: own.sha });
    expect(d.conflict).toBeNull();
    expect(d.external).toBeNull();
  });

  it('a deleted file reloads as empty', async () => {
    const files = new FakeFiles({ 'v0/new_v0.md': '## A' });
    const d = await opened(files, 'v0/new_v0.md');
    files.watch((c) => c.forEach((f) => d.outside(f)));
    files.outside('v0/new_v0.md', null);
    await settle();
    expect(d.text).toBe('');
    expect(d.exists).toBe(false);
  });

  it('says the editing computer is offline and never writes what it could not read', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'a' });
    files.failWith = new HttpError(503, 'editing machine is offline');
    const d = await opened(files);
    expect(d.loadError).toBe('The editing computer is offline');
    expect(d.offline).toBe(true);
    d.input('typed');
    await d.flush();
    expect(files.writes).toEqual([]);
    files.failWith = null;
    await d.retry();
    expect(d.loadError).toBe('');
    expect(d.text).toBe('a');
  });

  it('a failed save keeps the text and says so', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': 'a' });
    const d = await opened(files);
    files.failWith = new HttpError(500, 'disk full');
    d.input('ab');
    await d.flush();
    expect(d.error).toBe('disk full');
    expect(!!(d.conflict || d.error)).toBe(true);
    files.failWith = null;
    await d.save();
    expect(files.text('v0/jump_v0.md')).toBe('ab');
    expect([d.dirty, d.exists, d.error]).toEqual([false, true, '']);
  });

  it('takes a paragraph at a caret it had before the text changed, or at the end, and saves it', async () => {
    const files = new FakeFiles({ 'v0/jump_v0.md': '# Jump\n\nFirst thought.\n' });
    const d = await opened(files);
    const was = d.text;
    // The caret was after "First thought."; then a line was typed above it.
    d.input('# Jump\n\nAbove.\n\nFirst thought.\n');
    const start = await d.insert('  So the idea is the jump. ', { pos: was.indexOf('.') + 1, was }, () => true);
    expect(files.text('v0/jump_v0.md')).toBe('# Jump\n\nAbove.\n\nFirst thought.\n\nSo the idea is the jump.\n');
    expect(d.text.slice(start).startsWith('So the idea')).toBe(true);
    // The caret follows it, so the next talk-out lands after this one.
    expect(d.external).toMatchObject({ kind: 'splice', caret: start + 'So the idea is the jump.'.length });
    await d.insert('At the end.', null);
    expect(files.text('v0/jump_v0.md')).toMatch(/jump\.\n\nAt the end\.\n$/);
    expect(d.external).toMatchObject({ kind: 'splice', caret: undefined });
  });
});
