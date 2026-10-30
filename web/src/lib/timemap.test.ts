import { describe, expect, it, vi } from 'vitest';
import { mapTime, TimeMaps, timemapUrl, type Segment } from './timemap';

// v1 -> v2 of the demo: 0.3 s of VO air at 3.6-3.9 trimmed, so everything after it plays 0.3 s earlier.
const SEGS: Segment[] = [
  { a: [0, 2], b: [0, 2], by: 'video' },
  { a: [2, 3.6], b: [2, 3.6], by: 'spine' },
  { a: [3.6, 3.9], b: [3.599, 3.599], by: 'shot' },
  { a: [3.9, 16.1], b: [3.6, 15.8], by: 'spine' },
];

describe('mapTime', () => {
  it('maps through the segment holding the time', () => {
    expect(mapTime(SEGS, 1)).toBe(1);
    expect(mapTime(SEGS, 9)).toBe(8.7);
    expect(mapTime(SEGS, 3.75)).toBe(3.599); // trimmed: the end of the shortened shot
    expect(mapTime(SEGS, 16.1)).toBe(15.8);
  });

  it('clamps outside the segments and keeps the time without any', () => {
    expect(mapTime(SEGS, -1)).toBe(0);
    expect(mapTime(SEGS, 99)).toBe(15.8);
    expect(mapTime([], 5)).toBe(5);
    expect(mapTime(null, 5)).toBe(5);
    expect(mapTime(undefined, 5)).toBe(5);
  });

  it('handles a stretch that maps to one moment, and a stretch that grows', () => {
    expect(mapTime([{ a: [0, 2], b: [10, 10] }], 1)).toBe(10);
    expect(mapTime([{ a: [0, 2], b: [0, 4] }], 1)).toBe(2);
    expect(mapTime([{ a: [1, 1], b: [7, 7] }], 1)).toBe(7);
  });
});

const ok = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

describe('TimeMaps', () => {
  it('asks once per pair, and maps through what it got', async () => {
    const fetch = vi.fn(async () => ok({ from: 'v1', to: 'v2', segments: SEGS }));
    const maps = new TimeMaps({ fetch });
    expect(await maps.map('v1', 'v2', 9)).toBe(8.7);
    expect(await maps.map('v1', 'v2', 13)).toBe(12.7);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0]).toEqual([timemapUrl('v1', 'v2'), expect.anything()]);
    expect(timemapUrl('v1', 'v 2')).toBe('/api/timemap?from=v1&to=v%202');
    // Each direction is its own map.
    await maps.get('v2', 'v1');
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('peeks: undefined while loading (it starts the request), then the segments', async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const fetch = vi.fn(async () => {
      await gate;
      return ok({ segments: SEGS });
    });
    const maps = new TimeMaps({ fetch });
    expect(maps.peek('v1', 'v2')).toBeUndefined();
    expect(maps.peek('v1', 'v2')).toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(1);
    release();
    await maps.get('v1', 'v2');
    expect(maps.peek('v1', 'v2')).toEqual(SEGS);
    expect(maps.peek('v1', 'v1')).toBeNull();
    expect(maps.peek(null, 'v1')).toBeNull();
  });

  it('keeps the time when there is no map: same version, no route, a failure, no manifests, too slow', async () => {
    const none = new TimeMaps({ fetch: vi.fn() });
    expect(await none.get('v1', 'v1')).toBeNull();
    expect(await none.get(null, 'v1')).toBeNull();
    for (const fetch of [
      async () => new Response('{"error": "not found"}', { status: 404 }),
      async () => {
        throw new TypeError('network');
      },
      async () => ok({ from: 'v1', to: 'v2', segments: [] }),
      async () => ok({ segments: [{ a: [0, 'x'], b: [0, 1] }] }),
      async () => new Response('not json', { status: 200 }),
    ]) {
      expect(await new TimeMaps({ fetch }).map('v1', 'v2', 9)).toBe(9);
    }
    const slow = new TimeMaps({
      timeoutMs: 20,
      fetch: (_u, init) =>
        new Promise((_res, rej) =>
          init?.signal?.addEventListener('abort', () => rej(new DOMException('', 'AbortError'))),
        ),
    });
    expect(await slow.map('v1', 'v2', 9)).toBe(9);
  });

  it('forgets everything when the family changes', async () => {
    let segs: Segment[] = [];
    const fetch = vi.fn(async () => ok({ segments: segs }));
    const maps = new TimeMaps({ fetch });
    expect(await maps.map('v1', 'v2', 9)).toBe(9); // v2 had no manifest yet
    segs = SEGS;
    expect(await maps.map('v1', 'v2', 9)).toBe(9); // still the cached answer
    maps.clear();
    expect(await maps.map('v1', 'v2', 9)).toBe(8.7);
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
