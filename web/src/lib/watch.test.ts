import { describe, expect, it } from 'vitest';
import type { StreamState } from './types';
import { WATCH_EVERY_MS, Watcher } from './watch';

function harness(answer: unknown = null) {
  const posts: string[] = [];
  const beacons: string[] = [];
  const timers = new Map<number, () => void>();
  let id = 0;
  let visible = true;
  const streams: [string, StreamState][] = [];
  const w = new Watcher({
    viewer: 'p1',
    post: (url) => {
      posts.push(url);
      return Promise.resolve(answer);
    },
    beacon: (url) => {
      beacons.push(url);
      return true;
    },
    setInterval: ((fn: () => void, ms: number) => {
      expect(ms).toBe(WATCH_EVERY_MS);
      timers.set(++id, fn);
      return id;
    }) as never,
    clearInterval: ((t: number) => void timers.delete(t)) as never,
    visible: () => visible,
    onStream: (label, s) => streams.push([label, s]),
  });
  const tick = () => [...timers.values()].forEach((f) => f());
  return {
    w,
    posts,
    beacons,
    timers,
    streams,
    tick,
    hide: () => {
      visible = false;
      w.visibility(false);
    },
    show: () => {
      visible = true;
      w.visibility(true);
    },
  };
}

const watchUrl = (l: string) => `api/versions/${l}/watch?viewer=p1`;
const unwatchUrl = (l: string) => `api/versions/${l}/unwatch?viewer=p1`;

describe('watching a streamed version', () => {
  it('watches at once and every 10 s while it is shown', () => {
    const h = harness();
    h.w.watch('v7');
    expect(h.posts).toEqual([watchUrl('v7')]);
    h.tick();
    h.tick();
    expect(h.posts).toHaveLength(3);
    h.w.watch('v7'); // the same version again: nothing new
    expect(h.posts).toHaveLength(3);
    expect(h.timers.size).toBe(1);
    expect(h.beacons).toEqual([]);
  });

  it('stops with a beacon when the page shows another version or nothing to watch', () => {
    const h = harness();
    h.w.watch('v7');
    h.w.watch('v8');
    expect(h.beacons).toEqual([unwatchUrl('v7')]);
    expect(h.posts).toEqual([watchUrl('v7'), watchUrl('v8')]);
    h.w.watch(null); // a finished version (or the stream completed)
    expect(h.beacons).toEqual([unwatchUrl('v7'), unwatchUrl('v8')]);
    expect(h.timers.size).toBe(0);
    h.tick();
    expect(h.posts).toHaveLength(2);
    h.w.watch(null);
    expect(h.beacons).toHaveLength(2); // nothing was watched
  });

  it('stops while the page is hidden and watches again when it is visible', () => {
    const h = harness();
    h.w.watch('v7');
    h.hide();
    expect(h.beacons).toEqual([unwatchUrl('v7')]);
    expect(h.timers.size).toBe(0);
    h.w.watch('v7'); // still the version on screen, but hidden: no watch
    expect(h.posts).toHaveLength(1);
    h.show();
    expect(h.posts).toEqual([watchUrl('v7'), watchUrl('v7')]);
    expect(h.timers.size).toBe(1);
  });

  it('a page shown while hidden starts watching when it becomes visible', () => {
    const h = harness();
    h.hide();
    h.w.watch('v7');
    expect(h.posts).toEqual([]);
    expect(h.beacons).toEqual([]);
    h.show();
    expect(h.posts).toEqual([watchUrl('v7')]);
  });

  it('says it left when the page goes away', () => {
    const h = harness();
    h.w.watch('v7');
    h.w.leave();
    expect(h.beacons).toEqual([unwatchUrl('v7')]);
    expect(h.timers.size).toBe(0);
    h.w.leave();
    expect(h.beacons).toHaveLength(1);
  });

  it("hands each answer's stream state on, for the version still watched", async () => {
    const s: StreamState = { state: 'rendering', done: 2, total: 6, seconds: 3.9 };
    const h = harness({ label: 'v7', stream: s });
    h.w.watch('v7');
    await Promise.resolve();
    await Promise.resolve();
    expect(h.streams).toEqual([['v7', s]]);
    h.w.watch('v8');
    h.w.watch(null); // the answer for v8 comes after the page moved on: dropped
    await Promise.resolve();
    await Promise.resolve();
    expect(h.streams).toHaveLength(1);
  });

  it('escapes the label and has a random viewer id by default', () => {
    const a = new Watcher({ post: () => Promise.resolve(null), visible: () => false });
    const b = new Watcher({ post: () => Promise.resolve(null), visible: () => false });
    expect(a.viewer).not.toBe(b.viewer);
    expect(a.viewer.length).toBeGreaterThan(8);
    const posts: string[] = [];
    const w = new Watcher({
      viewer: 'x y',
      post: (u) => (posts.push(u), Promise.resolve(null)),
      visible: () => true,
      setInterval: (() => 1) as never,
    });
    w.watch('v 9');
    expect(posts).toEqual(['api/versions/v%209/watch?viewer=x%20y']);
  });
});
