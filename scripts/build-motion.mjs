#!/usr/bin/env node
// Writes the README's motion graphics, docs/media/<name>.svg, from docs/media/src (no browser needed).
//
//   node scripts/build-motion.mjs [name...]            write the SVGs
//   node scripts/build-motion.mjs --check              fail if an SVG is stale or breaks a rule below
//   node scripts/build-motion.mjs --preview DIR [--at 0,2.5,9] [name...]
//                                                      frozen frames as SVG, and PNG when rsvg-convert is installed
//
// Rules each SVG keeps: under 300 KB; nothing outside the file (no http(s) links, no scripts); a still for
// prefers-reduced-motion; every animation repeats on the graphic's loop (so the loop is seamless).
// The MP4s, GIFs and PNG stills come from scripts/render-motion.mjs (headless Chromium).
import { mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { svgDoc } from '../docs/media/src/kit.mjs';
import graphics from '../docs/media/src/index.mjs';

const root = fileURLToPath(new URL('..', import.meta.url));
const media = join(root, 'docs/media');
const MAX_BYTES = 300 * 1024;

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  if (i < 0) return undefined;
  const v = args[i + 1];
  args.splice(i, 2);
  return v;
};
const check = args.includes('--check');
if (check) args.splice(args.indexOf('--check'), 1);
const previewDir = flag('--preview');
const at = flag('--at');
const names = args;

export function validate(name, svg, motion) {
  const errs = [];
  const bytes = Buffer.byteLength(svg);
  if (bytes > MAX_BYTES) errs.push(`${bytes} bytes, over ${MAX_BYTES}`);
  if (/<script|<foreignObject|\son[a-z]+=/i.test(svg)) errs.push('has a script or an event handler');
  const refs = [...svg.matchAll(/(?:href|src)="([^"]*)"|url\(([^)]*)\)/g)].map((m) => m[1] ?? m[2]);
  for (const r of refs) if (!/^#|^data:/.test(r)) errs.push(`refers outside the file: ${r.slice(0, 60)}`);
  const http = [...svg.matchAll(/https?:\/\/[^\s"')]+/g)].map((m) => m[0]).filter((u) => u !== 'http://www.w3.org/2000/svg');
  if (http.length) errs.push(`has links: ${http.join(', ')}`);
  if (!svg.includes('prefers-reduced-motion:reduce')) errs.push('no reduced-motion still');
  if (!/<title[^>]*>[^<]+<\/title>/.test(svg)) errs.push('no title');
  for (const an of motion.list) {
    const reps = motion.duration / an.d;
    if (Math.abs(reps - Math.round(reps)) > 1e-9) errs.push(`${an.id} lasts ${an.d}s, which doesn't divide the ${motion.duration}s loop`);
  }
  if (motion.duration < 6 || motion.duration > 10) errs.push(`loop is ${motion.duration}s, not 6–10 s`);
  return errs.map((e) => `${name}: ${e}`);
}

function build(G) {
  const g = G();
  return { ...g, svg: svgDoc(g) };
}

const picked = names.length ? graphics.filter((G) => names.includes(G().name)) : graphics;
if (names.length && picked.length !== names.length) {
  console.error(`unknown graphic; have: ${graphics.map((G) => G().name).join(', ')}`);
  process.exit(2);
}

let failed = false;
for (const G of picked) {
  const g = build(G);
  const file = join(media, `${g.name}.svg`);
  const errs = validate(g.name, g.svg, g.motion);
  if (errs.length) {
    failed = true;
    for (const e of errs) console.error(e);
  }
  if (previewDir) {
    mkdirSync(previewDir, { recursive: true });
    const times = at ? at.split(',').map(Number) : [g.motion.stillAt];
    for (const t of times) {
      const frozen = svgDoc({ ...g, frozen: t });
      const base = join(previewDir, `${g.name}-${String(t.toFixed(2)).padStart(5, '0')}`);
      writeFileSync(`${base}.svg`, frozen);
      try {
        execFileSync('rsvg-convert', ['-w', '1200', '-o', `${base}.png`, `${base}.svg`], { stdio: 'inherit' });
      } catch (e) {
        if (e.code !== 'ENOENT') throw e;
      }
    }
    console.log(`${g.name}: ${times.length} frame(s) in ${previewDir}`);
    continue;
  }
  if (check) {
    const now = existsSync(file) ? readFileSync(file, 'utf8') : '';
    if (now !== g.svg) {
      failed = true;
      console.error(`${g.name}: docs/media/${g.name}.svg is stale; run node scripts/build-motion.mjs`);
    }
    continue;
  }
  writeFileSync(file, g.svg);
  console.log(`docs/media/${g.name}.svg  ${(Buffer.byteLength(g.svg) / 1024).toFixed(1)} KB, ${g.motion.duration}s loop, ${g.motion.list.length} animations`);
}
process.exit(failed ? 1 : 0);
