import { describe, expect, it } from 'vitest';
import {
  anchorAt,
  blocks,
  fillTalkOut,
  findAnchor,
  firstLines,
  headingAbove,
  headingOf,
  headings,
  insertBlock,
  keepLine,
  lineOf,
  lineStart,
  mapCaret,
  marker,
  sections,
  talkOutBlock,
  talkOutLine,
  talkOutOf,
  titleOf,
  TRANSCRIBING,
  waitingTalkOuts,
  wordsText,
} from './doc';

const SCRIPT = [
  '# Jump souls', // 0
  '', // 1
  '## Cold open', // 2
  'Start on the jump, mid-air.', // 3
  '[the wall, the souls]', // 4
  '', // 5
  '## The trick', // 6
  'Why it works: the secure window', // 7
  'lasts half a second.', // 8
  '', // 9
  '### Detail', // 10
  'Frame data here.', // 11
].join('\n');
const at = (text: string, line: number, col = 0) => lineStart(text, line) + col;

describe('headings', () => {
  it('are # lines, with their level and text', () => {
    expect(headingOf('## Cold open')).toEqual({ level: 2, text: 'Cold open' });
    expect(headingOf('## Cold open ##')).toEqual({ level: 2, text: 'Cold open' });
    expect(headingOf('# C#')).toEqual({ level: 1, text: 'C#' });
    expect(headingOf('#')).toEqual({ level: 1, text: '' });
    expect(headingOf('#hashtag')).toBeNull();
    expect(headingOf('####### seven')).toBeNull();
    expect(headings(SCRIPT).map((h) => [h.line, h.level, h.text])).toEqual([
      [0, 1, 'Jump souls'],
      [2, 2, 'Cold open'],
      [6, 2, 'The trick'],
      [10, 3, 'Detail'],
    ]);
  });

  it('the one above the caret names what the ask box is about', () => {
    expect(headingAbove(SCRIPT, at(SCRIPT, 4, 3))?.text).toBe('Cold open');
    expect(headingAbove(SCRIPT, at(SCRIPT, 6))?.text).toBe('The trick');
    expect(headingAbove(SCRIPT, at(SCRIPT, 9))?.text).toBe('The trick');
    expect(headingAbove(SCRIPT, SCRIPT.length)?.text).toBe('Detail');
    expect(headingAbove('no headings\nhere', 5)).toBeNull();
  });

  it('counts lines and finds where they start', () => {
    expect(lineOf('a\nb\nc', 0)).toBe(0);
    expect(lineOf('a\nb\nc', 2)).toBe(1);
    expect(lineOf('a\nb\nc', 99)).toBe(2);
    expect(lineStart('a\nbb\nc', 2)).toBe(5);
    expect(lineStart('a', 5)).toBe(1);
  });
});

describe('anchors', () => {
  it('at the caret: the line, the heading above and the paragraph', () => {
    expect(anchorAt('v0/script.md', SCRIPT, at(SCRIPT, 8, 2))).toEqual({
      file: 'v0/script.md',
      line: 9,
      heading: 'The trick',
      quote: 'Why it works: the secure window lasts half a second.',
    });
    expect(anchorAt('v0/script.md', SCRIPT, at(SCRIPT, 2))).toEqual({
      file: 'v0/script.md',
      line: 3,
      heading: 'Cold open',
    });
    expect(anchorAt('v0/idea.md', 'plain', 2)).toEqual({ file: 'v0/idea.md', line: 1, quote: 'plain' });
    expect(anchorAt('v0/idea.md', 'x'.repeat(300), 0).quote).toHaveLength(200);
  });

  it('are found by heading first', () => {
    const edited = `Intro line\n\n${SCRIPT}`;
    expect(findAnchor(edited, { line: 3, heading: 'Cold open' })).toBe(4);
    expect(findAnchor(edited, { line: 3, heading: 'cold  OPEN' })).toBe(4);
  });

  it('then by quote inside that heading', () => {
    expect(findAnchor(SCRIPT, { line: 1, heading: 'The trick', quote: 'lasts half a second.' })).toBe(7);
    // The same words under another heading don't count while the heading is there.
    const twice = `${SCRIPT}\n\n## Again\nlasts half a second.`;
    expect(findAnchor(twice, { line: 1, heading: 'Again', quote: 'lasts half a second.' })).toBe(14);
  });

  it('then by quote anywhere, when the heading was renamed', () => {
    const renamed = SCRIPT.replace('## The trick', '## How it works');
    expect(findAnchor(renamed, { line: 40, heading: 'The trick', quote: 'the secure window lasts' })).toBe(7);
    // A long quote that changed at its end still matches by its start.
    const quote = 'Why it works: the secure window lasts half a second, and nobody times it right.';
    expect(findAnchor(renamed, { line: 1, quote })).toBe(7);
  });

  it('then by line, clamped to the text', () => {
    expect(findAnchor(SCRIPT, { line: 4, heading: 'Gone', quote: 'not there' })).toBe(3);
    expect(findAnchor(SCRIPT, { line: 400 })).toBe(11);
    expect(findAnchor(SCRIPT, { line: 0 })).toBe(0);
    expect(findAnchor('', { line: 3 })).toBe(-1);
  });
});

