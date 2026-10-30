// Structural diff of what retake and the legacy Python server produced for the same e2e run.
//   takes <retake take dir> <legacy take dir>: same files; each JSON file has the same paths with the same value
//       types (array elements merged); values that depend on wall-clock timing are not compared.
//   info <retake info.json> <legacy info.json> <retake root> <legacy root>: /api/info at startup, deep-equal
//       (key order included) once each fixture root is replaced by <root>.
// The only difference allowed is retake's new session.json "author" (spec/api.md, new in v1).
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const [, , mode, a, b, rootA, rootB] = process.argv;
const json = p => JSON.parse(readFileSync(p, 'utf8'));
const allowedOnlyInRetake = new Set(['session.json:.author', 'session.json:.author.name']);
let problems = 0;
const fail = msg => { problems++; console.log(`  DIFF  ${msg}`); };

function shapes(v, path = '', out = new Map()) {
  const type = v === null ? 'null' : Array.isArray(v) ? 'array' : typeof v;
  if (!out.has(path)) out.set(path, new Set());
  out.get(path).add(type);
  if (type === 'array') for (const x of v) shapes(x, `${path}[]`, out);
  if (type === 'object') for (const [k, x] of Object.entries(v)) shapes(x, `${path}.${k}`, out);
  return out;
}

function diffShapes(file, va, vb) {
  const sa = shapes(va), sb = shapes(vb);
  let n = 0;
  for (const [p, ta] of sa) {
    const key = `${file}:${p}`;
    if (!sb.has(p)) {
      if (!allowedOnlyInRetake.has(key)) fail(`${key} only in retake (${[...ta]})`);
      continue;
    }
    const tb = sb.get(p);
    if ([...ta].sort().join('|') !== [...tb].sort().join('|')) fail(`${key} is ${[...ta]} in retake, ${[...tb]} in legacy`);
    n++;
  }
  for (const p of sb.keys()) if (!sa.has(p)) fail(`${file}:${p} only in legacy`);
  return n;
}

if (mode === 'takes') {
  const fa = readdirSync(a).sort(), fb = readdirSync(b).sort();
  if (fa.join() !== fb.join()) fail(`take files: retake [${fa}] vs legacy [${fb}]`);
  else console.log(`  same  take files: ${fa.join(' ')}`);
  for (const f of fa.filter(n => n.endsWith('.json') && fb.includes(n))) {
    const va = json(join(a, f)), vb = json(join(b, f));
    const before = problems;
    const n = diffShapes(f, va, vb);
    // Same top-level key order too (Python dicts and ours keep insertion order).
    const order = v => (v && typeof v === 'object' && !Array.isArray(v) ? Object.keys(v).filter(k => k !== 'author') : []);
    if (order(va).join() !== order(vb).join()) fail(`${f}: key order ${order(va)} vs ${order(vb)}`);
    const items = Array.isArray(va) ? ` (${va.length} vs ${Array.isArray(vb) ? vb.length : '?'} items)` : '';
    if (problems === before) console.log(`  same  ${f}: ${n} paths, same types${items}`);
  }
} else if (mode === 'info') {
  const norm = (v, root) => JSON.parse(JSON.stringify(v).split(JSON.stringify(root).slice(1, -1)).join('<root>'));
  const ia = norm(json(a), rootA), ib = norm(json(b), rootB);
  const n = diffShapes('info', ia, ib);
  if (JSON.stringify(ia) !== JSON.stringify(ib)) {
    fail(`/api/info values differ:\n    retake ${JSON.stringify(ia)}\n    legacy ${JSON.stringify(ib)}`);
  } else console.log(`  same  /api/info: identical after replacing the fixture roots (${n} paths, key order included)`);
} else {
  console.error('usage: legacy-parity-diff.mjs takes <retake take> <legacy take> | info <a.json> <b.json> <rootA> <rootB>');
  process.exit(2);
}
console.log(problems ? `${problems} differences` : 'no differences');
process.exit(problems ? 1 : 0);
