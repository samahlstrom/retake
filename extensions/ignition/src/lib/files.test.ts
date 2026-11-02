import { describe, expect, it } from 'vitest';
import { isDoc, isImage, nthName, refName, talkOuts, wordsLength } from './files';

const f = (path: string, deleted = false) => ({ path, size: 1, modified: 0, sha256: path, deleted });

describe('the talk-outs in voice/', () => {
  it('are grouped by stamp, newest first, only those with audio', () => {
    const t = talkOuts([
      f('v0/voice/20261101-190211.webm'),
      f('v0/voice/20261101-190211.md'),
      f('v0/voice/20261101-190211.words.json'),
      f('v0/voice/20261102-080000.webm'),
      f('v0/voice/20261103-080000.md'),
      f('v0/voice/20261104-080000.webm', true),
      f('v0/voice/sub/x.webm'),
    ]);
    expect(t.map((x) => x.st)).toEqual(['20261102-080000', '20261101-190211']);
    expect(t[1]!.rel).toBe('voice/20261101-190211.webm');
    expect(t[1]!.md?.path).toBe('v0/voice/20261101-190211.md');
    expect(t[1]!.words?.path).toBe('v0/voice/20261101-190211.words.json');
    expect(t[0]!.md).toBeNull();
    expect(t[0]!.at?.getDate()).toBe(2);
  });

  it('last as long as their words', () => {
    expect(
      wordsLength({
        words: [
          { w: 'a', s: 0, e: 0.4 },
          { w: 'b', s: 0.5, e: 7.25 },
        ],
      }),
    ).toBe(7.25);
    expect(wordsLength({ words: [] })).toBeNull();
    expect(wordsLength(null)).toBeNull();
  });
});

describe('refs', () => {
  it('take a dropped file by a safe name', () => {
    expect(refName('shot 1.png')).toBe('shot 1.png');
    expect(refName('../../etc/passwd')).toBe('passwd');
    expect(refName('C:\\Users\\x\\.hidden.txt')).toBe('hidden.txt');
    expect(refName('...')).toBe('');
    expect(refName('a\u0000b.txt')).toBe('ab.txt');
  });

  it('number a name that exists', () => {
    expect(nthName('shot.png', 1)).toBe('shot.png');
    expect(nthName('shot.png', 2)).toBe('shot-2.png');
    expect(nthName('README', 3)).toBe('README-3');
  });

  it('open images inline and Markdown as a document', () => {
    expect(isImage('v0/refs/a.JPG')).toBe(true);
    expect(isImage('v0/refs/a.pdf')).toBe(false);
    expect(isDoc('v0/refs/links.md')).toBe(true);
    expect(isDoc('v0/refs/sub/links.md')).toBe(false);
  });
});
