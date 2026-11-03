import { describe, expect, it } from 'vitest';
import {
  draftOf,
  latestVersion,
  verName,
  byLabel,
  catchUp,
  coverMessage,
  covers,
  failedToast,
  flipPair,
  flipRefusal,
  flipTarget,
  flipToast,
  fromStartMessage,
  incomplete,
  landing,
  latestPlayable,
  neighbor,
  newlyStreamed,
  partner,
  pickerEntries,
  renderedEnd,
  revisionOf,
  streamChip,
  versionTitle,
  withStream,
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

  it('pairs a draft with the version it is an edit of, so C shows what the agent changed', () => {
    const drafts = [
      { label: 'v4', n: 4, duration: 40 },
      { label: 'v5-draft1', n: 5, duration: 40 },
      { label: 'v5-draft2', n: 5, duration: 40 },
    ];
    expect(partner(drafts, 'v5-draft2')?.label).toBe('v4');
    expect(partner(drafts, 'v5-draft1')?.label).toBe('v4'); // not the newer draft
    expect(partner(drafts, 'v4')?.label).toBe('v5-draft1'); // a version still compares with the next newer entry
    expect(partner([drafts[1]!], 'v5-draft1')).toBeNull(); // nothing before it: no partner
    expect(flipTarget(drafts, 'v5-draft2', false)?.label).toBe('v4');
    expect(flipTarget(drafts, 'v5-draft2', true)?.label).toBe('v5-draft2');
  });
});

describe('C back while following the latest', () => {
  const growing = [
    { label: 'v4', n: 4, duration: 40 },
    { label: 'v5-draft1', n: 5, duration: 40, stream: { state: 'complete', done: 6, total: 6, seconds: 40 } },
    { label: 'v5-draft2', n: 5, duration: 40, stream: { state: 'rendering', done: 2, total: 6, seconds: 12 } },
  ] as const;

  it('the latest playable is the newest that has the moment', () => {
    expect(latestPlayable(growing, 5)?.label).toBe('v5-draft2');
    expect(latestPlayable(growing, 20)?.label).toBe('v5-draft1'); // draft2 hasn't rendered 0:20 yet
    const stopped = [growing[0], { ...growing[2], stream: { ...growing[2].stream, state: 'stopped' as const } }];
    expect(latestPlayable(stopped, 5)?.label).toBe('v4');
  });

  it('lands on the newest when following, else on the base', () => {
    expect(catchUp(growing, 'v5-draft1', true, 5)?.label).toBe('v5-draft2'); // a newer draft came while comparing
    expect(catchUp(growing, 'v5-draft2', true, 5)).toBeNull(); // the base is the newest: back to it
    expect(catchUp(growing, 'v4', false, 5)).toBeNull(); // pinned on v4: back to v4
    // The base was a draft the family stopped listing (promoted, or replaced): the newest, even when pinned.
    expect(catchUp(growing, 'v5-draft0', false, 5)?.label).toBe('v5-draft2');
  });
});

