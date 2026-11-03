#!/usr/bin/env node
// Writes THIRD_PARTY_NOTICES.md: every third-party component inside the shipped binaries, with its license, and the
// license texts and copyright notices once each. It reads what is actually built, not what is declared:
//   - retake (Go): the modules `go list -deps` compiles for each release target (CGO off, -tags webdist), plus the Go
//     standard library;
//   - the review page (web/, embedded in retake): the npm packages in a sourcemap build of the page;
//   - retake-edit (Rust): the crates on normal dependency edges from retake-edit in `cargo metadata`, for each release
//     target;
//   - native code, fonts and separate programs that no metadata sees: scripts/notices-extra.json (hand-maintained).
// It also lists, without texts, the npm packages used only to develop and test (web/ and e2e/ lock files).
//
// usage: scripts/notices.mjs [--check]
//   --check  regenerate in memory and exit 1 if THIRD_PARTY_NOTICES.md is out of date (CI runs this)
// needs:   go, node and cargo on PATH (`mise exec -- node scripts/notices.mjs`), web/node_modules (`npm ci` in web/),
//          the Go module cache (`go mod download`), and the crate sources (`cargo fetch --locked` in editor/).
// Writes only THIRD_PARTY_NOTICES.md, and scratch in .retake-work/notices/ (deleted afterwards).
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

// The product's name: the binaries are NAME and NAME-edit. Renaming the product is this one edit.
const NAME = 'retake';
const EDIT = `${NAME}-edit`;

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const OUT = join(ROOT, 'THIRD_PARTY_NOTICES.md');
const EXTRA = join(ROOT, 'scripts', 'notices-extra.json');
const SCRATCH = join(ROOT, '.retake-work', 'notices');

// The release targets (keep in step with .goreleaser.yaml and the editor's release matrix).
const GO_TARGETS = ['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64', 'windows/arm64'];
const RUST_TARGETS = [
  'x86_64-unknown-linux-gnu',
  'aarch64-unknown-linux-gnu',
  'x86_64-apple-darwin',
  'aarch64-apple-darwin',
  'x86_64-pc-windows-msvc',
];

const check = process.argv.includes('--check');
for (const a of process.argv.slice(2)) {
  if (a !== '--check') fail(`unknown argument ${a} (usage: scripts/notices.mjs [--check])`);
}

function fail(msg) {
  console.error(`notices: ${msg}`);
  process.exit(2);
}

function run(cmd, args, opts = {}) {
  try {
    return execFileSync(cmd, args, { encoding: 'utf8', maxBuffer: 512 << 20, stdio: ['ignore', 'pipe', 'pipe'], ...opts });
  } catch (e) {
    const out = `${e.stderr ?? ''}`.trim().split('\n').slice(-8).join('\n');
    fail(`${cmd} ${args.join(' ')} failed${e.code === 'ENOENT' ? ` (${cmd} not on PATH; run through mise)` : ''}\n${out}`);
  }
}

const read = (p) => readFileSync(p, 'utf8').replace(/^﻿/, '').replace(/\r\n?/g, '\n');
const isFile = (p) => {
  try {
    return statSync(p).isFile();
  } catch {
    return false;
  }
};
const byKey = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

// ---------- license files and identification

const LICENSE_FILE = /^(licen[cs]e|copying|notice|unlicense|copyright)([-._].*)?$/i;

/** The license, notice and copying files at the top of a package folder, sorted. */
function licenseFiles(dir) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    // A license for a logo or other picture that isn't compiled in (modernc.org/memory's LICENSE-LOGO) is skipped.
    .filter((n) => LICENSE_FILE.test(n) && !/logo/i.test(n) && !/\.(rs|go|js|ts|json|html|png|svg)$/i.test(n)
      && isFile(join(dir, n)))
    .sort(byKey)
    .map((n) => join(dir, n));
}

const has = (t, ...needles) => needles.every((n) => t.includes(n));

