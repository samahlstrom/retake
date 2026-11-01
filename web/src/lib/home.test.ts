import { describe, expect, it } from 'vitest';
import {
  cardStatus,
  fmtLength,
  moveTargets,
  needsComputer,
  homeContext,
  nextToReview,
  normalizeHome,
  projectUrl,
  STATUS_TEXT,
  streamText,
  toReview,
  unwatchUrl,
  viewerId,
  type Card,
} from './home';

const card = (name: string, status: Card['status']): Card => ({
  name,
  title: name,
  url: `p/${name}/`,
  version: 'v1',
  versions: 1,
  duration: 10,
  pushed: '',
  status,
  takes: 0,
  open: 0,
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
    expect(STATUS_TEXT).toEqual({ to_review: 'To review', reviewed: 'Reviewed', being_edited: 'Being edited' });
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
    expect(normalizeHome(null)).toEqual({ title: '', folder: undefined, role: '', projects: [] });
    expect(normalizeHome({ projects: 'nope' }).projects).toEqual([]);
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

describe('one status per card, and where a video can move', () => {
  it('rendering wins over the review status', () => {
    expect(cardStatus({ status: 'to_review', stream: null })).toEqual({ text: 'To review', tone: 'to_review' });
    expect(cardStatus({ status: 'reviewed', stream: { state: 'rendering', done: 3, total: 12 } })).toEqual({
      text: 'Rendering 3/12',
      tone: 'rendering',
    });
    expect(cardStatus({ status: 'being_edited', stream: { state: 'complete', done: 12, total: 12 } }).text).toBe(
      'Being edited',
    );
  });
  it('one project without groups: nowhere to move, so no menu', () => {
    const w = { revision: 1, projects: [{ id: 'default', title: 'Shows', groups: [] }], placements: [] };
    expect(moveTargets(w, 'alpha')).toEqual([]);
  });
  it('one project with groups: the groups and Ungrouped, minus where it is', () => {
    const w = {
      revision: 1,
      projects: [{ id: 'default', title: 'Shows', groups: [{ id: 'g', title: 'Shorts' }] }],
      placements: [{ video: 'alpha', project: 'default', group: 'g' }],
    };
    expect(moveTargets(w, 'alpha')).toEqual([{ value: 'default/', label: 'Ungrouped' }]);
    expect(moveTargets(w, 'bravo')).toEqual([{ value: 'default/g', label: 'Shorts' }]);
  });
  it('several projects: named with their project', () => {
    const w = {
      revision: 1,
      projects: [
        { id: 'default', title: 'Shows', groups: [] },
        { id: 'l', title: 'Launch', groups: [{ id: 's', title: 'Shorts' }] },
      ],
      placements: [],
    };
    expect(moveTargets(w, 'alpha')).toEqual([
      { value: 'l/', label: 'Launch · Ungrouped' },
      { value: 'l/s', label: 'Launch · Shorts' },
    ]);
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
