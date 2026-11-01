import { describe, expect, it } from 'vitest';
import { Home } from './home.svelte';

const answer = (status: number, body: unknown): Response =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

describe('the home state', () => {
  it('reads api/home, and keeps the last cards when a read fails', async () => {
    const urls: string[] = [];
    let next: () => Promise<Response> = async () =>
      answer(200, { title: 'shows', role: 'owner', projects: [{ name: 'a', status: 'to_review' }] });
    const home = new Home({
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
    const home = new Home({ fetch: (async () => answer(200, {})) as typeof fetch });
    home.thumbFailed('a');
    home.thumbFailed('a');
    expect([...home.broken]).toEqual(['a']);
  });
});

it('keeps project organization on save and refreshes conflicts without overwriting', async () => {
  let conflict = false;
  const layout = { revision: 3, projects: [{ id: 'default', title: 'Shows', groups: [] }], placements: [] };
  const home = new Home({
    fetch: (async (_url: string, init?: RequestInit) => {
      if (init?.method === 'POST')
        return conflict
          ? answer(409, { error: 'Changed in another window' })
          : answer(200, { ...JSON.parse(String(init.body)), revision: 4 });
      return answer(200, { title: 'Shows', role: 'owner', projects: [{ name: 'alpha' }], workspace: layout });
    }) as typeof fetch,
  });
  await home.refresh();
  expect(home.videos('default')).toHaveLength(1);
  expect(await home.save((w) => w.projects.push({ id: 'launch', title: 'Launch', groups: [] }))).toBe(true);
  expect(home.workspace.projects).toHaveLength(2);
  expect(home.workspace.revision).toBe(4);
  conflict = true;
  expect(
    await home.save((w) => {
      w.projects[0].title = 'My title';
    }),
  ).toBe(false);
  expect(home.saveError).toBe('Changed in another window');
  expect(home.workspace.projects[0].title).toBe('Shows');
});

it('a home with one project opens straight to it; with more, the Projects page', async () => {
  let body: unknown = {
    role: 'owner',
    projects: [{ name: 'a', status: 'to_review' }],
    workspace: { revision: 1, projects: [{ id: 'default', title: 'Shows', groups: [] }], placements: [] },
  };
  const home = new Home({ fetch: (async () => answer(200, body)) as unknown as typeof fetch });
  await home.refresh();
  expect(home.single).toBe(true);
  expect(home.project?.id).toBe('default');
  body = {
    role: 'owner',
    projects: [{ name: 'a', status: 'to_review' }],
    workspace: {
      revision: 2,
      projects: [
        { id: 'default', title: 'Shows', groups: [] },
        { id: 'l', title: 'Launch', groups: [] },
      ],
      placements: [],
    },
  };
  await home.refresh();
  expect(home.single).toBe(false);
  expect(home.project).toBeUndefined();
  home.selected = 'l';
  expect(home.project?.title).toBe('Launch');
  home.selected = 'gone';
  expect(home.project).toBeUndefined();
});
