import { describe, expect, it } from 'vitest';
import {
  cardExt,
  extHash,
  iconUrl,
  kindOf,
  kindRecords,
  kindStatus,
  navSections,
  openingVersion,
  sidebarTabs,
  versionName,
  versionSlot,
} from './kinds';
import type { ExtInfo } from './loader';
import type { Card } from '../lib/home';

const scripts: ExtInfo = {
  id: 'scripts',
  name: 'Scripts',
  version: '1.0.0',
  base: 'ext/scripts/',
  module: 'ui/scripts.js',
  style: 'ui/scripts.css',
  nav: { title: 'Scripts', icon: 'ui/script.svg', list: 'kind-only', action: { title: 'New script', route: 'new' } },
  kinds: [
    {
      id: 'scripts.doc',
      kind: 'doc',
      folder: 'v0',
      marker: ['*_v0.md'],
      label: 'Script',
      status: 'Idea',
      record: true,
    },
  ],
  slots: [
    { slot: 'page', kind: '', title: '', when: 'always' },
    { slot: 'version', kind: 'scripts.doc', title: '', when: 'always' },
    { slot: 'sidebar-tab', kind: '', title: 'Script', when: 'has-kind' },
  ],
  machine: true,
};
const hello: ExtInfo = {
  id: 'hello',
  name: 'Hello',
  version: '0.1.0',
  base: 'ext/hello/',
  module: 'ui/hello.js',
  style: '',
  nav: { title: 'Hello', icon: '', list: '', action: null },
  kinds: [],
  slots: [
    { slot: 'sidebar-tab', kind: '', title: '', when: 'always' },
    { slot: 'review-panel', kind: '', title: 'Board', when: 'always' },
  ],
  machine: true,
};
const exts = [scripts, hello];

const card = (name: string, extra: Partial<Card> = {}): Card => ({
  name,
  title: name,
  video: name,
  path: `deadlock/${name}`,
  project: 'deadlock',
  url: `p/${name}/`,
  version: 'v1',
  versions: 1,
  duration: 10,
  pushed: '',
  status: 'to_review',
  takes: 0,
  open: 0,
  questions: 0,
  stream: null,
  thumb: '',
  ...extra,
});

describe('kinds of versions', () => {
  it('name a version `v0 · <label>`, and find its extension, slot and Record', () => {
    expect(kindOf(exts, 'scripts.doc')?.ext.id).toBe('scripts');
    expect(kindOf(exts, 'other.doc')).toBeNull();
    expect(versionName(exts, { label: 'v0', kind: 'scripts.doc' })).toBe('v0 · Script');
    expect(versionName(exts, { label: 'v0', kind: 'gone.doc' })).toBe('v0'); // its extension isn't on
    expect(versionName(exts, { label: 'v3' })).toBe('v3');
    expect(versionSlot(exts, 'scripts.doc')?.id).toBe('scripts');
    expect(versionSlot(exts, null)).toBeNull();
    expect(versionSlot([hello], 'scripts.doc')).toBeNull();
    expect(kindRecords(exts, 'scripts.doc')).toBe(true);
    const quiet = { ...scripts, kinds: [{ ...scripts.kinds[0]!, record: false }] };
    expect(kindRecords([quiet], 'scripts.doc')).toBe(false);
  });

  it('open the newest cut as always; a v0-only video on v0; a deep link where it says', () => {
    const v0 = { label: 'v0', kind: 'scripts.doc' };
    expect(openingVersion([v0, { label: 'v1' }, { label: 'v2' }], 'v2', null)).toBe('v2');
    expect(openingVersion([v0], 'v0', null)).toBe('v0');
    expect(openingVersion([v0], null, null)).toBe('v0');
    // a cut that can't play yet (a stream with nothing rendered): the current one, never v0
    expect(openingVersion([v0, { label: 'v1', playable: false }], 'v1', null)).toBe('v1');
    expect(openingVersion([v0, { label: 'v1' }], 'v1', 'v0')).toBe('v0');
    expect(openingVersion([v0, { label: 'v1' }], 'v1', 'v9')).toBe('v1'); // not in the family
    expect(openingVersion([], 'v1', null)).toBe('v1');
  });
});

describe("the comments column's extension tabs", () => {
  it('come after To do · Done on a video version; has-kind only with a version of its kind', () => {
    const withV0 = [{ label: 'v0', kind: 'scripts.doc' }, { label: 'v1' }];
    expect(sidebarTabs(exts, withV0, { kind: null, phone: false }).map((t) => [t.key, t.title])).toEqual([
      ['scripts/sidebar-tab', 'Script'],
      ['hello/sidebar-tab', 'Hello'],
      ['hello/review-panel', 'Board'],
    ]);
    expect(sidebarTabs(exts, [{ label: 'v1' }], { kind: null, phone: false }).map((t) => t.key)).toEqual([
      'hello/sidebar-tab',
      'hello/review-panel',
    ]);
  });

  it('are not on a phone (a review panel) or on a kind’s version (its slot is the page)', () => {
    expect(sidebarTabs(exts, [{ label: 'v1' }], { kind: null, phone: true }).map((t) => t.key)).toEqual([
      'hello/sidebar-tab',
    ]);
    expect(sidebarTabs(exts, [{ label: 'v0', kind: 'scripts.doc' }], { kind: 'scripts.doc', phone: false })).toEqual(
      [],
    );
  });
});

describe('the home', () => {
  it("nav sections: kind-only rows are its kind's ideas, the most recently changed first", () => {
    const cards = [
      card('cut', { kinds: ['scripts.doc'], changed: 900 }), // has a cut: not an idea
      card('old-idea', { status: 'no_cut', kind: 'scripts.doc', version: 'v0', changed: 100 }),
      card('other-kind', { status: 'no_cut', kind: 'board.card', changed: 999 }),
      card('new-idea', { status: 'no_cut', kind: 'scripts.doc', version: 'v0', changed: 500 }),
      card('undated', { status: 'no_cut', kind: 'scripts.doc' }),
    ];
    const [ign, hel] = navSections(exts, cards);
    expect(ign!.title).toBe('Scripts');
    expect(ign!.icon).toBe('ext/scripts/ui/script.svg');
    expect(ign!.action).toEqual({ title: 'New script', route: 'new' });
    expect(ign!.rows.map((c) => c.name)).toEqual(['new-idea', 'old-idea', 'undated']);
    expect(hel).toMatchObject({ title: 'Hello', icon: '', action: null, rows: [] }); // no list
    expect(navSections([{ ...hello, nav: null }], cards)).toEqual([]);
  });

  it("an idea's card says its kind's word, and the tree shows its extension's icon", () => {
    const idea = card('idea', { status: 'no_cut', kind: 'scripts.doc' });
    expect(kindStatus(exts, idea)).toBe('Idea');
    expect(kindStatus([], idea)).toBe('No cut yet');
    expect(kindStatus(exts, card('cut'))).toBeNull();
    expect(cardExt(exts, idea)?.id).toBe('scripts');
    expect(cardExt(exts, card('cut', { kinds: ['scripts.doc'] }))).toBeNull();
    expect(iconUrl(scripts)).toBe('ext/scripts/ui/script.svg');
    expect(iconUrl(hello)).toBe('');
    expect(extHash('scripts')).toBe('#ext/scripts/');
    expect(extHash('scripts', '/new')).toBe('#ext/scripts/new');
  });
});