describe('past-the-end checks (C or the picker onto a short preview)', () => {
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
    const drafts = [
      { label: 'v4', n: 4, duration: 40 },
      { label: 'v5-draft2', n: 5, duration: 40 },
    ];
    expect(flipToast(drafts, 'v4', drafts[1]!, true)).toBe('v5 (newer)');
    expect(flipToast(drafts, 'v5-draft2', drafts[0]!, true)).toBe('v4 (previous)');
    expect(flipToast(drafts, 'v5-draft2', drafts[1]!, false)).toBe('v5');
  });
  it('titles the version button', () => {
    expect(versionTitle(fam, 'v6', 'v6', false)).toBe('Version v6. C flips to v7');
    expect(versionTitle(fam, 'v6', 'v7', true)).toBe('Comparing: showing v7. C goes back to v6.');
    expect(versionTitle([fam[0]], 'v5', 'v5', false)).toBe('Version v5');
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

// Streamed versions (spec/bridge.md, "Streaming"): v8 will run 3:02, 0:41 of it is playable so far; v9 is pending
// with nothing rendered; v5 is a stream that completed.
const rendering = { state: 'rendering', done: 12, total: 50, seconds: 41.3 } as const;
const streaming = [
  { label: 'v5', n: 5, duration: 182, stream: { state: 'complete', done: 50, total: 50, seconds: 182 } as const },
  { label: 'v6', n: 6, duration: 182 },
  { label: 'v7', n: 7, duration: 182 },
  { label: 'v8', n: 8, duration: 182, stream: rendering },
  { label: 'v9', n: 9, duration: 182, stream: { state: 'pending', done: 0, total: 50, seconds: 0 } as const },
];

describe('an incomplete stream', () => {
  const [v5, v6, , v8, v9] = streaming as [
    (typeof streaming)[0],
    (typeof streaming)[1],
    (typeof streaming)[2],
    (typeof streaming)[3],
    (typeof streaming)[4],
  ];
  it('covers only what it has rendered; complete streams and files cover everything', () => {
    expect(incomplete(v8) && incomplete(v9)).toBe(true);
    expect(incomplete(v5) || incomplete(v6) || incomplete(null)).toBe(false);
    expect(renderedEnd(v8)).toBe(41.3);
    expect(renderedEnd(v9)).toBe(0);
    expect(renderedEnd(v5)).toBeNull();
    expect(renderedEnd(v6)).toBeNull();
    expect(covers(v8, 41.2)).toBe(true);
    expect(covers(v8, 41.3)).toBe(false);
    expect(covers(v9, 0)).toBe(false); // nothing playable yet
    expect(covers(v5, 150)).toBe(true);
  });

  it('is never refused: a moment it has rendered lands there, any other at its start, saying so', () => {
    expect(landing(v8, 30, null)).toEqual({ t: 30 });
    expect(landing(v8, 90, null)).toEqual({ t: 0, note: 'v8 is rendering: 0:41 of 3:02 so far, from the start' });
    expect(fromStartMessage(v9)).toBe('v9 is rendering: 0:00 of 3:02 so far, from the start');
    expect(landing(v9, 0, null)).toEqual({ t: 0 }); // the start anyway: nothing to say
    expect(landing(v9, 0.03, null)).toEqual({ t: 0 });
    expect(landing(v9, 12, null)).toMatchObject({ t: 0, note: expect.stringContaining('from the start') });
    expect(flipRefusal(streaming, 'v7', false, 90)).toBeNull();
    expect(flipRefusal(streaming, 'v8', false, 90)).toBeNull();
    // A preview still refuses what it doesn't cover.
    expect(landing({ label: 'v3', duration: 10 }, 20, null)).toEqual({ refuse: 'v3 only covers 0:00–0:10' });
  });

  it('has a chip in the version list until it is complete', () => {
    expect(streamChip(v8)).toBe('rendering 12/50');
    expect(streamChip(v9)).toBe('pending');
    expect(streamChip({ stream: { state: 'rendering', done: 0, total: 0, seconds: 0 } })).toBe('rendering');
    expect(streamChip(v5)).toBeNull();
    expect(streamChip(v6)).toBeNull();
  });

  it('takes stream states in place, only for streamed versions', () => {
    const s = { state: 'rendering', done: 13, total: 50, seconds: 44 } as const;
    const next = withStream(streaming, 'v8', s);
    expect(next).not.toBe(streaming);
    expect(next[3]).toMatchObject({ label: 'v8', stream: s });
    expect(next[0]).toBe(streaming[0]); // the others stay as they were
    expect(streaming[3]!.stream).toBe(rendering); // not mutated
    expect(withStream(next, 'v8', { ...s })).toBe(next); // nothing new
    expect(withStream(streaming, 'v6', s)).toBe(streaming); // not a streamed version
    expect(withStream(streaming, 'v10', s)).toBe(streaming); // unknown
    const done = withStream(streaming, 'v9', { state: 'complete', done: 50, total: 50, seconds: 182 });
    expect(incomplete(done[4])).toBe(false);
  });

  it('is announced once when it is pushed', () => {
    expect(newlyStreamed(streaming, new Set()).map((v) => v.label)).toEqual(['v8', 'v9']);
    expect(newlyStreamed(streaming, new Set(['v8', 'v9']))).toEqual([]);
    expect(failedToast('v9', 'ffmpeg failed')).toBe("v9's render failed: ffmpeg failed");
    expect(failedToast('v9')).toBe("v9's render failed");
  });
});

describe('drafts (C4) read as the version they head for', () => {
  it('names them quietly', () => {
    expect(draftOf('v5-draft2')).toBe('v5');
    expect(draftOf('v5')).toBeNull();
    expect(draftOf('v5_draft')).toBeNull();
    expect(draftOf(null)).toBeNull();
    expect(verName('v5-draft2')).toBe('v5'); // never "draft": it's the live v5
    expect(verName('v12')).toBe('v12');
  });
  it('Latest is the newest real version', () => {
    expect(latestVersion([{ label: 'v3' }, { label: 'v4' }, { label: 'v5-draft1' }])?.label).toBe('v4');
    expect(latestVersion([{ label: 'v1-draft1' }])).toBeUndefined();
  });
});

describe('the live version (spec/automation.md, "The live version")', () => {
  const fam = [
    { label: 'v3', n: 3, duration: 40 },
    { label: 'v4', n: 4, duration: 40 },
    { label: 'v5-draft1', n: 5, duration: 40, stream: { state: 'complete' as const, done: 3, total: 3, seconds: 40 } },
    { label: 'v5-draft2', n: 5, duration: 40, stream: { state: 'rendering' as const, done: 1, total: 3, seconds: 12 } },
  ];
  it('the picker has one entry per version: its newest revision', () => {
    expect(pickerEntries(fam).map((v) => v.label)).toEqual(['v3', 'v4', 'v5-draft2']);
    // Updating v4 in place: the live revision stands for v4.
    expect(pickerEntries([...fam.slice(0, 2), { label: 'v4-draft3', n: 4, duration: 40 }]).map((v) => v.label)).toEqual(
      ['v3', 'v4-draft3'],
    );
    // An extension's version is never folded.
    expect(pickerEntries([{ label: 'v0', kind: 'scripts.doc' }, { label: 'v1' }]).map((v) => v.label)).toEqual([
      'v0',
      'v1',
    ]);
  });
  it('showing v5 plays its newest revision that has the moment', () => {
    expect(revisionOf(fam, 'v5', 5)?.label).toBe('v5-draft2');
    expect(revisionOf(fam, 'v5', 30)?.label).toBe('v5-draft1'); // draft2 hasn't rendered 0:30 yet
    expect(revisionOf(fam, 'v4', 30)?.label).toBe('v4');
    expect(revisionOf(fam, 'v9', 0)).toBeUndefined();
  });
});