/** The SPDX id of a license text, by fingerprint, or null. */
function identify(text) {
  const t = text.toLowerCase().replace(/\s+/g, ' ');
  if (has(t, 'apache license', 'version 2.0')) {
    // Some files carry both (go-sdk during its relicensing, hls.js for code it took from MIT projects).
    return t.includes('permission is hereby granted, free of charge') ? 'Apache-2.0 AND MIT' : 'Apache-2.0';
  }
  if (has(t, 'unicode license') || has(t, 'unicode, inc.', 'data files')) return 'Unicode-3.0';
  if (has(t, 'mozilla public license', '2.0')) return 'MPL-2.0';
  if (has(t, 'gnu lesser general public license')) return 'LGPL';
  if (has(t, 'gnu general public license')) return 'GPL';
  if (has(t, 'sil open font license')) return 'OFL-1.1';
  if (has(t, 'this is free and unencumbered software released into the public domain')) return 'Unlicense';
  if (has(t, 'boost software license')) return 'BSL-1.0';
  if (has(t, 'sqlite is public domain')) return 'public domain (SQLite)';
  if (has(t, 'creative commons', 'cc0')) return 'CC0-1.0';
  if (has(t, 'permission is hereby granted, free of charge')) {
    return t.includes('the above copyright notice and this permission notice shall be included') ? 'MIT' : 'MIT-0';
  }
  if (has(t, 'redistribution and use in source and binary forms')) {
    return t.includes('neither the name') || t.includes('names of its contributors') || t.includes('may not be used to endorse')
      ? 'BSD-3-Clause'
      : 'BSD-2-Clause';
  }
  if (t.includes('permission to use, copy, modify, and/or distribute this software for any purpose')
    || t.includes('permission to use, copy, modify, and distribute this software for any purpose')) {
    return t.includes('above copyright notice and this permission notice appear in all copies') ? 'ISC' : '0BSD';
  }
  if (has(t, "provided 'as-is', without any express or implied warranty", 'altered source versions must be plainly marked')) {
    return 'Zlib';
  }
  return null;
}

const PLACEHOLDER = /\[yyyy\]|\{yyyy\}|\[name of copyright owner\]|\{name of copyright owner\}|<year>|<copyright holders>/i;
// A title before the license text ("MIT License", "(The MIT License)", "BSD 3-Clause License"): not part of the terms.
const TITLE = /^\(?(the )?(mit|isc|bsd|zlib)( [0-9]-clause)?( license)?( \((mit|expat)\))?\)?:? /;

/**
 * A copyright notice line: "Copyright (c) 2015 X", "Copyright 2009 The Go Authors", "Copyright © X", "(C) 1995 X",
 * "© 2020 X". Not license prose that starts with the word ("copyright notice that is included...", "COPYRIGHT AND
 * PERMISSION NOTICE"), and not Apache's "(c) You must retain...".
 */
function isCopyrightLine(s) {
  if (PLACEHOLDER.test(s)) return false;
  if (/^(\(c\)\s*\d{4}|©\s*\S)/i.test(s)) return true;
  const m = s.match(/^copyright\b:?\s*(.*)$/i);
  if (!m) return false;
  const rest = m[1];
  if (/^(\(c\)|©|\d{4})/i.test(rest)) return true;
  if (/^(AND|NOTICE|NOTICES|HOLDERS?|OWNERS?|STATEMENT|LICENSE)\b/.test(rest)) return false;
  return /^[A-Z]/.test(rest); // a name: "Copyright The Rust Project Developers", "Copyright IBM Corp."
}

/** Splits a license file into its copyright lines and the remaining body. */
function splitCopyright(text) {
  const copyright = [];
  const body = [];
  let after = false; // the previous line was a copyright line
  for (const raw of text.split('\n')) {
    const line = raw.replace(/\s+$/, '');
    const trimmed = line.trim();
    if (isCopyrightLine(trimmed)) {
      copyright.push(trimmed.replace(/\s+/g, ' '));
      after = true;
    } else if (after && /^all rights reserved\.?$/i.test(trimmed)) {
      copyright[copyright.length - 1] += ' All rights reserved.';
    } else {
      body.push(line);
      after = false;
    }
  }
  let text2 = body.join('\n').replace(/\n{3,}/g, '\n\n').trim();
  // Common indentation off, so an indented text doesn't render as a code block inside the fence.
  const indents = text2.split('\n').filter((l) => l.trim()).map((l) => l.match(/^ */)[0].length);
  const cut = indents.length ? Math.min(...indents) : 0;
  if (cut) text2 = text2.split('\n').map((l) => l.slice(cut)).join('\n');
  return { copyright: [...new Set(copyright)], body: text2 };
}