describe('talk-outs', () => {
  const rel = 'voice/20261101-190211.webm';

  it('are a marker line and their words', () => {
    expect(talkOutBlock(rel, '  So the idea is…  ')).toBe(
      '<!-- talk-out voice/20261101-190211.webm -->\nSo the idea is…',
    );
    expect(talkOutOf(marker(rel))).toBe(rel);
    expect(talkOutOf('<!--talk-out voice/a.webm-->')).toBe('voice/a.webm');
    expect(talkOutOf('<!-- something else -->')).toBeNull();
  });

  it('go in at the caret as a paragraph of their own', () => {
    const idea = '# Jump souls\n\n';
    const r = insertBlock(idea, idea.length, talkOutBlock(rel, 'Words.'));
    expect(r.text).toBe(`# Jump souls\n\n${marker(rel)}\nWords.\n`);
    expect(r.caret).toBe(r.text.length - 1);
    expect(talkOutLine(r.text, rel)).toBe(2);
  });

  it('after the caret line when it has text, with blank lines around', () => {
    const t = 'First para\nstill first\n\nSecond';
    const r = insertBlock(t, 3, 'BLOCK');
    expect(r.text).toBe('First para\n\nBLOCK\n\nstill first\n\nSecond');
    const end = insertBlock('Only line', 4, 'BLOCK');
    expect(end.text).toBe('Only line\n\nBLOCK\n');
    const top = insertBlock('', 0, 'BLOCK');
    expect(top.text).toBe('BLOCK\n');
    const blankLine = insertBlock('A\n\n\nB', at('A\n\n\nB', 2), 'BLOCK');
    expect(blankLine.text).toBe('A\n\nBLOCK\n\nB');
  });

  it('get their words in place of the placeholder', () => {
    const t = `# Idea\n\n${talkOutBlock(rel, TRANSCRIBING)}\n\nMore.\n`;
    const r = fillTalkOut(t, rel, 'So the idea is the jump.');
    expect(r?.text).toBe(`# Idea\n\n${marker(rel)}\nSo the idea is the jump.\n\nMore.\n`);
    expect(r?.splice).toEqual({
      at: t.indexOf(TRANSCRIBING),
      del: TRANSCRIBING.length,
      ins: 'So the idea is the jump.',
    });
  });

  it('that still wait for their words are found, and their words read from the words file', () => {
    const t = `${talkOutBlock('voice/a.webm', TRANSCRIBING)}\n\n${talkOutBlock('voice/b.webm', 'Done.')}\n`;
    expect(waitingTalkOuts(t)).toEqual(['voice/a.webm']);
    expect(
      wordsText({ model: 'x', words: [{ w: 'So' }, { w: ' the' }, { w: 'idea' }, { w: ',' }, { w: 'right' }] }),
    ).toBe('So the idea, right');
    expect(wordsText({})).toBe('');
  });

  it("don't overwrite what someone wrote there, or come back after being removed", () => {
    const typed = `${marker(rel)}\nI typed this`;
    expect(fillTalkOut(typed, rel, 'words')).toBeNull();
    expect(fillTalkOut('# Idea\n', rel, 'words')).toBeNull();
  });
});

describe('the caret', () => {
  it('moves with a change before it, stays before one after it', () => {
    const s = { at: 10, del: 4, ins: 'abcdefgh' };
    expect(mapCaret(5, s)).toBe(5);
    expect(mapCaret(20, s)).toBe(24);
    expect(mapCaret(12, s)).toBe(18);
    expect(mapCaret(10, s)).toBe(10);
  });

  it('keeps its line and column across a reload', () => {
    const old = 'one\ntwo two\nthree';
    expect(keepLine(old, at(old, 1, 4), 'ONE\nTWO TWO TWO\nTHREE')).toBe(at('ONE\nTWO TWO TWO\nTHREE', 1, 4));
    expect(keepLine(old, at(old, 2, 5), 'short')).toBe(5);
    expect(keepLine(old, at(old, 1, 7), 'a\nb')).toBe(3);
  });
});

describe('an idea on the ideas page', () => {
  const md = `# Jump souls\n\n${marker('voice/a.webm')}\nSo the idea is the jump.\n\n## Hook\nSecond line.\nThird line.`;

  it('is titled by its # line', () => {
    expect(titleOf(md)).toBe('Jump souls');
    expect(titleOf('no title\n## Only h2')).toBe('');
  });

  it('shows its first two lines, without the title or talk-out markers', () => {
    expect(firstLines(md)).toEqual(['So the idea is the jump.', 'Hook']);
    expect(firstLines('# T\n\n')).toEqual([]);
  });
});

describe('the script read-only', () => {
  it('is cut at its ## headings', () => {
    const s = sections(SCRIPT);
    expect(s.map((x) => [x.heading, x.start, x.end])).toEqual([
      [null, 0, 2],
      ['Cold open', 2, 6],
      ['The trick', 6, 12],
    ]);
    expect(s[1]!.lines[0]).toBe('## Cold open');
  });

  it('shows paragraphs and headings, not blank lines or HTML comments', () => {
    expect(blocks(['Para one', 'still one', '', marker('voice/a.webm'), 'Talk', '### Sub', 'x'])).toEqual([
      { heading: false, text: 'Para one\nstill one' },
      { heading: false, text: 'Talk' },
      { heading: true, text: 'Sub' },
      { heading: false, text: 'x' },
    ]);
  });
});
