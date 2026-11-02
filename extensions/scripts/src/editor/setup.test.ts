// The editor around the preview: the file's text is kept byte for byte (CR LF too), positions map to the file's
// offsets for the anchor of a typed comment, and an outside edit keeps the caret and stays out of undo.
import { describe, expect, it } from 'vitest';
import { Transaction } from '@codemirror/state';
import { undo } from '@codemirror/commands';
import { anchorAt } from '../lib/doc';
import { changes } from '../lib/diff';
import { editorState, isCRLF, offsetOf, outside, posOf } from './setup';

const make = (text: string, caret = 0) => editorState({ text, caret, save: () => {}, update: () => {} });

describe('the text', () => {
  it('is what the file holds, LF or CR LF', () => {
    for (const t of ['# T\n\nBody\n', '# T\r\n\r\nBody\r\n', 'mixed\r\nand\nlone\n', '']) {
      const s = make(t);
      // Mixed breaks become LF (an editor can only keep one kind); one kind is kept as it is.
      expect(s.sliceDoc()).toBe(isCRLF(t) || !t.includes('\r') ? t : t.replace(/\r\n/g, '\n'));
    }
    expect(isCRLF('a\r\nb')).toBe(true);
    expect(isCRLF('a\r\nb\nc')).toBe(false);
    expect(isCRLF('a\nb')).toBe(false);
  });

  it('positions are the file’s offsets for the anchor (CR LF counts two)', () => {
    const raw = '# Title\r\n\r\n## Cold open\r\n\r\nThe jump.\r\n';
    const s = make(raw);
    const pos = s.doc.toString().indexOf('jump');
    const off = offsetOf(s, pos);
    expect(off).toBe(raw.indexOf('jump'));
    expect(posOf(raw, off)).toBe(pos);
    expect(anchorAt('v0/a_v0.md', raw, off)).toMatchObject({ line: 5, heading: 'Cold open' });
    const lf = make('# T\n\nx');
    expect(offsetOf(lf, 5)).toBe(5);
    expect(posOf('# T\n\nx', 5)).toBe(5);
  });
});

describe('an outside edit', () => {
  it('keeps the caret on its word and is never undone by Ctrl+Z', () => {
    const a = '# Jump souls\n\nMy words here.\n';
    const b = '# Jump souls\n\nIntro.\n\nMy words here.\n\n## Outline\n';
    let s = make(a, a.indexOf('words'));
    // The person types, then the agent's edit arrives.
    s = s.update({ changes: { from: a.length, insert: 'More.\n' }, userEvent: 'input.type' }).state;
    const typed = s.sliceDoc();
    const next = b + 'More.\n';
    s = s.update({
      changes: changes(typed, next),
      annotations: [outside.of(true), Transaction.addToHistory.of(false)],
    }).state;
    expect(s.sliceDoc()).toBe(next);
    expect(s.selection.main.head).toBe(next.indexOf('words'));
    // Undo takes back the typing only; the agent's edit stays.
    let after = s;
    undo({ state: s, dispatch: (tr) => (after = tr.state) });
    expect(after.sliceDoc()).toBe(b);
  });
});
