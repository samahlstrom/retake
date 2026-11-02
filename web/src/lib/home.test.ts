import { describe, expect, it } from 'vitest';
import {
  cardStatus,
  crumb,
  fmtLength,
  needsComputer,
  homeContext,
  nextToReview,
  normalizeHome,
  projectUrl,
  STATUS_TEXT,
  streamText,
  toReview,
  unwatchUrl,
  videoName,
  viewerId,
  type Card,
} from './home';

const card = (name: string, status: Card['status']): Card => ({
  name,
  title: name,
  video: name,
  path: name,
  project: name,
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
  thumb: `p/${name}/api/poster`,
});

describe('home', () => {
  it('knows when the review page is a project of a home', () => {
    expect(homeContext('/p/alpha/')).toEqual({ home: '/', project: 'alpha' });
    expect(homeContext('/p/alpha/index.html')).toEqual({ home: '/', project: 'alpha' });
    expect(homeContext('/review/p/item-shop/')).toEqual({ home: '/review/', project: 'item-shop' });
    expect(homeContext('/p/caf%C3%A9/')).toEqual({ home: '/', project: 'café' });
    expect(homeContext('/p/%E0%A4%A/')).toEqual({ home: '/', project: '%E0%A4%A' });
    for (const p of ['/', '/index.html', '/p/', '/p/alpha', '/p/alpha/api/info', '/x/alpha/'])
      expect(homeContext(p)).toBeNull();
    expect(projectUrl('/', 'bravo cut')).toBe('/p/bravo%20cut/');
    expect(projectUrl('/review/', 'b')).toBe('/review/p/b/');
  });

  it('picks the next project to review in the home order, never the current one', () => {
    const cards = [card('a', 'to_review'), card('b', 'to_review'), card('c', 'reviewed'), card('d', 'being_edited')];
    expect(nextToReview(cards, 'a')?.name).toBe('b');
    expect(nextToReview(cards, 'b')?.name).toBe('a');
    expect(nextToReview(cards, 'c')?.name).toBe('a');
    expect(nextToReview(cards, null)?.name).toBe('a');
    expect(nextToReview([card('a', 'to_review'), card('c', 'reviewed')], 'a')).toBeNull();
    expect(toReview(cards)).toBe(2);
  });

  it('names the statuses and the streaming state', () => {
    expect(STATUS_TEXT).toEqual({
      to_review: 'To review',
      reviewed: 'Reviewed',
      being_edited: 'Being edited',
      no_cut: 'No cut yet',
    });
    expect(streamText(null)).toBeNull();
    expect(streamText({ state: 'pending', done: 0, total: 0 })).toBe('Not rendered yet');
    expect(streamText({ state: 'rendering', done: 3, total: 12 })).toBe('Rendering 3/12');
    expect(streamText({ state: 'rendering', done: 0, total: 0 })).toBe('Rendering');
    expect(streamText({ state: 'complete', done: 12, total: 12 })).toBeNull();
  });

  it('makes a /api/home body safe to render', () => {
    const h = normalizeHome({
      title: 'deadlock',
      role: 'owner',
      folder: '/videos',
      projects: [
        { name: 'a', status: 'being_edited', duration: 12.5, stream: { state: 'rendering', done: 2, total: 5 } },
        { name: 'b', status: 'weird', duration: 'long', stream: { done: 1 } },
        { title: 'no name' },
        null,
        'x',
      ],
    });
    expect(h.title).toBe('deadlock');
    expect(h.folder).toBe('/videos');
    expect(h.projects.map((c) => [c.name, c.status, c.duration, c.stream, c.url, c.title])).toEqual([
      ['a', 'being_edited', 12.5, { state: 'rendering', done: 2, total: 5 }, 'p/a/', 'a'],
      ['b', 'to_review', null, null, 'p/b/', 'b'],
    ]);
    expect(normalizeHome(null)).toEqual({
      title: '',
      folder: undefined,
      role: '',
      projects: [],
      tree: { title: '', path: '', project: false, videos: [], children: [] },
    });
    expect(normalizeHome({ projects: 'nope' }).projects).toEqual([]);
  });

  it('reads the tree: its videos are the cards, and an older server gets one at the top', () => {
    const h = normalizeHome({
      title: 'deadlock',
      projects: [
        { name: 'item-shop', video: 'itemshop', path: 'item-shop', project: 'item-shop', questions: 2 },
        { name: 'one', path: 'item-shop/shorts/one', project: 'item-shop' },
        { name: 'flat', path: 'flat' },
      ],
      tree: {
        title: 'deadlock',
        path: '',
        children: [
          {
            title: 'item-shop',
            path: 'item-shop',
            project: true,
            videos: [{ name: 'item-shop' }, { name: 'one' }, { name: 'not-a-card' }, 'x'],
            children: 'nope',
          },
          null,
        ],
      },
    });
    const [shop, one, flat] = h.projects;
    expect([shop.video, shop.questions, one.project, flat.project]).toEqual(['itemshop', 2, 'item-shop', 'flat']);
    expect(h.tree.children).toHaveLength(1);
    expect(h.tree.children[0].videos).toEqual([shop, one]);
    expect(h.tree.children[0].children).toEqual([]);
    expect(h.tree.videos).toEqual([]);
    const old = normalizeHome({ title: 'shows', projects: [{ name: 'a' }] });
    expect(old.tree).toEqual({ title: 'shows', path: '', project: false, videos: old.projects, children: [] });
  });

  it('names a video, and where it is', () => {
    // A video's folder goes by the folder's name; a project's own video by its family's.
    const shop = { video: 'itemshop', title: 'item-shop', name: 'item-shop' };
    expect(videoName({ ...shop, path: 'item-shop', project: '' })).toBe('item-shop');
    expect(videoName({ ...shop, path: 'deadlock/item-shop', project: 'deadlock' })).toBe('item-shop');
    expect(videoName({ ...shop, path: 'item-shop', project: 'item-shop' })).toBe('itemshop');
    expect(videoName({ video: '', title: 'Intro Cut', name: 'intro-cut', path: '', project: '' })).toBe('Intro Cut');
    expect(crumb('item-shop', 'itemshop')).toEqual(['item-shop']);
    expect(crumb('Item Shop', 'item_shop')).toEqual(['Item Shop']);
    expect(crumb('item-shop', 'honored-one')).toEqual(['item-shop', 'honored-one']);
    expect(crumb('', 'clip')).toEqual(['clip']);
    expect(crumb('jump-souls', '')).toEqual(['jump-souls']);
  });

  it('formats lengths, viewer ids and the unwatch call', () => {
    expect(fmtLength(null)).toBe('');
    expect(fmtLength(0)).toBe('');
    expect(fmtLength(42.4)).toBe('0:42');
    expect(fmtLength(412.5)).toBe('6:53');
    expect(fmtLength(3723)).toBe('1:02:03');
    const id = viewerId();
    expect(id).toMatch(/^[0-9a-z]{12}$/);
    expect(viewerId(() => 0)).toBe('000000000000');
    expect(viewerId(() => 0.9999)).toBe('zzzzzzzzzzzz');
    expect(unwatchUrl('v3', 'abc')).toBe('api/versions/v3/unwatch?viewer=abc');
    expect(unwatchUrl('a b', 'x&y')).toBe('api/versions/a%20b/unwatch?viewer=x%26y');
  });
});

