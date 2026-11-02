// The smallest edit between two texts (an outside edit shown in the open editor): it turns one into the other, keeps
// the lines both share, and maps the caret onto the same word when the edit is above and below it.
import { describe, expect, it } from 'vitest';
import { ChangeSet } from '@codemirror/state';
import { apply, changes } from './diff';

describe('changes', () => {
  it('turn a into b', () => {
    const cases: [string, string][] = [
      ['', ''],
      ['', 'new'],
      ['old', ''],
      ['a\nb\nc\n', 'a\nB\nc\n'],
      ['# T\n\nmine\n', '# T\n\nmine\n\n## Outline\n\n- a\n'],
      ['one\ntwo\nthree', 'zero\none\nthree\nfour'],
      ['x\n'.repeat(50), 'y\n'.repeat(50)],
      ['no newline at the end', 'no newline at the end\n'],
    ];
    for (const [a, b] of cases) expect(apply(a, changes(a, b)), JSON.stringify([a, b])).toBe(b);
  });

  it('are as small as the lines allow, in a’s positions, sorted', () => {
    const a = '# T\n\nmine here\n\nend\n';
    const b = '# T\n\nNEW\n\nmine here\n\nend\nmore\n';
    expect(changes(a, b)).toEqual([
      { from: 5, to: 5, insert: 'NEW\n\n' },
      { from: a.length, to: a.length, insert: 'more\n' },
    ]);
    // A changed word inside a line: just the word.
    expect(changes('The jump.\n', 'The trick.\n')).toEqual([{ from: 4, to: 8, insert: 'trick' }]);
  });

  it('keep the caret on its word when the edit is above and below it', () => {
    const a = '# Jump souls\n\nThe jump to secure souls.\n\n## Old\n';
    const b = '# Jump souls\n\nIntro first.\n\nThe jump to secure souls.\n\n## Outline\n\n- beat\n';
    const caret = a.indexOf('secure');
    const set = ChangeSet.of(changes(a, b), a.length);
    expect(set.mapPos(caret)).toBe(b.indexOf('secure'));
  });

  it('give one change when too much differs to be worth it', () => {
    const a = Array.from({ length: 40 }, (_, i) => `a${i}`).join('\n');
    const b = Array.from({ length: 40 }, (_, i) => `b${i}`).join('\n');
    const cs = changes(a, b, 5);
    expect(cs).toHaveLength(1);
    expect(apply(a, cs)).toBe(b);
  });
});
