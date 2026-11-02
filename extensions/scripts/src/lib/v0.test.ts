import { describe, expect, it } from 'vitest';
import { docPathFor, folderOf, isDoc, migrate, parseSidecar, pickDoc, sidecarText, waiting, wordsOf } from './v0';

describe("the document's name", () => {
  it('is v0/<video>_v0.md, by the folder of the video', () => {
    expect(docPathFor('jump-souls')).toBe('v0/jump-souls_v0.md');
    expect(folderOf({ name: 'deadlock-jump-souls', path: 'deadlock/jump-souls' })).toBe('jump-souls');
    expect(folderOf({ name: 'clip', path: '' })).toBe('clip');
    expect(isDoc('v0/clip_v0.md')).toBe(true);
    expect(isDoc('v0/voice/x_v0.md')).toBe(false);
    expect(isDoc('v0/idea.md')).toBe(false);
  });

  it('is the one *_v0.md in v0/; with several, <folder>_v0.md, else the first by name', () => {
    expect(pickDoc(['v0/voice/a.webm', 'v0/clip_v0.md', 'v0/cuts/v1.json'], 'jump-souls')).toBe('v0/clip_v0.md');
    expect(pickDoc(['v0/b_v0.md', 'v0/jump-souls_v0.md', 'v0/a_v0.md'], 'jump-souls')).toBe('v0/jump-souls_v0.md');
    expect(pickDoc(['v0/b_v0.md', 'v0/a_v0.md'], 'jump-souls')).toBe('v0/a_v0.md');
    expect(pickDoc(['v0/idea.md', 'v0/script.md'], 'jump-souls')).toBeNull();
    expect(pickDoc([], 'x')).toBeNull();
  });
});

describe('a v0 from before it was one file', () => {
  it('becomes idea.md, then the outline and the script under ## Outline and ## Script', () => {
    expect(
      migrate({
        idea: '# Jump souls\n\nMy idea.\n',
        outline: '- Cold open\n- The trick\n',
        script: '### Cold open\n\n[The jump.] So.\n\n\n',
      }),
    ).toBe(
      '# Jump souls\n\nMy idea.\n\n## Outline\n\n- Cold open\n- The trick\n\n## Script\n\n### Cold open\n\n[The jump.] So.\n',
    );
  });

  it("keeps a file's own # heading in place of the section's, and leaves out empty or missing files", () => {
    expect(migrate({ idea: '# T\n', outline: '\n\n', script: '# Script for T\n\nWords.' })).toBe(
      '# T\n\n# Script for T\n\nWords.\n',
    );
    expect(migrate({ idea: '# T\n\nOnly the idea.' })).toBe('# T\n\nOnly the idea.\n');
    expect(migrate({})).toBe('');
  });

  it('drops only the old talk-out marks, never the words', () => {
    const idea =
      '# T\n\n<!-- talk-out voice/20261101-190211.webm -->\nSo the idea is.\n\n<!-- talk-out voice/b.webm -->\n(transcribing…)\n';
    expect(migrate({ idea })).toBe('# T\n\nSo the idea is.\n');
    expect(migrate({ idea: '# T\n\n<!-- my own note -->\n' })).toBe('# T\n\n<!-- my own note -->\n');
  });
});

describe("a talk-out's sidecar (v0/voice/<stamp>.md)", () => {
  it("holds the audio's name, whether its words are in the document, and the words", () => {
    const md = sidecarText({ audio: '20261101-190211.webm', inserted: true, text: ' So the idea. ' });
    expect(md).toBe('Audio: 20261101-190211.webm\nInserted: yes\n\nSo the idea.\n');
    expect(parseSidecar(md)).toEqual({ audio: '20261101-190211.webm', inserted: true, text: 'So the idea.' });
    expect(parseSidecar(sidecarText({ audio: 'a.webm', inserted: false, text: 'x' })).inserted).toBe(false);
  });

  it('from before (just the words) counts as in the document', () => {
    expect(parseSidecar('So the idea.\n')).toEqual({ audio: '', inserted: true, text: 'So the idea.' });
  });

  it('says which talk-outs still wait to go in: words and audio, and no sidecar or one not inserted', () => {
    const paths = [
      'v0/voice/20261101-190211.webm',
      'v0/voice/20261101-190211.words.json', // waiting: no sidecar
      'v0/voice/20261101-190300.webm',
      'v0/voice/20261101-190300.words.json',
      'v0/voice/20261101-190300.md', // in
      'v0/voice/20261101-190400.webm',
      'v0/voice/20261101-190400.words.json',
      'v0/voice/20261101-190400.md', // claimed, not in
      'v0/voice/20261101-190500.webm', // no words (transcription failed)
      'v0/voice/20261101-190000.words.json', // no audio
    ];
    const sidecars = new Map([
      ['v0/voice/20261101-190300.md', sidecarText({ audio: '20261101-190300.webm', inserted: true, text: 'a' })],
      ['v0/voice/20261101-190400.md', sidecarText({ audio: '20261101-190400.webm', inserted: false, text: 'b' })],
    ]);
    expect(waiting(paths, sidecars)).toEqual([
      {
        stamp: '20261101-190211',
        audio: 'v0/voice/20261101-190211.webm',
        words: 'v0/voice/20261101-190211.words.json',
        sidecar: null,
      },
      {
        stamp: '20261101-190400',
        audio: 'v0/voice/20261101-190400.webm',
        words: 'v0/voice/20261101-190400.words.json',
        sidecar: 'v0/voice/20261101-190400.md',
      },
    ]);
  });

  it("reads a words file's text", () => {
    expect(
      wordsOf(
        JSON.stringify({
          words: [
            { w: ' So', s: 0, e: 1 },
            { w: ' it', s: 1, e: 2 },
          ],
        }),
      ),
    ).toBe('So it');
    expect(wordsOf('not json')).toBe('');
  });
});
