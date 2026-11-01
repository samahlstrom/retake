import { describe, expect, it } from 'vitest';
import { Home, NAV_MAX, NAV_MIN, NAV_WIDTH } from './home.svelte';

const answer = (status: number, body: unknown): Response =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

/** A Storage stand-in. */
function memory(init: Record<string, string> = {}) {
  const m = new Map(Object.entries(init));
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    saved: (k = 'retake.nav:/') => JSON.parse(m.get(k) ?? 'null'),
  };
}

const card = (name: string, path: string, project = path, status = 'to_review') => ({
  name,
  path,
  project,
  status,
});

/** A home like a video maker's: a dated folder with a project, and a project with a short. */
const body = {
  title: 'deadlock',
  role: 'owner',
  projects: [
    card('item-shop', 'item-shop'),
    card('item-shop-shorts-one', 'item-shop/shorts/one', 'item-shop', 'reviewed'),
    card('2026-10-19-fullgame', '2026-10-19/fullgame'),
  ],
  tree: {
    title: 'deadlock',
    path: '',
    project: false,
    videos: [],
    children: [
      {
        title: '2026-10-19',
        path: '2026-10-19',
        project: false,
        videos: [],
        children: [
          {
            title: 'fullgame',
            path: '2026-10-19/fullgame',
            project: true,
            videos: [{ name: '2026-10-19-fullgame' }],
            children: [],
          },
        ],
      },
      {
        title: 'item-shop',
        path: 'item-shop',
        project: true,
        videos: [{ name: 'item-shop' }, { name: 'item-shop-shorts-one' }],
        children: [],
      },
    ],
  },
};

describe('the home state', () => {
  it('reads api/home, and keeps the last cards when a read fails', async () => {
    const urls: string[] = [];
    let next: () => Promise<Response> = async () =>
      answer(200, { title: 'shows', role: 'owner', projects: [{ name: 'a', status: 'to_review' }] });
    const home = new Home({
      storage: null,
      fetch: (async (url: string) => {
        urls.push(url);
        return next();
      }) as typeof fetch,
    });
    expect(home.hook.loaded).toBe(false);
    await home.refresh();
    expect(urls).toEqual(['api/home']);
    expect(home.hook.loaded).toBe(true);
    expect(home.hook.projects.map((p) => p.name)).toEqual(['a']);
    expect(home.res.title).toBe('shows');
    // An older server sends no tree: every video is at the top.
    expect(home.tree.videos.map((c) => c.name)).toEqual(['a']);
    expect(home.videos.map((c) => c.name)).toEqual(['a']);

    next = async () => answer(401, { error: 'token required' });
    await home.refresh();
    expect(home.error).toMatch(/can’t open/);
    expect(home.hook.projects.map((p) => p.name)).toEqual(['a']);

    next = async () => {
      throw new TypeError('network');
    };
    await home.refresh();
    expect(home.hook.error).toBe('Review server not reachable');

    next = async () => answer(200, { projects: [] });
    await home.refresh();
    expect(home.error).toBe('');
    expect(home.hook.projects).toEqual([]);
  });

  it('remembers broken thumbnails', () => {
    const home = new Home({ storage: null, fetch: (async () => answer(200, {})) as typeof fetch });
    home.thumbFailed('a');
    home.thumbFailed('a');
    expect([...home.broken]).toEqual(['a']);
  });
});

describe('the folder tree', () => {
  it('selects a folder or project: its videos, its trail, and the folders above it opened', async () => {
    const storage = memory();
    const home = new Home({ storage, fetch: (async () => answer(200, body)) as unknown as typeof fetch });
    await home.refresh();
    expect(home.selected).toBe('');
    expect(home.node.path).toBe('');
    expect(home.videos.map((c) => c.name)).toEqual(['item-shop', 'item-shop-shorts-one', '2026-10-19-fullgame']);

    home.select('2026-10-19/fullgame');
    expect(home.node.title).toBe('fullgame');
    expect(home.videos.map((c) => c.name)).toEqual(['2026-10-19-fullgame']);
    expect(home.trail.map((n) => n.title)).toEqual(['deadlock', '2026-10-19', 'fullgame']);
    expect([...home.expanded]).toEqual(['2026-10-19', '2026-10-19/fullgame']);
    expect(home.hook.selected).toBe('2026-10-19/fullgame');

    home.select('item-shop');
    expect(home.videos.map((c) => c.name)).toEqual(['item-shop', 'item-shop-shorts-one']);
    // A selection that's gone shows the top.
    home.select('nope/gone');
    expect(home.node.path).toBe('');
    expect(home.videos).toHaveLength(3);
  });

  it('remembers the selection, the opened folders and the column in localStorage', () => {
    const storage = memory();
    const home = new Home({ storage });
    home.select('item-shop');
    home.toggle('2026-10-19');
    home.toggle('item-shop'); // closes it
    home.resize(9999);
    expect(storage.saved()).toEqual({
      selected: 'item-shop',
      top: true,
      expanded: ['2026-10-19'],
      width: NAV_MAX,
      hidden: false,
    });
    home.resize(10);
    expect(home.width).toBe(NAV_MIN);
    home.toggleNav();
    expect(home.hidden).toBe(true);
    home.toggleTop();
    expect(home.topOpen).toBe(false);

    const again = new Home({ storage });
    expect(again.topOpen).toBe(false);
    expect(again.selected).toBe('item-shop');
    expect([...again.expanded]).toEqual(['2026-10-19']);
    expect(again.width).toBe(NAV_MIN);
    expect(again.hidden).toBe(true);
    again.select('item-shop'); // selecting something inside opens the top again
    expect(again.topOpen).toBe(true);

    // Another home (path) has its own; a broken entry leaves the defaults.
    const other = new Home({ storage, path: '/other/' });
    expect(other.selected).toBe('');
    const broken = new Home({ storage: memory({ 'retake.nav:/': '{nope' }) });
    expect(broken.width).toBe(NAV_WIDTH);
    expect(
      new Home({ storage: memory({ 'retake.nav:/': '{"width":"x","expanded":[1,"a"]}' }) }).expanded.has('a'),
    ).toBe(true);
  });

  it('on a phone, ☰ opens the drawer and choosing closes it', () => {
    const home = new Home({ storage: null });
    home.phone = true;
    home.toggleNav();
    expect(home.drawer).toBe(true);
    expect(home.hidden).toBe(false);
    home.select('item-shop');
    expect(home.drawer).toBe(false);
  });
});
