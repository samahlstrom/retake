// The smallest edit between two texts, for showing an outside edit (the agent's, an undo, another editor) in the open
// editor without losing the caret: the lines both texts share stay where they are, and each changed run of lines
// becomes one change trimmed to the characters that differ. The editor maps the caret and its own undo history through
// these changes, so the caret stays on the word it was on even when the agent edited above and below it.
import { diff } from './doc';

/** One change, in the old text's positions: [from, to) replaced by `insert`. */
export interface Change {
  from: number;
  to: number;
  insert: string;
}

/** A text cut after each line break, so the pieces join back to it exactly. */
function pieces(s: string): string[] {
  const out: string[] = [];
  let at = 0;
  for (let i = s.indexOf('\n'); i !== -1; i = s.indexOf('\n', at)) {
    out.push(s.slice(at, i + 1));
    at = i + 1;
  }
  if (at < s.length) out.push(s.slice(at));
  return out;
}

/**
 * The lines of `a` and `b` that match, in order (Myers' diff), as pairs of indexes; null when more than `maxD` lines
 * differ (then one change is as good).
 */
function common(a: number[], b: number[], maxD: number): [number, number][] | null {
  const n = a.length,
    m = b.length;
  const max = n + m;
  const off = max + 1;
  const v = new Int32Array(2 * max + 3);
  /** v before each step d, its entries k in [-d-1, d+1]. */
  const trace: Int32Array[] = [];
  for (let d = 0; d <= Math.min(max, maxD); d++) {
    trace.push(v.slice(off - d - 1, off + d + 2));
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && v[off + k - 1]! < v[off + k + 1]!) ? v[off + k + 1]! : v[off + k - 1]! + 1;
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v[off + k] = x;
      if (x >= n && y >= m) return back(trace, n, m, d);
    }
  }
  return null;
}

function back(trace: Int32Array[], n: number, m: number, end: number): [number, number][] {
  const out: [number, number][] = [];
  let x = n,
    y = m;
  for (let d = end; d > 0; d--) {
    const prev = trace[d]!;
    const at = (k: number): number => prev[k + d + 1]!;
    const k = x - y;
    const pk = k === -d || (k !== d && at(k - 1) < at(k + 1)) ? k + 1 : k - 1;
    const px = at(pk),
      py = px - pk;
    while (x > px && y > py) out.push([--x, --y]);
    x = px;
    y = py;
  }
  while (x > 0 && y > 0) out.push([--x, --y]);
  return out.reverse();
}

/** The changes that turn `a` into `b`, sorted and apart, in `a`'s positions. */
export function changes(a: string, b: string, maxD = 500): Change[] {
  if (a === b) return [];
  const pa = pieces(a),
    pb = pieces(b);
  const ids = new Map<string, number>();
  const id = (s: string): number => {
    let n = ids.get(s);
    if (n === undefined) ids.set(s, (n = ids.size));
    return n;
  };
  const ia = pa.map(id),
    ib = pb.map(id);
  const pairs = common(ia, ib, maxD);
  if (!pairs) {
    const s = diff(a, b);
    return [{ from: s.at, to: s.at + s.del, insert: s.ins }];
  }
  const out: Change[] = [];
  const startA: number[] = [0];
  for (const p of pa) startA.push(startA[startA.length - 1]! + p.length);
  let ai = 0,
    bi = 0;
  const hunk = (a1: number, b1: number): void => {
    if (a1 === ai && b1 === bi) return;
    const from = startA[ai]!;
    const was = pa.slice(ai, a1).join(''),
      now = pb.slice(bi, b1).join('');
    const s = diff(was, now);
    if (s.del || s.ins) out.push({ from: from + s.at, to: from + s.at + s.del, insert: s.ins });
  };
  for (const [x, y] of pairs) {
    hunk(x, y);
    ai = x + 1;
    bi = y + 1;
  }
  hunk(pa.length, pb.length);
  return out;
}

/** `changes` applied to `a` (for tests and checks). */
export function apply(a: string, cs: Change[]): string {
  let out = '',
    at = 0;
  for (const c of cs) {
    out += a.slice(at, c.from) + c.insert;
    at = c.to;
  }
  return out + a.slice(at);
}
