import { describe, expect, it } from 'vitest';
import type { Card, TreeNode } from './home';
import {
  ancestors,
  backPlace,
  dotOf,
  findNode,
  hashOf,
  navKey,
  parseExtHash,
  parseHash,
  rememberedPlace,
  rows,
  topDot,
  videosUnder,
  within,
} from './tree';

const card = (
  name: string,
  path: string,
  project: string,
  status: Card['status'],
  extra: Partial<Card> = {},
): Card => ({
  name,
  title: path.split('/').pop() || 'top',
  video: name,
  path,
  project,
  url: `p/${name}/`,
  version: 'v1',
  versions: 1,
  duration: 10,
  pushed: '',
  status,
  takes: 0,
  open: 0,
  questions: 0,
  stream: null,
  thumb: '',
  ...extra,
});

const node = (path: string, project: boolean, videos: Card[], children: TreeNode[] = []): TreeNode => ({
  title: path.split('/').pop() || 'deadlock',
  path,
  project,
  videos,
  children,
});

const intro = card('intro', '', '', 'reviewed');
const fullgame = card('fullgame', '2026-10-19/fullgame', '2026-10-19/fullgame', 'to_review');
const shorts = card('aura', '2026-10-19/shorts', '2026-10-19/shorts', 'being_edited', { questions: 1 });
const shop = card('itemshop', 'item-shop', 'item-shop', 'being_edited');
const honored = card('honored', 'item-shop/shorts/honored', 'item-shop', 'reviewed');
const tree = node(
  '',
  false,
  [intro],
  [
    node(
      '2026-10-19',
      false,
      [],
      [node('2026-10-19/fullgame', true, [fullgame]), node('2026-10-19/shorts', true, [shorts])],
    ),
    node('drafts', false, [], [node('drafts/new-cut', true, [])]),
    node('item-shop', true, [shop, honored]),
  ],
);

/** The rows as "  title (kind) dot", indented by depth; ▸ closed, ▾ open. */
const draw = (open: string[], filter = '') =>
  rows(tree, new Set(open), filter).map((r) => {
    const mark = r.canOpen ? (r.open ? '▾ ' : '▸ ') : '';
    return `${'  '.repeat(r.depth)}${mark}${r.title}${r.dot ? ` ·${r.dot}` : ''}`;
  });

