// The time map between two versions (spec/bridge.md, "The time map"): C, [ ], a question's chips and "see it in vN"
// show the other version at the matching moment instead of the same clock time. The server builds the map from the
// versions' render manifests; without them its segments are empty and the time stays as it is.

/** One linear piece: A's seconds a[0]..a[1] show at B's b[0]..b[1]. */
export interface Segment {
  a: [number, number];
  b: [number, number];
  by?: string;
}

export interface TimeMapResponse {
  from: string;
  to: string;
  segments: Segment[] | null;
}

const round6 = (x: number): number => Math.round(x * 1e6) / 1e6;

/** Maps time `t` of version A through the segments; no segments: `t` itself. Before or after them it clamps. */
export function mapTime(segs: readonly Segment[] | null | undefined, t: number): number {
  if (!segs || !segs.length) return t;
  if (t <= segs[0].a[0]) return segs[0].b[0];
  for (let i = 0; i < segs.length; i++) {
    const s = segs[i];
    if (t < s.a[1] || i === segs.length - 1) {
      if (s.a[1] <= s.a[0]) return s.b[0];
      const f = Math.min((t - s.a[0]) / (s.a[1] - s.a[0]), 1);
      return round6(s.b[0] + f * (s.b[1] - s.b[0]));
    }
  }
  return t;
}

// Relative, like every page URL: the page also runs under /p/<project>/ on `retake serve`.
export const timemapUrl = (from: string, to: string): string =>
  `api/timemap?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`;

type Fetch = (url: string, init?: RequestInit) => Promise<Response>;

/**
 * The time maps the page has asked for, one request per pair of versions. A map that can't be had (an older server
 * without the route, a failed request, no manifests) is null: the time stays.
 */
export class TimeMaps {
  readonly #fetch: Fetch;
  readonly #timeoutMs: number;
  #pending = new Map<string, Promise<Segment[] | null>>();
  #ready = new Map<string, Segment[] | null>();

  constructor(opts: { fetch?: Fetch; timeoutMs?: number } = {}) {
    this.#fetch = opts.fetch ?? ((u, i) => fetch(u, i));
    this.#timeoutMs = opts.timeoutMs ?? 1500;
  }

  /** The segments from `from` to `to` (null: no map). Same version: null. */
  get(from: string | null, to: string | null): Promise<Segment[] | null> {
    if (!from || !to || from === to) return Promise.resolve(null);
    const key = `${from}\n${to}`;
    let p = this.#pending.get(key);
    if (!p) {
      p = this.#load(from, to).then((segs) => {
        if (this.#pending.get(key) === p) this.#ready.set(key, segs);
        return segs;
      });
      this.#pending.set(key, p);
    }
    return p;
  }

  /** What `get` has already: the segments, null for no map, undefined while not loaded (it starts loading). */
  peek(from: string | null, to: string | null): Segment[] | null | undefined {
    if (!from || !to || from === to) return null;
    const key = `${from}\n${to}`;
    if (this.#ready.has(key)) return this.#ready.get(key);
    void this.get(from, to);
    return undefined;
  }

  /** Time `t` of `from`, mapped onto `to`. */
  async map(from: string | null, to: string | null, t: number): Promise<number> {
    return mapTime(await this.get(from, to), t);
  }

  /** Forget every map (the family changed: a new version may have brought its manifest). */
  clear(): void {
    this.#pending.clear();
    this.#ready.clear();
  }

  async #load(from: string, to: string): Promise<Segment[] | null> {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), this.#timeoutMs);
    try {
      const r = await this.#fetch(timemapUrl(from, to), { signal: ctl.signal });
      if (!r.ok) {
        await r.body?.cancel().catch(() => {});
        return null;
      }
      const d = (await r.json()) as TimeMapResponse;
      const segs = Array.isArray(d?.segments) ? d.segments.filter(validSegment) : [];
      return segs.length ? segs : null;
    } catch {
      return null; // no route (an older or the legacy server), the server gone, or too slow: keep the time
    } finally {
      clearTimeout(timer);
    }
  }
}

const pair = (v: unknown): v is [number, number] =>
  Array.isArray(v) && v.length === 2 && v.every((x) => typeof x === 'number' && Number.isFinite(x));

function validSegment(s: unknown): s is Segment {
  const x = s as Segment;
  return !!x && pair(x.a) && pair(x.b);
}