describe("an extension's kind on the home", () => {
  it('keeps a no_cut card, its kind, its kinds and when it changed', () => {
    const h = normalizeHome({
      projects: [
        {
          name: 'idea',
          status: 'no_cut',
          kind: 'ignition.doc',
          version: 'v0',
          kinds: ['ignition.doc', 3],
          changed: 17,
        },
        { name: 'cut', status: 'to_review', kinds: ['ignition.doc'], changed: 'soon' },
        { name: 'old' },
      ],
    });
    expect(h.projects.map((c) => [c.status, c.kind, c.kinds, c.changed])).toEqual([
      ['no_cut', 'ignition.doc', ['ignition.doc'], 17],
      ['to_review', undefined, ['ignition.doc'], undefined],
      ['to_review', undefined, undefined, undefined],
    ]);
    expect(cardStatus(h.projects[0]!)).toEqual({ text: 'No cut yet', tone: 'no_cut' });
    expect(toReview(h.projects)).toBe(2); // an idea isn't to review
    expect(nextToReview(h.projects, null)?.name).toBe('cut');
  });
});

describe('one status per card', () => {
  it('rendering wins over the review status', () => {
    expect(cardStatus({ status: 'to_review', stream: null })).toEqual({ text: 'To review', tone: 'to_review' });
    expect(cardStatus({ status: 'reviewed', stream: { state: 'rendering', done: 3, total: 12 } })).toEqual({
      text: 'Rendering 3/12',
      tone: 'rendering',
    });
    expect(cardStatus({ status: 'being_edited', stream: { state: 'complete', done: 12, total: 12 } }).text).toBe(
      'Being edited',
    );
    // A question waiting on an answer says so (rendering still wins).
    expect(cardStatus({ status: 'being_edited', stream: null, questions: 1 })).toEqual({
      text: 'Has questions',
      tone: 'question',
    });
    expect(
      cardStatus({ status: 'being_edited', stream: { state: 'pending', done: 0, total: 0 }, questions: 1 }).tone,
    ).toBe('rendering');
  });
});

describe('machines (C5)', () => {
  it('reads them when the server sends them, and leaves them out otherwise', () => {
    expect(normalizeHome({ projects: [] }).machines).toBeUndefined();
    expect(normalizeHome({ projects: [], machines: [] }).machines).toEqual([]);
    expect(
      normalizeHome({
        projects: [],
        machines: [{ id: 'm1', name: 'sam-pc', online: true, agents: [{ id: 'claude', name: 'Claude Code' }] }, null],
      }).machines,
    ).toEqual([{ id: 'm1', name: 'sam-pc', online: true, agents: [{ id: 'claude', name: 'Claude Code' }] }]);
  });
});

describe('hosted: connect your computer', () => {
  const m = (online: boolean) => ({ id: 'm', name: 'sam-pc', online, agents: [] });
  it('an owner signed in to a hosted server with no computer online', () => {
    expect(normalizeHome({ projects: [], user: { email: 'a@example.com', name: 'Ana' } }).user).toEqual({
      email: 'a@example.com',
      name: 'Ana',
    });
    const user = { email: 'a@example.com', name: 'Ana' };
    expect(needsComputer({ user, role: 'owner', machines: [] })).toBe(true);
    expect(needsComputer({ user, role: 'owner', machines: [m(false)] })).toBe(true);
    expect(needsComputer({ user, role: 'owner', machines: [m(false), m(true)] })).toBe(false);
  });
  it('never in local mode, for other roles, or on an older server', () => {
    expect(needsComputer({ role: 'owner', machines: [] })).toBe(false);
    expect(needsComputer({ user: { email: '', name: '' }, role: 'reviewer', machines: [] })).toBe(false);
    expect(needsComputer({ user: { email: '', name: '' }, role: 'owner' })).toBe(false);
  });
});
