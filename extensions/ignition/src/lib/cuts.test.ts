import { describe, expect, it } from 'vitest';
import { chips, labelOfCutFile, newerFirst, parseCut, sectionAt, sectionFor, versionOrder, type Cut } from './cuts';

const v1: Cut = {
  version: 'v1',
  sections: [
    { heading: 'Cold open', from: 0, to: 10 },
    { heading: 'The trick', from: 10, to: 50 },
  ],
};
const v3: Cut = {
  version: 'v3',
  sections: [
    { heading: 'The trick', from: 12.4, to: 61 },
    { heading: 'Outro', from: 61, to: 70 },
  ],
};

describe('cuts files', () => {
  it('are read safely', () => {
    expect(
      parseCut({
        version: 'v3',
        sections: [
          { heading: '## The trick', from: 12.4, to: 61 },
          { heading: 'Cold open', from: 0, to: 12.4 },
          { heading: '', from: 1, to: 2 },
          { heading: 'Bad', from: 5, to: 1 },
          'nope',
        ],
      }),
    ).toEqual({
      version: 'v3',
      sections: [
        { heading: 'Cold open', from: 0, to: 12.4 },
        { heading: 'The trick', from: 12.4, to: 61 },
      ],
    });
    expect(parseCut({ sections: [] }, 'v2')).toEqual({ version: 'v2', sections: [] });
    expect(parseCut({ sections: 'x' }, 'v2')).toBeNull();
    expect(parseCut(null)).toBeNull();
    expect(labelOfCutFile('v0/cuts/v3.json')).toBe('v3');
    expect(labelOfCutFile('v0/cuts/x/v3.json')).toBeNull();
  });

  it('order versions newest first, drafts before their final', () => {
    expect(versionOrder('v4-draft2')).toEqual([4, 2]);
    expect(['v3', 'v4', 'v4-draft1', 'v10', 'v4-draft2'].sort(newerFirst)).toEqual([
      'v10',
      'v4',
      'v4-draft2',
      'v4-draft1',
      'v3',
    ]);
  });
});

describe('the script chips', () => {
  it('give each heading the newest version that covers it, and where', () => {
    const c = chips(['Cold open', 'The trick', 'Outro', 'New beat'], [v1, v3]);
    expect(c.get('Cold open')).toEqual({ version: 'v1', t: 0, text: 'v1 0:00' });
    expect(c.get('The trick')).toEqual({ version: 'v3', t: 12.4, text: 'v3 0:12' });
    expect(c.get('Outro')?.text).toBe('v3 1:01');
    expect(c.has('New beat')).toBe(false);
  });

  it('match headings regardless of case and spacing', () => {
    expect(chips(['the  TRICK'], [v1]).get('the  TRICK')?.version).toBe('v1');
  });
});

describe('the playhead section', () => {
  it('is the one whose span holds the time', () => {
    expect(sectionAt(v3, 12.4)?.heading).toBe('The trick');
    expect(sectionAt(v3, 60.99)?.heading).toBe('The trick');
    expect(sectionAt(v3, 61)?.heading).toBe('Outro');
    expect(sectionAt(v3, 70)?.heading).toBe('Outro');
  });

  it('is none before, after or between sections, or without a cut', () => {
    expect(sectionAt(v3, 3)).toBeNull();
    expect(sectionAt(v3, 75)).toBeNull();
    expect(sectionAt(null, 3)).toBeNull();
    expect(sectionAt(v3, NaN)).toBeNull();
  });

  it("a heading the version doesn't cover has no section", () => {
    expect(sectionFor(v3, 'Cold open')).toBeNull();
    expect(sectionFor(v3, 'outro')?.from).toBe(61);
  });
});
