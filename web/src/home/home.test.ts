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
