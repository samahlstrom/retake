import { describe, expect, it } from 'vitest';
import {
  anchorAt,
  blocks,
  diff,
  findAnchor,
  firstLines,
  headingAbove,
  headingOf,
  headings,
  insertParagraph,
  keepLine,
  lineOf,
  lineStart,
  mapCaret,
  mapPos,
  scriptSections,
  titleOf,
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

  it('the one above the caret is where a typed comment points', () => {
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

describe('the anchor of a typed comment (ctx.onAnchor) and finding it again (reveal)', () => {
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

describe('a talk-out paragraph', () => {
  it('goes in at the caret as a plain paragraph: on a blank line, with blank lines around', () => {
    const r = insertParagraph('# Jump\n\n', 8, '  So the idea is the jump. ');
    expect(r.text).toBe('# Jump\n\nSo the idea is the jump.\n');
    expect(r.text.slice(r.start, r.caret)).toBe('So the idea is the jump.');
    expect(r.splice).toEqual({ at: 8, del: 0, ins: 'So the idea is the jump.\n' });
  });

  it('after the caret line when it has text, never inside it, with a blank line on each side', () => {
    const text = '# Jump\nFirst thought.\nMore.';
    const r = insertParagraph(text, 9, 'Words.');
    expect(r.text).toBe('# Jump\nFirst thought.\n\nWords.\n\nMore.');
    expect(insertParagraph('', 0, 'Words.').text).toBe('Words.\n');
    expect(insertParagraph('a\n\nb', 0, 'W').text).toBe('a\n\nW\n\nb');
    // No marker, no comment line: just the words.
    expect(r.text).not.toMatch(/<!--/);
  });

  it('follows the caret through what changed while its words were on the way', () => {
    const was = '# Jump\n\nOne.\n';
    const now = '# Jump\n\nAdded above.\n\nOne.\n';
    expect(mapPos(was, now, was.indexOf('One.') + 4)).toBe(now.indexOf('One.') + 4);
    expect(mapPos(was, now, 3)).toBe(3);
    expect(diff('abc', 'aXbc')).toEqual({ at: 1, del: 0, ins: 'X' });
    expect(diff('same', 'same')).toEqual({ at: 4, del: 0, ins: '' });
  });

  it('is a words file joined', () => {
    expect(wordsText({ words: [{ w: ' So' }, { w: ' the' }, { w: ' idea' }, { w: ',' }, { w: ' ok' }] })).toBe(
      'So the idea, ok',
    );
    expect(wordsText(null)).toBe('');
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
  const md = `# Jump souls\n\n<!-- an old note -->\nSo the idea is the jump.\n\n## Hook\nSecond line.\nThird line.`;

  it('is titled by its # line', () => {
    expect(titleOf(md)).toBe('Jump souls');
    expect(titleOf('no title\n## Only h2')).toBe('');
  });

  it('shows its first two lines, without the title or HTML comments', () => {
    expect(firstLines(md)).toEqual(['So the idea is the jump.', 'Hook']);
    expect(firstLines('# T\n\n')).toEqual([]);
  });
});

describe('the script read-only (the Script tab)', () => {
  const DOC = [
    '# Jump souls', // 0
    'My idea, my words.', // 1
    '', // 2
    '## Outline', // 3
    '- Cold open', // 4
    '', // 5
    '## Script', // 6
    '', // 7
    '### Cold open', // 8
    '[The jump.] So the idea is the jump.', // 9
    '', // 10
    '### The trick', // 11
    'Time it.', // 12
    '#### Detail', // 13
    'Frames.', // 14
    '', // 15
    '## Cut', // 16
    'Old line.', // 17
  ].join('\n');

  it("is the document's ## Script, cut at its ### headings", () => {
    const s = scriptSections(DOC)!;
    expect(s.map((x) => [x.heading, x.start, x.end])).toEqual([
      ['Cold open', 8, 11],
      ['The trick', 11, 16],
    ]);
    expect(s[0]!.lines[0]).toBe('### Cold open');
    expect(s[1]!.lines).toContain('#### Detail');
  });

  it('is nothing without a Script heading or with nothing under it', () => {
    expect(scriptSections('# T\n\nJust an idea.')).toBeNull();
    expect(scriptSections('# T\n\n## Script\n\n## Cut\nx')).toBeNull();
  });

  it('takes the beats at whatever level is under Script, and an old script.md whole at ##', () => {
    expect(scriptSections('# Script\n## A\na\n## B\nb')!.map((x) => x.heading)).toEqual(['A', 'B']);
    const old = scriptSections(SCRIPT, true)!;
    expect(old.map((x) => [x.heading, x.start, x.end])).toEqual([
      [null, 0, 2],
      ['Cold open', 2, 6],
      ['The trick', 6, 12],
    ]);
  });

  it('shows paragraphs and headings, not blank lines or HTML comments', () => {
    expect(blocks(['Para one', 'still one', '', '<!-- x -->', 'Talk', '### Sub', 'x'])).toEqual([
      { heading: false, text: 'Para one\nstill one' },
      { heading: false, text: 'Talk' },
      { heading: true, text: 'Sub' },
      { heading: false, text: 'x' },
    ]);
  });
});
