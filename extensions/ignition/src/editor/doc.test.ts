import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeFiles, HttpError, settle } from '../testing/fake';
import { marker, TRANSCRIBING } from '../lib/doc';
import { Doc, SAVE_DELAY } from './doc.svelte';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

async function opened(files: FakeFiles, path = 'v0/idea.md'): Promise<Doc> {
  const d = new Doc(files, path);
  await d.ready;
  return d;
}

describe('a document', () => {
  it('opens with what is on disk', async () => {
    const files = new FakeFiles({ 'v0/idea.md': '# Jump\n\n' });
    const d = await opened(files);
    expect(d.text).toBe('# Jump\n\n');
    expect(d.exists).toBe(true);
    expect(d.sha).toBe(files.store.get('v0/idea.md')!.sha);
    expect(d.word).toBe('Saved');
  });

  it('that does not exist yet opens empty, and its first save makes it', async () => {
    const files = new FakeFiles();
    const d = await opened(files, 'v0/outline.md');
    expect(d.text).toBe('');
    expect(d.exists).toBe(false);
    expect(d.word).toBe('');
    d.input('## Beat one');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(files.writes).toEqual([{ path: 'v0/outline.md', ifMatch: '*', text: '## Beat one' }]);
    expect(d.exists).toBe(true);
  });

  it('saves 600 ms after the last keystroke, with If-Match on what it read', async () => {
    const files = new FakeFiles({ 'v0/idea.md': '# Jump\n' });
    const d = await opened(files);
    const read = d.sha;
    d.input('# Jump\nA');
    expect(d.word).toBe('Edited');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY - 100);
    d.input('# Jump\nAB');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY - 100);
    expect(files.writes).toEqual([]);
    await vi.advanceTimersByTimeAsync(100);
    expect(files.writes).toEqual([{ path: 'v0/idea.md', ifMatch: read, text: '# Jump\nAB' }]);
    expect(d.word).toBe('Saved');
    const written = files.store.get('v0/idea.md')!.sha;
    expect(d.sha).toBe(written);
    d.input('# Jump\nABC');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(files.writes[1]).toEqual({ path: 'v0/idea.md', ifMatch: written, text: '# Jump\nABC' });
  });

  it('saves at once on blur (flush)', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'a' });
    const d = await opened(files);
    d.input('ab');
    await d.flush();
    expect(files.text('v0/idea.md')).toBe('ab');
    await vi.advanceTimersByTimeAsync(SAVE_DELAY * 2);
    expect(files.writes).toHaveLength(1);
  });

  it('keeps typing that arrives while it saves, and saves it next', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'a' });
    const d = await opened(files);
    const release = files.hold();
    d.input('ab');
    const first = d.flush();
    await settle();
    expect(d.word).toBe('Saving…');
    d.input('abc');
    void d.save();
    release();
    await first;
    await settle();
    expect(files.text('v0/idea.md')).toBe('abc');
    expect(d.dirty).toBe(false);
  });

  it('asks when the file changed since it was read (409), and Keep mine writes over it', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'mine' });
    const d = await opened(files);
    files.put('v0/idea.md', 'theirs');
    d.input('mine, edited');
    await d.flush();
    expect(d.conflict).toEqual({ sha: files.store.get('v0/idea.md')!.sha });
    expect(d.word).toBe('Not saved');
    await d.keepMine();
    expect(files.text('v0/idea.md')).toBe('mine, edited');
    expect(d.conflict).toBeNull();
    expect(d.word).toBe('Saved');
  });

  it('reloads an outside edit in place when nothing is unsaved', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'one\ntwo' });
    const d = await opened(files);
    files.watch((c) => c.forEach((f) => d.outside(f)));
    files.outside('v0/idea.md', 'one\ntwo\nthree');
    await settle();
    expect(d.text).toBe('one\ntwo\nthree');
    expect(d.external).toEqual({ kind: 'reload', old: 'one\ntwo' });
    expect(d.word).toBe('Saved');
  });

  it('shows "Changed outside Retake" when there is unsaved typing; Reload takes theirs', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'one' });
    const d = await opened(files);
    files.watch((c) => c.forEach((f) => d.outside(f)));
    d.input('one, mine');
    files.outside('v0/idea.md', 'one, theirs');
    expect(d.conflict).toEqual({ sha: files.store.get('v0/idea.md')!.sha });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(files.text('v0/idea.md')).toBe('one, theirs');
    await d.reload(true);
    expect(d.text).toBe('one, theirs');
    expect(d.conflict).toBeNull();
    expect(d.dirty).toBe(false);
  });

  it('ignores its own save coming back through the watch, even mid-save', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'a' });
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
    const own = files.store.get('v0/idea.md')!;
    d.outside({ sha256: own.sha });
    expect(d.conflict).toBeNull();
    expect(d.external).toBeNull();
  });

  it('a deleted file reloads as empty', async () => {
    const files = new FakeFiles({ 'v0/outline.md': '## A' });
    const d = await opened(files, 'v0/outline.md');
    files.watch((c) => c.forEach((f) => d.outside(f)));
    files.outside('v0/outline.md', null);
    await settle();
    expect(d.text).toBe('');
    expect(d.exists).toBe(false);
  });

  it('says the editing computer is offline and never writes what it could not read', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'a' });
    files.failWith = new HttpError(503, 'editing machine is offline');
    const d = await opened(files);
    expect(d.loadError).toBe('The editing computer is offline');
    expect(d.word).toBe('Offline');
    d.input('typed');
    await d.flush();
    expect(files.writes).toEqual([]);
    files.failWith = null;
    await d.retry();
    expect(d.loadError).toBe('');
    expect(d.text).toBe('a');
  });

  it('a failed save keeps the text and says so', async () => {
    const files = new FakeFiles({ 'v0/idea.md': 'a' });
    const d = await opened(files);
    files.failWith = new HttpError(500, 'disk full');
    d.input('ab');
    await d.flush();
    expect(d.error).toBe('disk full');
    expect(d.word).toBe('Not saved');
    files.failWith = null;
    await d.save();
    expect(files.text('v0/idea.md')).toBe('ab');
    expect(d.word).toBe('Saved');
  });

  it('takes a talk-out at the caret and then its words', async () => {
    const files = new FakeFiles({ 'v0/idea.md': '# Jump\n\nFirst thought.\n' });
    const d = await opened(files);
    const rel = 'voice/20261101-190211.webm';
    const line = await d.insertTalkOut(d.text.length, rel);
    expect(line).toBe(4);
    expect(d.text.split('\n')[line]).toBe(marker(rel));
    expect(files.text('v0/idea.md')).toBe(`# Jump\n\nFirst thought.\n\n${marker(rel)}\n${TRANSCRIBING}\n`);
    expect(d.external?.kind).toBe('splice');
    expect(await d.fillTalkOut(rel, 'So the idea is the jump.')).toBe(true);
    expect(files.text('v0/idea.md')).toBe(`# Jump\n\nFirst thought.\n\n${marker(rel)}\nSo the idea is the jump.\n`);
    expect(await d.fillTalkOut(rel, 'again')).toBe(false);
  });
});