describe('the folder tree', () => {
  it('finds places, the videos in them and the folders above them', () => {
    expect(findNode(tree, '')).toBe(tree);
    expect(findNode(tree, '2026-10-19/shorts')?.videos).toEqual([shorts]);
    expect(findNode(tree, '2026-10-19/nope')).toBeNull();
    expect(findNode(tree, 'item-shop/shorts')).toBeNull(); // a project's folders aren't places
    expect(videosUnder(findNode(tree, '2026-10-19')!).map((c) => c.name)).toEqual(['fullgame', 'aura']);
    expect(videosUnder(tree)).toHaveLength(5);
    expect(ancestors('a/b/c')).toEqual(['a', 'a/b']);
    expect(ancestors('a')).toEqual([]);
    expect(ancestors('')).toEqual([]);
    expect(within('item-shop/shorts/honored', 'item-shop')).toBe(true);
    expect(within('item-shop-retake', 'item-shop')).toBe(false);
    expect(within('anything', '')).toBe(true);
  });

  it('shows closed folders with their most pressing dot, and what is in the open ones', () => {
    expect(draw([])).toEqual(['intro', '▸ 2026-10-19 ·question', '▸ drafts', '▸ item-shop ·being_edited']);
    expect(draw(['2026-10-19', 'item-shop', 'drafts'])).toEqual([
      'intro',
      '▾ 2026-10-19',
      '  ▸ fullgame ·to_review',
      '  ▸ shorts ·question',
      '▾ drafts',
      '  new-cut', // a project with nothing in it: nothing to open
      '▾ item-shop',
      '  itemshop ·being_edited',
      '  honored',
    ]);
    const r = rows(tree, new Set(['item-shop']));
    const v = r.find((x) => x.key === 'v:honored')!;
    expect([v.kind, v.path, v.parent, v.card]).toEqual(['video', 'item-shop', 'item-shop', honored]);
    expect(r.find((x) => x.key === 'item-shop')?.kind).toBe('project');
    expect(r.find((x) => x.key === '2026-10-19')?.kind).toBe('folder');
  });

  it('filters: matches and the way to them, opened; a matching folder keeps its own state', () => {
    expect(draw([], 'HONOR')).toEqual(['▾ item-shop', '  honored']);
    // A project's video also by the folders it's in: item-shop/shorts/honored.
    expect(draw([], 'shorts/')).toEqual(['▾ item-shop', '  honored']);
    expect(draw([], 'aura')).toEqual(['▾ 2026-10-19', '  ▾ shorts', '    aura ·question']);
    // The folder's own name matches: it shows as it is (closed here), with what's in it when opened.
    expect(draw([], '2026')).toEqual(['▸ 2026-10-19 ·question']);
    expect(draw(['2026-10-19'], '2026')).toEqual(['▾ 2026-10-19', '  ▸ fullgame ·to_review', '  ▸ shorts ·question']);
    expect(draw([], 'intro')).toEqual(['intro']);
    expect(draw([], 'zzz')).toEqual([]);
    expect(draw([], '  ')).toEqual(draw([]));
  });

  it('dots: questions, then to review, then being edited; reviewed has none', () => {
    expect(dotOf(shorts)).toBe('question');
    expect(dotOf(fullgame)).toBe('to_review');
    expect(dotOf(shop)).toBe('being_edited');
    expect(dotOf(honored)).toBe('');
    expect(topDot([honored, shop, fullgame])).toBe('to_review');
    expect(topDot([])).toBe('');
  });

  it('puts the selection in the URL hash', () => {
    expect(hashOf('')).toBe('');
    expect(hashOf('2026-10-19/full game')).toBe('#/2026-10-19/full%20game');
    expect(hashOf('a#b/c?d')).toBe('#/a%23b/c%3Fd');
    for (const p of ['', 'item-shop', '2026-10-19/full game', 'a#b/c?d', 'Café/x'])
      expect(parseHash(hashOf(p) || '#/')).toBe(p);
    expect(parseHash('')).toBe('');
    expect(parseHash('#/')).toBe('');
    expect(parseHash('#item-shop')).toBe(''); // not ours
    expect(parseHash('#//a//b/')).toBe('a/b');
    expect(parseHash('#/%E0%A4%A')).toBe('%E0%A4%A');
  });

  it("knows an extension's page in the hash: #ext/<id>/<route>, never a folder", () => {
    expect(parseExtHash('#ext/ignition/')).toEqual({ id: 'ignition', route: '' });
    expect(parseExtHash('#ext/ignition')).toEqual({ id: 'ignition', route: '' });
    expect(parseExtHash('#ext/ignition/new')).toEqual({ id: 'ignition', route: 'new' });
    expect(parseExtHash('#ext/ignition/idea/a%20b')).toEqual({ id: 'ignition', route: 'idea/a b' });
    expect(parseExtHash('ext/hello-2/x')).toEqual({ id: 'hello-2', route: 'x' });
    for (const h of ['', '#/', '#/ext/ignition', '#ext/', '#ext/Bad/x', '#extra/x', '#/deadlock'])
      expect(parseExtHash(h), h).toBeNull();
    expect(parseHash('#ext/ignition/new')).toBe(''); // the folder selection stays the top
  });

  it("sends a review's back link to where the reviewer was, when the video is there, else to its project", () => {
    expect(backPlace('2026-10-19', fullgame)).toBe('2026-10-19');
    expect(backPlace('', fullgame)).toBe('');
    expect(backPlace('item-shop', fullgame)).toBe('2026-10-19/fullgame');
    expect(backPlace(null, honored)).toBe('item-shop');
    const storage = new Map([[navKey('/'), JSON.stringify({ selected: '2026-10-19' })]]);
    const get = { getItem: (k: string) => storage.get(k) ?? null };
    expect(rememberedPlace(get, '/')).toBe('2026-10-19');
    expect(rememberedPlace(get, '/index.html')).toBe('2026-10-19');
    expect(rememberedPlace(get, '/other/')).toBeNull();
    expect(rememberedPlace({ getItem: () => '{bad' }, '/')).toBeNull();
    expect(rememberedPlace(null, '/')).toBeNull();
  });
});
