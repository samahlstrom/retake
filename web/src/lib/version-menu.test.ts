import { describe, expect, it } from 'vitest';
import { versionStrip } from './notes/view';
import { menuDot, menuRows, menuTitle } from './version-menu';
import { verName } from './versions';

const name = (v: { label: string; kind?: string }) => (v.kind ? 'Script' : verName(v.label));
// The family as /api/info lists it: v0 (a script), three cuts, and v4 live (two of its revisions listed).
const family = [
  { label: 'v0', kind: 'scripts.doc', duration: null },
  { label: 'v1', duration: 63.5 },
  { label: 'v2', duration: 71.15 },
  { label: 'v3', duration: 58.3 },
  { label: 'v4-draft10', duration: 55 },
  { label: 'v4-draft11', duration: 55.3 },
];
const todo = new Map([
  ['v0', 3],
  ['v3', 15],
]);

describe('the version menu', () => {
  it('lists every version newest first, a version and its revisions as one row, then Script set apart', () => {
    const rows = menuRows(versionStrip(family, 'v4-draft10', todo, name), family, 'v4');
    expect(rows.map((r) => [r.name, r.label, r.kind])).toEqual([
      ['v4', 'v4-draft11', false], // what a pick shows: the newest revision
      ['v3', 'v3', false],
      ['v2', 'v2', false],
      ['v1', 'v1', false],
      ['Script', 'v0', true], // last, by its kind's label alone
    ]);
    expect(rows.every((r) => !/draft|Latest|→|·/.test(r.name + r.detail))).toBe(true);
  });

  it('marks the live one with its dot and the one on screen (bold), once each', () => {
    const rows = menuRows(versionStrip(family, 'v4-draft10', todo, name), family, 'v4');
    expect(rows.filter((r) => r.live).map((r) => r.name)).toEqual(['v4']);
    expect(rows.filter((r) => r.on).map((r) => r.name)).toEqual(['v4']);
    const onScript = menuRows(versionStrip(family, 'v0', todo, name), family, null);
    expect(onScript.filter((r) => r.on).map((r) => r.name)).toEqual(['Script']);
    expect(onScript.some((r) => r.live)).toBe(false); // nothing runs: no dot
  });

  it('has at most one quiet detail per row: what is still to do, else the length; a script never a length', () => {
    const rows = menuRows(versionStrip(family, 'v2', todo, name), family, 'v4');
    expect(Object.fromEntries(rows.map((r) => [r.name, r.detail]))).toEqual({
      v4: '0:55', // the live one: its newest revision's length
      v3: '15 to do',
      v2: '1:11',
      v1: '1:03',
      Script: '3 to do',
    });
    const quiet = menuRows(versionStrip(family, 'v2', new Map(), name), family, null);
    expect(quiet.at(-1)).toMatchObject({ name: 'Script', detail: '' });
  });

  it("says a streamed version's state while it renders, unless it is the live one (its dot says it)", () => {
    const streamed = [
      { label: 'v1', duration: 40 },
      { label: 'v2', duration: 40, stream: { state: 'rendering' as const, done: 3, total: 12, seconds: 9 } },
      { label: 'v3-draft1', duration: 40, stream: { state: 'pending' as const, done: 0, total: 12, seconds: 0 } },
    ];
    const rows = menuRows(versionStrip(streamed, 'v1', new Map([['v2', 2]]), name), streamed, 'v3');
    expect(rows.map((r) => [r.name, r.detail])).toEqual([
      ['v3', '0:40'],
      ['v2', 'rendering 3/12'],
      ['v1', '0:40'],
    ]);
  });

  it('is empty for a single version: nothing to choose', () => {
    const one = [{ label: 'v1', duration: 10 }];
    expect(menuRows(versionStrip(one, 'v1', new Map(), name), one, null)).toEqual([]);
  });

  it("the button's one dot: live, else rendering, else earlier comments to do", () => {
    const rendering = { stream: { state: 'rendering' as const, done: 1, total: 4, seconds: 2 } };
    expect(menuDot({ shownLive: true, shown: rendering, earlier: 2 })).toBe('live');
    expect(menuDot({ shownLive: false, shown: rendering, earlier: 2 })).toBe('rendering');
    expect(menuDot({ shownLive: false, shown: { stream: undefined }, earlier: 2 })).toBe('earlier');
    expect(menuDot({ shownLive: false, shown: null, earlier: 0 })).toBe(null);
  });

  it("the button's title", () => {
    expect(menuTitle({ name: 'v4', flipTo: 'v3', flipped: null, earlier: [] })).toBe('Version v4. C flips to v3');
    expect(
      menuTitle({
        name: 'v4',
        flipTo: 'v3',
        flipped: null,
        earlier: [
          { version: 'v3', todo: 15 },
          { version: 'v1', todo: 2 },
        ],
      }),
    ).toBe('Version v4. C flips to v3. Open on earlier versions: v3 · 15, v1 · 2');
    expect(menuTitle({ name: 'v3', flipTo: null, flipped: { shown: 'v3', base: 'v4' }, earlier: [] })).toBe(
      'Comparing: showing v3. C goes back to v4',
    );
  });
});