// ---------- the license text registry (deduplicated at the end)

/** key -> {label, body, members: [{who, copyright}]} */
const texts = new Map();

/**
 * The grouping key of a license body: its words, ignoring case, spacing and URL schemes. Apache-2.0 copies differ in
 * their title and in whether they carry the "how to apply" appendix, so those parts don't count; any other text a
 * file adds (an exception, a preamble) does.
 */
function groupKey(label, body) {
  let t = body.toLowerCase().replace(/https?:\/\//g, '').replace(/\s+/g, ' ').trim().replace(TITLE, '');
  const start = 'terms and conditions for use, reproduction, and distribution';
  const end = 'end of terms and conditions';
  const a = t.indexOf(start);
  const b = t.indexOf(end);
  if (label === 'Apache-2.0' && a >= 0 && b > a) {
    const pre = t.slice(0, a).replace(/^(apache license )?version 2\.0, january 2004 www\.apache\.org\/licenses\/? ?/, '');
    let post = t.slice(b + end.length).trim();
    if (post.startsWith('appendix: how to apply the apache license to your work')
      && post.endsWith('limitations under the license.')) post = '';
    t = `${pre.trim()}|${t.slice(a, b)}|${post}`;
  }
  return createHash('sha256').update(`${label}\n${t}`).digest('hex');
}

/**
 * Records one license file of a component. who: "Go module `x` v1". declared: the component's declared license.
 * label: use this label instead of identifying the text (hand-maintained entries).
 */
function addText(who, fileText, { declared, strict = false, file = '', label: given }) {
  // NOTICE files and a dependency's own third-party notices (modernc.org/libc's LICENSE-3RD-PARTY.md) are kept whole.
  const isNotice = /(^|[-_.])notice|(3rd|third)[-_ ]?party/i.test(file);
  const { copyright, body } = isNotice
    ? { copyright: [], body: fileText.replace(/[ \t]+$/gm, '').replace(/\n{3,}/g, '\n\n').trim() }
    : splitCopyright(fileText);
  if (!body.trim()) {
    // Only copyright lines: attach them to the declared license's text.
    if (!declared) fail(`${file || who} holds only copyright lines and the component declares no license`);
    return addRef(who, declared, copyright);
  }
  let label = given ?? (isNotice ? 'NOTICE' : identify(body) ?? identify(fileText));
  if (!label) {
    if (strict || !declared) fail(`can't identify the license in ${file || who}: add a fingerprint to identify()`);
    label = declared;
  }
  const key = groupKey(label, body);
  let g = texts.get(key);
  if (!g) texts.set(key, (g = { label, body, members: [] }));
  g.members.push({ who, copyright });
  return label;
}

/** refs: components whose license text is a standard one printed anyway (by SPDX id): {id -> [{who, copyright}]} */
const refs = new Map();
function addRef(who, id, copyright = []) {
  if (!refs.has(id)) refs.set(id, []);
  refs.get(id).push({ who, copyright });
  return id;
}

// ---------- Go

function goSection() {
  const mods = new Map(); // path -> version
  for (const t of GO_TARGETS) {
    const [goos, goarch] = t.split('/');
    const out = run('go', ['list', '-deps', '-tags', 'webdist', '-f', '{{with .Module}}{{.Path}} {{.Version}}{{end}}',
      './cmd/retake'], { cwd: ROOT, env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0', GOFLAGS: '-mod=readonly' } });
    for (const line of out.split('\n')) {
      const [path, version] = line.trim().split(' ');
      if (path && version) mods.set(path, version); // the main module has no version
    }
  }
  const list = JSON.parse(`[${run('go', ['list', '-m', '-json', ...[...mods.keys()].sort(byKey)], { cwd: ROOT })
    .replace(/}\s*{/g, '},{')}]`);
  const rows = [];
  for (const m of list) {
    if (!m.Dir || !existsSync(m.Dir)) fail(`Go module ${m.Path} isn't downloaded: run \`go mod download\``);
    const files = licenseFiles(m.Dir);
    if (!files.length) fail(`Go module ${m.Path} has no license file`);
    const who = `Go module \`${m.Path}\` ${m.Version}`;
    const ids = files.map((f) => addText(who, read(f), { strict: true, file: relative(m.Dir, f) }));
    rows.push([`\`${m.Path}\``, m.Version, [...new Set(ids.filter((i) => i !== 'NOTICE'))].join(' AND ')]);
  }
  const goroot = run('go', ['env', 'GOROOT']).trim();
  const gover = run('go', ['env', 'GOVERSION']).trim();
  addText(`the Go standard library ${gover}`, read(join(goroot, 'LICENSE')), { strict: true, file: 'LICENSE' });
  return { rows: [['the Go standard library', gover, 'BSD-3-Clause'], ...rows], dirs: new Map(list.map((m) => [m.Path, m.Dir])) };
}

// ---------- the review page

function webSection() {
  const web = join(ROOT, 'web');
  const vite = join(web, 'node_modules', 'vite', 'bin', 'vite.js');
  if (!existsSync(vite)) fail('web/node_modules is missing: run `npm ci` in web/');
  const outDir = join(SCRATCH, 'web-dist');
  run(process.execPath, [vite, 'build', '--sourcemap', '--outDir', outDir, '--emptyOutDir', '--logLevel', 'error'],
    { cwd: web });
  const pkgDirs = new Set();
  const maps = [];
  const walk = (d) => {
    for (const n of readdirSync(d)) {
      const p = join(d, n);
      if (statSync(p).isDirectory()) walk(p);
      else if (n.endsWith('.map')) maps.push(p);
    }
  };
  walk(outDir);
  if (!maps.length) fail('the page build wrote no sourcemaps');
  const marker = `${sep}node_modules${sep}`;
  for (const m of maps) {
    const map = JSON.parse(read(m));
    for (const s of map.sources) {
      const abs = resolve(dirname(m), map.sourceRoot ?? '', s);
      const i = abs.lastIndexOf(marker);
      if (i < 0) {
        if (!abs.startsWith(join(web, 'src') + sep)) fail(`unexpected source in the page bundle: ${s}`);
        continue;
      }
      const parts = abs.slice(i + marker.length).split(sep);
      pkgDirs.add(abs.slice(0, i + marker.length) + (parts[0].startsWith('@') ? `${parts[0]}${sep}${parts[1]}` : parts[0]));
    }
  }
  const rows = [];
  const bundled = new Set();
  for (const dir of [...pkgDirs].sort(byKey)) {
    const pj = JSON.parse(read(join(dir, 'package.json')));
    const files = licenseFiles(dir);
    const who = `npm package \`${pj.name}\` ${pj.version}`;
    let declared = typeof pj.license === 'string' ? pj.license : pj.license?.type;
    if (!files.length) fail(`${who} (bundled into the page) has no license file`);
    const ids = files.map((f) => addText(who, read(f), { declared, file: relative(dir, f) }));
    declared ??= [...new Set(ids.filter((i) => i !== 'NOTICE'))].join(' AND ');
    rows.push([`\`${pj.name}\``, pj.version, declared]);
    bundled.add(`${pj.name}@${pj.version}`);
  }
  rows.sort((a, b) => byKey(a[0].replace(/`/g, ''), b[0].replace(/`/g, '')));
  return { rows, bundled };
}

/** The packages of an npm lock file, except the root and those in skip: [name, version, license]. */
function lockPackages(lockPath, skip = new Set()) {
  if (!existsSync(lockPath)) fail(`${relative(ROOT, lockPath)} is missing`);
  const lock = JSON.parse(read(lockPath));
  const seen = new Map();
  for (const [path, p] of Object.entries(lock.packages ?? {})) {
    if (!path || p.link) continue;
    const name = p.name ?? path.slice(path.lastIndexOf('node_modules/') + 'node_modules/'.length);
    const key = `${name}@${p.version}`;
    if (skip.has(key) || seen.has(key)) continue;
    let license = p.license;
    if (!license) {
      // The lock file doesn't say: read the installed package (so the output never depends on what happens to be
      // installed, a missing install is an error).
      const dir = join(dirname(lockPath), path);
      if (!existsSync(join(dir, 'package.json'))) {
        fail(`${relative(ROOT, lockPath)} gives no license for ${key}: run \`npm ci\` in ${relative(ROOT, dirname(lockPath))}/`);
      }
      license = JSON.parse(read(join(dir, 'package.json'))).license
        ?? licenseFiles(dir).map((f) => identify(read(f))).find(Boolean) ?? 'not declared';
    }
    seen.set(key, [`\`${name}\``, p.version, typeof license === 'string' ? license : license.type]);
  }
  const bare = (s) => s.replace(/`/g, '');
  return [...seen.values()].sort((a, b) => byKey(bare(a[0]), bare(b[0])) || byKey(a[1], b[1]));
}

// ---------- Rust

function rustSection(extra) {
  const editor = join(ROOT, 'editor');
  const args = ['metadata', '--format-version', '1', '--locked', ...RUST_TARGETS.flatMap((t) => ['--filter-platform', t])];
  const meta = JSON.parse(run('cargo', args, { cwd: editor }));
  const pkgs = new Map(meta.packages.map((p) => [p.id, p]));
  const nodes = new Map(meta.resolve.nodes.map((n) => [n.id, n]));
  const members = new Set(meta.workspace_members);
  const root = meta.packages.find((p) => p.name === EDIT && members.has(p.id));
  if (!root) fail(`no ${EDIT} package in editor/`);
  const seen = new Set([root.id]);
  const queue = [root.id];
  while (queue.length) {
    const n = nodes.get(queue.shift());
    for (const d of n?.deps ?? []) {
      if (!d.dep_kinds.some((k) => k.kind === null) || seen.has(d.pkg)) continue;
      seen.add(d.pkg);
      queue.push(d.pkg);
    }
  }
  const crates = [...seen].filter((id) => !members.has(id)).map((id) => pkgs.get(id))
    .sort((a, b) => byKey(a.name, b.name) || byKey(a.version, b.version));
  const byName = new Map(crates.map((c) => [c.name, c]));
  for (const [name, version] of Object.entries(extra.native.requires)) {
    const c = byName.get(name);
    if (!c) fail(`scripts/notices-extra.json requires ${name}, which ${EDIT} no longer uses: update it`);
    if (c.version !== version) {
      fail(`${name} is ${c.version}, but scripts/notices-extra.json was reviewed for ${version}: check what it bundles, `
        + 'then update "requires"');
    }
  }
  const rows = [];
  const missing = [];
  for (const c of crates) {
    const dir = dirname(c.manifest_path);
    if (!existsSync(dir)) fail(`crate ${c.name} ${c.version} isn't downloaded: run \`cargo fetch --locked\` in editor/`);
    const files = licenseFiles(dir);
    if (c.license_file && !files.some((f) => f === resolve(dir, c.license_file))) files.push(resolve(dir, c.license_file));
    const who = `Rust crate \`${c.name}\` ${c.version}`;
    const declared = c.license ?? (c.license_file ? null : undefined);
    if (!files.length) {
      missing.push(`${c.name} ${c.version}`);
      // The declared SPDX license's standard text is printed in the texts section.
      for (const id of (declared ?? '').split(/\s+(?:OR|AND|WITH)\s+|[()/]/).map((s) => s.trim()).filter(Boolean)) {
        addRef(who, id, authors(c));
      }
    }
    const ids = files.map((f) => addText(who, read(f), { declared: declared ?? undefined, file: relative(dir, f) }));
    const license = declared ?? [...new Set(ids)].join(' AND ');
    rows.push([`\`${c.name}\``, c.version, license.replace(/\//g, ' OR '), files.length ? '' : 'none in the crate']);
  }
  return { rows, missing, dirs: new Map(crates.map((c) => [c.name, dirname(c.manifest_path)])) };
}

/** A crate's authors (without email addresses), as its notice when it ships no license file. */
function authors(c) {
  const names = (c.authors ?? []).map((a) => a.replace(/<[^>]*>/g, '').trim()).filter(Boolean);
  return names.length ? [`Authors: ${names.join(', ')}`] : [];
}

// ---------- rendering

const sub = (s) => s.replaceAll('{NAME}', NAME);
/** A paragraph, wrapped at 118 columns. */
function para(text) {
  const lines = [];
  let cur = '';
  for (const w of text.split(/\s+/).filter(Boolean)) {
    if (cur && cur.length + 1 + w.length > 118) {
      lines.push(cur);
      cur = w;
    } else cur = cur ? `${cur} ${w}` : w;
  }
  if (cur) lines.push(cur);
  return `${lines.join('\n')}\n`;
}
const esc = (s) => String(s).replace(/\|/g, '\\|');
function table(head, rows) {
  const out = [`| ${head.join(' | ')} |`, `|${head.map(() => '---').join('|')}|`];
  for (const r of rows) out.push(`| ${r.map(esc).join(' | ')} |`);
  return out.join('\n');
}
function fence(body) {
  let f = '```';
  while (body.includes(f)) f += '`';
  return `${f}text\n${body}\n${f}`;
}

function render() {
  const extra = JSON.parse(read(EXTRA));
  const go = goSection();
  const web = webSection();
  const rust = rustSection(extra);

  const native = extra.native.libraries.map((l) => {
    const who = `${l.name} (${l.version})`;
    if (l.text) addText(who, l.text, { declared: l.license, label: l.license, file: l.text_source });
    else if (l.crate_file) {
      const dir = rust.dirs.get(l.crate_file.crate);
      if (!dir) fail(`notices-extra.json: ${l.name} names crate ${l.crate_file.crate}, which isn't a dependency`);
      addText(who, read(join(dir, l.crate_file.path)), { declared: l.license, label: l.license, file: l.crate_file.path });
    } else if (l.text_ref) addRef(who, l.text_ref, l.copyright ?? []);
    else fail(`notices-extra.json: ${l.name} has no license text`);
    return [l.name, sub(l.version), l.license, l.url];
  });
  const fonts = extra.fonts.map((f) => {
    const who = `the font ${f.name}`;
    if (f.repo_file) addText(who, read(join(ROOT, f.repo_file)), { declared: f.license, file: f.repo_file });
    else if (f.go_file) {
      const dir = go.dirs.get(f.go_file.module);
      if (!dir) fail(`notices-extra.json: ${f.name} names Go module ${f.go_file.module}, which isn't a dependency`);
      addText(who, read(join(dir, f.go_file.path)), { declared: f.license, file: f.go_file.path });
    }
    return [f.name, `\`${sub(f.binary)}\``, sub(f.use), f.license];
  });

  const webDev = lockPackages(join(ROOT, 'web', 'package-lock.json'), web.bundled);
  const e2eDev = lockPackages(join(ROOT, 'e2e', 'package-lock.json'));

  // Standard texts that refs point at must be printed: find a group with that label.
  for (const [id, who] of [...refs].sort((a, b) => byKey(a[0], b[0]))) {
    // The most used copy of that license (the standard text), not a short header that merely names it.
    const g = [...texts.values()].filter((x) => x.label === id)
      .sort((a, b) => b.members.length - a.members.length || byKey(a.body, b.body))[0];
    if (!g) fail(`no ${id} license text to point ${who.map((w) => w.who).join(', ')} at`);
    g.members.push(...who);
  }

  const groups = [...texts.values()].map((g) => ({
    ...g,
    members: [...new Map(g.members.map((m) => [`${m.who}\n${m.copyright.join('\n')}`, m])).values()]
      .sort((a, b) => byKey(a.who, b.who) || byKey(a.copyright.join('\n'), b.copyright.join('\n'))),
  })).sort((a, b) => byKey(a.label, b.label) || byKey(a.members[0].who, b.members[0].who));

  const L = [];
  L.push('# Third-party notices', '');
  L.push(para(`Generated by \`scripts/notices.mjs\`. Don't edit it by hand: run \`mise exec -- node scripts/notices.mjs\`. `
    + 'CI fails when this file is out of date.'));
  L.push(para(`\`${NAME}\` and \`${EDIT}\` contain the open-source software listed below, each part with its license. `
    + 'The license texts and copyright notices follow at the end, each text once.'));

  L.push(`## \`${NAME}\` (Go)`, '');
  L.push(para(`Built with CGO off for ${GO_TARGETS.join(', ')}. It contains the Go standard library and these modules:`));
  L.push(table(['Module', 'Version', 'License'], go.rows), '');
  L.push(para('`golang.org/x/image` carries the Go fonts: Go Bold is compiled in (see Fonts).'));

  L.push(`## The review page (embedded in \`${NAME}\`)`, '');
  L.push('The npm packages bundled into the built page:', '');
  L.push(table(['Package', 'Version', 'License'], web.rows), '');

  L.push(`## \`${EDIT}\` (Rust)`, '');
  L.push(para(`The crates compiled into \`${EDIT}\` for ${RUST_TARGETS.join(', ')}: normal dependencies only. `
    + 'Build scripts and test-only crates are not in the binary and are not listed. Procedural macros, which run while '
    + 'compiling, are listed.'));
  L.push(table(['Crate', 'Version', 'License', 'Note'], rust.rows), '');
  if (rust.missing.length) {
    L.push(para('A crate marked "none in the crate" ships no license file. Its authors and the standard text of its '
      + 'declared license are in the texts below.'));
  }

  L.push(`## Native libraries in \`${EDIT}\``, '');
  L.push(para(sub(extra.native.note)));
  L.push(table(['Library', 'Version', 'License', 'Upstream'], native), '');
  L.push(para(sub(extra.native.system)));
  L.push(para(extra.native.attribution));

  L.push('## Fonts', '');
  L.push(table(['Font', 'In', 'Use', 'License'], fonts), '');

  const list = (items) => {
    for (const p of items) L.push(para(`- ${sub(p)}`).replace(/\n(?=.)/g, '\n  ').trimEnd());
    L.push('');
  };
  L.push('## Shipped as separate programs', '');
  L.push(para('The release archives and npm packages carry these programs next to the binaries above. Each is its own '
    + 'program, run as a separate process, never linked; their license texts and notices are in the archive\'s '
    + '`licenses/` folder.'));
  list(extra.shipped);
  L.push('## Run as separate programs (not shipped)', '');
  list(extra.programs);

  L.push('## Development and test only (not distributed)', '');
  L.push(para('These npm packages build and test the page and run the end-to-end tests. None of them is in a release. '
    + 'The packages bundled into the page are listed above.'));
  L.push(`### web/ (${webDev.length} packages)`, '');
  L.push(table(['Package', 'Version', 'License'], webDev), '');
  L.push(`### e2e/ (${e2eDev.length} packages)`, '');
  L.push(table(['Package', 'Version', 'License'], e2eDev), '');

  L.push('## License texts', '');
  L.push('Each text is printed once, after the components it covers and their copyright notices.', '');
  groups.forEach((g, i) => {
    L.push(`### ${i + 1}. ${g.label}`, '');
    for (const m of g.members) {
      L.push(m.copyright.length ? `- ${m.who}: ${m.copyright.join('; ')}` : `- ${m.who}`);
    }
    L.push('', fence(g.body), '');
  });
  const md = `${L.join('\n').replace(/\n+$/, '')}\n`;

  for (const bad of [ROOT, process.env.HOME].filter(Boolean)) {
    if (md.includes(bad)) fail(`the output contains a local path (${bad}); fix the script`);
  }
  return { md, counts: { go: go.rows.length, web: web.rows.length, rust: rust.rows.length, native: native.length,
    fonts: fonts.length, webDev: webDev.length, e2eDev: e2eDev.length, texts: groups.length }, missing: rust.missing };
}

rmSync(SCRATCH, { recursive: true, force: true });
mkdirSync(SCRATCH, { recursive: true });
let result;
try {
  result = render();
} finally {
  rmSync(SCRATCH, { recursive: true, force: true });
}
const { md, counts, missing } = result;
const summary = Object.entries(counts).map(([k, v]) => `${k} ${v}`).join(', ');
if (check) {
  const have = existsSync(OUT) ? read(OUT) : '';
  if (have !== md) {
    const a = have.split('\n');
    const b = md.split('\n');
    const diff = [];
    for (let i = 0; i < Math.max(a.length, b.length) && diff.length < 10; i++) {
      if (a[i] !== b[i]) diff.push(`  line ${i + 1}\n    have: ${a[i] ?? '(end)'}\n    want: ${b[i] ?? '(end)'}`);
    }
    console.error(`notices: THIRD_PARTY_NOTICES.md is out of date: run \`mise exec -- node scripts/notices.mjs\`\n${diff.join('\n')}`);
    process.exit(1);
  }
  console.log(`notices: THIRD_PARTY_NOTICES.md is current (${summary})`);
} else {
  writeFileSync(OUT, md);
  console.log(`notices: wrote THIRD_PARTY_NOTICES.md, ${md.length} bytes (${summary})`);
  if (missing.length) console.log(`notices: crates without a license file: ${missing.join(', ')}`);
}
