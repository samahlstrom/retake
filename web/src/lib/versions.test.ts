import { describe, expect, it } from 'vitest';
import {
  byLabel,
  coverMessage,
  covers,
  flipPair,
  flipRefusal,
  flipTarget,
  flipToast,
  landing,
  neighbor,
  partner,
  stepTarget,
  versionTitle,
} from './versions';
import { mapTime, type Segment } from './timemap';

const fam = [
  { label: 'v5', n: 5, duration: 40 },
  { label: 'v6', n: 6, duration: 40 },
  { label: 'v7', n: 7, duration: 10 },
];

describe('family navigation', () => {
  it('finds neighbors in family order', () => {
    expect(neighbor(fam, 'v6', 1)?.label).toBe('v7');
    expect(neighbor(fam, 'v6', -1)?.label).toBe('v5');
    expect(neighbor(fam, 'v7', 1)).toBeNull();
    expect(neighbor(fam, 'v5', -1)).toBeNull();
    expect(neighbor(fam, 'v9', 1)).toBeNull();
    expect(byLabel(fam, 'v5')?.n).toBe(5);
  });

  it('pairs C with the next newer version, else the previous one', () => {
    expect(partner(fam, 'v6')?.label).toBe('v7');
    expect(partner(fam, 'v5')?.label).toBe('v6');
    expect(partner(fam, 'v7')?.label).toBe('v6');
    expect(partner([fam[0]], 'v5')).toBeNull();
  });

  it('flips to the partner, and back to the base when flipped', () => {
    expect(flipTarget(fam, 'v6', false)?.label).toBe('v7');
    expect(flipTarget(fam, 'v6', true)?.label).toBe('v6');
    expect(flipTarget([fam[1]], 'v6', false)).toBeNull(); // "Only one version"
  });

  it('steps [ and ], refusing past either end', () => {
    expect(stepTarget(fam, 'v6', -1)).toMatchObject({ label: 'v5' });
    expect(stepTarget(fam, 'v6', 1)).toMatchObject({ label: 'v7' });
    expect(stepTarget(fam, 'v5', -1)).toBe('No earlier version');
    expect(stepTarget(fam, 'v7', 1)).toBe('No later version');
  });
});

describe('past-the-end checks (C and ] onto a short preview)', () => {
  it('covers a time up to 50 ms before the end', () => {
    expect(covers(fam[2], 9.9)).toBe(true);
    expect(covers(fam[2], 9.95)).toBe(true);
    expect(covers(fam[2], 9.96)).toBe(false);
    expect(covers(fam[2], 10.3)).toBe(false);
  });
  it('covers everything when the duration is unknown', () => {
    expect(covers({ duration: null }, 1e6)).toBe(true);
    expect(covers({ duration: 0 }, 5)).toBe(true);
  });
  it('says what the version covers', () => {
    expect(coverMessage(fam[2])).toBe('v7 only covers 0:00–0:10');
    expect(coverMessage({ label: 'v3', duration: 190.5 })).toBe('v3 only covers 0:00–3:10');
  });
});

describe('labels', () => {
  it('toasts newer/previous while flipped, the label when back', () => {
    expect(flipToast(fam, 'v6', fam[2], true)).toBe('v7 (newer)');
    expect(flipToast(fam, 'v7', fam[1], true)).toBe('v6 (previous)');
    expect(flipToast(fam, 'v6', fam[1], false)).toBe('v6');
  });
  it('titles the version button', () => {
    expect(versionTitle(fam, 'v6', 'v6', false)).toBe('Version v6. [ and ] step, C flips to v7');
    expect(versionTitle(fam, 'v6', 'v7', true)).toBe('Comparing: showing v7. C goes back to v6.');
    expect(versionTitle([fam[0]], 'v5', 'v5', false)).toBe('Version v5. [ and ] step');
  });
});

describe('the flip button', () => {
  it('toggles between the base and its partner', () => {
    expect(flipPair(fam, 'v6')).toEqual(['v6', 'v7']);
    expect(flipPair(fam, 'v7')).toEqual(['v7', 'v6']); // the newest compares with the previous one
    expect(flipPair(fam, 'v5')).toEqual(['v5', 'v6']);
    expect(flipPair([fam[1]], 'v6')).toBeNull();
    expect(flipPair(fam, null)).toBeNull();
  });

  it('is refused exactly when C is, with the same toast', () => {
    expect(flipRefusal(fam, 'v6', false, 5)).toBeNull();
    expect(flipRefusal(fam, 'v6', false, 9.9)).toBeNull();
    expect(flipRefusal(fam, 'v6', false, 9.97)).toBe('v7 only covers 0:00–0:10'); // within 0.05 s of its end
    expect(flipRefusal(fam, 'v6', false, 12)).toBe('v7 only covers 0:00–0:10');
    expect(flipRefusal(fam, 'v6', true, 9)).toBeNull(); // back to the base always works inside it
    expect(flipRefusal(fam, 'v7', false, 30)).toBeNull(); // v7's partner v6 covers 0:30
    expect(flipRefusal([fam[1]], 'v6', false, 5)).toBe('Only one version');
  });
});

// The demo's v1 (16.1 s) and v2 (15.8 s): 0.3 s of VO trimmed at 3.6 s, so v2 runs 0.3 s earlier after it.
const demo = [
  { label: 'v1', n: 1, duration: 16.1 },
  { label: 'v2', n: 2, duration: 15.8 },
];
const v1ToV2: Segment[] = [
  { a: [0, 3.6], b: [0, 3.6] },
  { a: [3.6, 3.9], b: [3.599, 3.599] },
  { a: [3.9, 16.1], b: [3.6, 15.8] },
];

describe('the time-mapped flip', () => {
  it('lands on the matching moment, not the same clock time', () => {
    expect(landing(demo[1], 9, v1ToV2)).toEqual({ t: 8.7 });
    expect(landing(demo[1], 2, v1ToV2)).toEqual({ t: 2 }); // before the trim, nothing moved
    expect(landing(demo[1], 3.8, v1ToV2)).toEqual({ t: 3.599 }); // trimmed air: the cut
  });

  it('keeps the time without a map', () => {
    expect(landing(demo[1], 9, null)).toEqual({ t: 9 });
    expect(landing(demo[1], 9, [])).toEqual({ t: 9 });
  });

  it('is refused only when the matching moment is past the other version', () => {
    // 15.9 s on v1 is past v2's end as a clock time, but its matching moment, 15.6 s, is inside v2.
    expect(landing(demo[1], 15.9, null)).toEqual({ refuse: 'v2 only covers 0:00–0:15' });
    expect(landing(demo[1], 15.9, v1ToV2)).toEqual({ t: 15.6 });
    const mapT = (label: string, t: number) => (label === 'v2' ? mapTime(v1ToV2, t) : t);
    expect(flipRefusal(demo, 'v1', false, 15.9)).toBe('v2 only covers 0:00–0:15');
    expect(flipRefusal(demo, 'v1', false, 15.9, mapT)).toBeNull();
    // A preview that ends early still refuses a moment it doesn't have.
    const preview = { label: 'v2', n: 2, duration: 5 };
    expect(landing(preview, 9, v1ToV2)).toEqual({ refuse: 'v2 only covers 0:00–0:05' });
  });
});
