#!/usr/bin/env node
// The npm install end to end, on this computer, from the tarballs scripts/npm-pack.sh made, with nothing published:
// a registry of its own (this script, on 127.0.0.1) serves them, so npm resolves the optional platform package the
// way it does from npmjs.org. In a scratch HOME with empty npm caches it checks that:
//
//   1. `npm i -g retake-cli` downloads the command and only this platform's package, and `retake --version` runs;
//   2. the bundled ffmpeg has libx264 and zimg (`zscale`), by running it: build config, filters, a real encode;
//   3. `retake --no-open --port N` (what plain `retake` runs in a terminal) serves the setup page, finds the bundled
//      ffmpeg and herdr, and stops on SIGINT, taking the Go process with it;
//   4. an agent can run what its prompt names (`retake working`, `retake done`) with nothing on PATH, as in a herdr
//      or tmux pane under nvm: the prompt names the platform package's Go binary, not the node launcher;
//   5. `npx retake-cli` downloads once, a second `npx retake-cli` downloads nothing, and `npx retake-cli@latest` picks
//      up a newer release (reusing a platform package it already has).
//
//   node scripts/npm-smoke.mjs [DIR]          DIR: the tarballs (default dist/npm)
//   RETAKE_SMOKE_PORTS=9180,9181,9182         ports for the registry and the two servers (default: free ones)
//   RETAKE_SMOKE_DIR=DIR                      scratch folder (default .retake-work/npm-smoke, deleted when it passes)
//   RETAKE_SMOKE_KEEP=1                       keep the scratch folder
//
// Runs on Linux, macOS and Windows (the release workflow runs it on each). Never touches the real npm registry.
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createReadStream, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { createServer as netServer } from 'node:net';
import { basename, delimiter, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createGunzip } from 'node:zlib';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const TARBALLS = resolve(process.argv[2] || join(ROOT, 'dist', 'npm'));
const WIN = process.platform === 'win32';
const S = resolve(process.env.RETAKE_SMOKE_DIR || join(ROOT, '.retake-work', 'npm-smoke'));
const EXTRA = join(S, 'registry-extra'); // tarballs the test adds (a newer release)
const results = {};
const children = new Set();

const log = (...a) => console.log('npm-smoke:', ...a);
function check(cond, what, detail = '') {
  if (!cond) throw new Error(`FAILED: ${what}${detail ? `\n${detail}` : ''}`);
  log(`ok - ${what}`);
}

// ---------------------------------------------------------------------------------------------------------------
// The registry: GET /<name> is the packument, GET /<name>/-/<file>.tgz the tarball. It reads the tarballs' own
// package.json, so os, cpu and libc reach npm as they would from npmjs.org, and logs each tarball download.

/** package/package.json of a .tgz, read from a stream (the platform tarballs are ~90 MB). */
function packageJson(file) {
  return new Promise((ok, fail) => {
    let buf = Buffer.alloc(0);
    let skip = 0;
    let want = 0;
    let pending = null;
    const gz = createGunzip();
    const input = createReadStream(file);
    const stop = (v, e) => {
      input.destroy();
      gz.destroy();
      e ? fail(e) : ok(v);
    };
    gz.on('data', (chunk) => {
      buf = Buffer.concat([buf, chunk]);
      for (;;) {
        if (skip > 0) {
          const n = Math.min(skip, buf.length);
          if (pending) pending.push(buf.subarray(0, n));
          buf = buf.subarray(n);
          skip -= n;
          if (skip > 0) return;
          if (pending) return stop(JSON.parse(Buffer.concat(pending).subarray(0, want).toString('utf8')));
        }
        if (buf.length < 512) return;
        const h = buf.subarray(0, 512);
        buf = buf.subarray(512);
        const name = h.subarray(0, 100).toString('utf8').replace(/\0.*$/s, '');
        if (!name) return stop(null, new Error(`${file}: no package/package.json`));
        const size = parseInt(h.subarray(124, 136).toString('utf8').replace(/\0/g, '').trim() || '0', 8);
        skip = Math.ceil(size / 512) * 512;
        if (name === 'package/package.json') {
          pending = [];
          want = size;
        }
      }
    });
    gz.on('error', (e) => stop(null, e));
    input.on('error', (e) => stop(null, e));
    input.pipe(gz);
  });
}

function digests(file) {
  return new Promise((ok, fail) => {
    const sha512 = createHash('sha512');
    const sha1 = createHash('sha1');
    createReadStream(file)
      .on('data', (c) => (sha512.update(c), sha1.update(c)))
      .on('end', () => ok({ integrity: `sha512-${sha512.digest('base64')}`, shasum: sha1.digest('hex') }))
      .on('error', fail);
  });
}

const known = new Map(); // file -> {mtime, pkg, dist}
async function catalog(base) {
  const files = [];
  for (const dir of [TARBALLS, EXTRA]) {
    if (!existsSync(dir)) continue;
    for (const f of readdirSync(dir)) if (f.endsWith('.tgz')) files.push(join(dir, f));
  }
  const byName = new Map();
  for (const file of files) {
    const mtime = statSync(file).mtimeMs;
    let k = known.get(file);
    if (!k || k.mtime !== mtime) {
      k = { mtime, pkg: await packageJson(file), dist: await digests(file) };
      known.set(file, k);
    }
    const doc = byName.get(k.pkg.name) ?? { name: k.pkg.name, 'dist-tags': {}, versions: {}, time: {}, _latest: 0 };
    doc.versions[k.pkg.version] = {
      ...k.pkg,
      _id: `${k.pkg.name}@${k.pkg.version}`,
      dist: { ...k.dist, tarball: `${base}/${k.pkg.name}/-/${basename(file)}` },
    };
    doc.time[k.pkg.version] = new Date(mtime).toISOString();
    if (mtime >= doc._latest) {
      doc._latest = mtime; // the newest file is the latest release
      doc['dist-tags'].latest = k.pkg.version;
    }
    doc._files = { ...doc._files, [basename(file)]: file };
    byName.set(k.pkg.name, doc);
  }
  return byName;
}

const downloads = []; // tarball file names, in order
let packuments = 0;
function startRegistry(port) {
  const base = `http://127.0.0.1:${port}`;
  const server = createServer(async (req, res) => {
    try {
      const path = decodeURIComponent(new URL(req.url, base).pathname);
      const docs = await catalog(base);
      const tgz = path.match(/^\/(.+)\/-\/([^/]+\.tgz)$/);
      if (req.method === 'GET' && tgz && docs.get(tgz[1])?._files[tgz[2]]) {
        downloads.push(tgz[2]);
        res.writeHead(200, { 'Content-Type': 'application/octet-stream' });
        createReadStream(docs.get(tgz[1])._files[tgz[2]]).pipe(res);
        return;
      }
      const doc = req.method === 'GET' && docs.get(path.slice(1));
      if (doc) {
        packuments++;
        const { _latest, _files, ...body } = doc;
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(body));
        return;
      }
      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end('{"error":"not found"}');
    } catch (e) {
      res.writeHead(500);
      res.end(String(e));
    }
  });
  return new Promise((ok, fail) => server.once('error', fail).listen(port, '127.0.0.1', () => ok({ server, base })));
}

// ---------------------------------------------------------------------------------------------------------------
// Processes.

const ports = (process.env.RETAKE_SMOKE_PORTS || '').split(',').filter(Boolean).map(Number);
function freePort() {
  if (ports.length) return Promise.resolve(ports.shift());
  return new Promise((ok, fail) => {
    const s = netServer().once('error', fail).listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => ok(port));
    });
  });
}

/** Runs a command to its end: {code, signal, out, err}. npm, npx and the launcher are .cmd files on Windows. */
function run(cmd, args, { env, cwd = S, timeout = 300_000, input } = {}) {
  return new Promise((ok) => {
    const child = spawn(cmd, args, { env, cwd, shell: WIN, stdio: ['pipe', 'pipe', 'pipe'] });
    children.add(child);
    let out = '';
    let err = '';
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', (d) => (err += d));
    // A program that exits before reading closes its stdin: writing then is EPIPE, which Node 18 throws unhandled.
    child.stdin.on('error', () => {});
    if (input === undefined) child.stdin.end();
    else child.stdin.end(input);
    const timer = setTimeout(() => stopTree(child), timeout);
    child.on('close', (code, signal) => {
      clearTimeout(timer);
      children.delete(child);
      ok({ code, signal, out, err });
    });
  });
}

/** Starts a server in the background; resolves once it printed its URL and answers api/ping. */
async function serve(args, env, name) {
  const child = spawn('retake', args, { env, cwd: S, shell: WIN, stdio: ['ignore', 'pipe', 'pipe'] });
  children.add(child);
  let out = '';
  child.stdout.on('data', (d) => (out += d));
  child.stderr.on('data', (d) => (out += d));
  child.exited = new Promise((ok) => child.on('close', (code, signal) => (children.delete(child), ok({ code, signal }))));
  const until = Date.now() + 60_000;
  while (Date.now() < until) {
    const m = out.match(/http:\/\/127\.0\.0\.1:\d+\//);
    if (m && (await fetch(`${m[0]}api/ping`).then((r) => r.ok, () => false))) return { child, url: m[0], log: () => out };
    if (child.exitCode !== null) break;
    await sleep(200);
  }
  stopTree(child);
  throw new Error(`FAILED: ${name} didn't start\n${out}`);
}

function stopTree(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  if (WIN) spawn('taskkill', ['/pid', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  else child.kill('SIGTERM');
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function waitFor(what, fn, timeout = 90_000) {
  const until = Date.now() + timeout;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > until) throw new Error(`FAILED: timed out waiting for ${what}`);
    await sleep(250);
  }
}
const json = async (url, body) =>
  (
    await fetch(url, body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  ).json();
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

// ---------------------------------------------------------------------------------------------------------------

async function main() {
  const tgz = existsSync(TARBALLS) ? readdirSync(TARBALLS).filter((f) => f.endsWith('.tgz')) : [];
  if (!tgz.length) throw new Error(`no tarballs in ${TARBALLS}: run scripts/npm-pack.sh first`);
  const pkgs = await Promise.all(tgz.map((f) => packageJson(join(TARBALLS, f))));
  const main = pkgs.find((p) => p.bin);
  if (!main) throw new Error(`no package with a bin in ${TARBALLS}`);
  const NAME = main.name;
  const V = main.version;
  const PLATFORM = `${NAME}-${process.platform}-${process.arch}`;
  const platformFile = tgz.find((f) => f.startsWith(`${PLATFORM}-${V}`));
  if (!platformFile) throw new Error(`no ${PLATFORM} ${V} in ${TARBALLS}`);
  results.tarballs = Object.fromEntries(tgz.map((f) => [f, statSync(join(TARBALLS, f)).size]));

  rmSync(S, { recursive: true, force: true });
  for (const d of ['home', 'prefix', 'npx-prefix', 'config', 'state', 'cache', 'data', 'out', 'videos/promo', 'empty', EXTRA]) {
    mkdirSync(resolve(S, d), { recursive: true });
  }
  const rp = await freePort();
  const { server, base } = await startRegistry(rp);
  log(`registry ${base} serving ${tgz.length} tarballs from ${TARBALLS}`);
  try {
    // A clean environment: no npm settings, no Retake settings, a HOME of its own, the npm prefix's bin first.
    const env = Object.fromEntries(
      Object.entries(process.env).filter(([k]) => !/^(npm_|RETAKE_|XDG_)/i.test(k) && !/^(HOME|USERPROFILE)$/i.test(k)),
    );
    const prefix = join(S, 'prefix');
    const globalBin = WIN ? prefix : join(prefix, 'bin');
    writeFileSync(join(S, 'npmrc'), '');
    Object.assign(env, {
      HOME: join(S, 'home'),
      USERPROFILE: join(S, 'home'),
      XDG_CONFIG_HOME: join(S, 'config'),
      XDG_STATE_HOME: join(S, 'state'),
      XDG_CACHE_HOME: join(S, 'cache'),
      XDG_DATA_HOME: join(S, 'data'),
      npm_config_registry: `${base}/`,
      npm_config_cache: join(S, 'npm-cache'),
      npm_config_prefix: prefix,
      npm_config_userconfig: join(S, 'npmrc'),
      npm_config_audit: 'false',
      npm_config_fund: 'false',
      npm_config_update_notifier: 'false',
      npm_config_loglevel: 'warn',
      RETAKE_ENCODER: 'x264',
      PATH: [globalBin, process.env.PATH ?? process.env.Path ?? ''].join(delimiter),
    });
    if (WIN) delete env.Path;

    // 1. npm i -g: the command and only this platform's package.
    let t = Date.now();
    let r = await run('npm', ['install', '--global', NAME], { env });
    check(r.code === 0, `npm i -g ${NAME}`, r.out + r.err);
    results.installSeconds = (Date.now() - t) / 1000;
    check(
      downloads.length === 2 && downloads.includes(`${NAME}-${V}.tgz`) && downloads.includes(platformFile),
      `it downloaded ${NAME} and ${PLATFORM} only`,
      downloads.join(', '),
    );
    r = await run('retake', ['--version'], { env });
    check(r.code === 0 && r.out.startsWith(`retake ${V}`), `retake --version says ${V}`, r.out + r.err);
    const npmRoot = (await run('npm', ['root', '--global'], { env })).out.trim();
    const pkgDir = dirname(createRequire(join(npmRoot, NAME, 'package.json')).resolve(`${PLATFORM}/package.json`));
    const exe = WIN ? '.exe' : '';
    const bin = (n) => join(pkgDir, 'bin', n + exe);
    const tools = ['retake', 'retake-edit', 'ffmpeg', 'ffprobe', 'herdr'];
    for (const n of tools) check(existsSync(bin(n)), `${PLATFORM} has ${n}${exe}`);
    check(existsSync(join(pkgDir, 'bin', 'retake-bundle.json')), `${PLATFORM} has retake-bundle.json next to retake`);
    r = await run(bin('retake-edit'), ['--version'], { env });
    check(r.code === 0 && /retake-edit/.test(r.out), 'the bundled retake-edit runs', r.out + r.err);

    // 2. ffmpeg: libx264 and zimg, which retake-edit renders with.
    r = await run(bin('ffmpeg'), ['-hide_banner', '-buildconf'], { env });
    check(/--enable-libx264/.test(r.out + r.err) && /--enable-libzimg/.test(r.out + r.err), 'ffmpeg is built with libx264 and zimg', r.err);
    r = await run(bin('ffmpeg'), ['-hide_banner', '-filters'], { env });
    check(/\szscale\s/.test(r.out), 'ffmpeg has the zscale filter');
    const clip = join(S, 'out', 'zscale.mp4');
    r = await run(bin('ffmpeg'), ['-hide_banner', '-v', 'error', '-f', 'lavfi', '-i', 'testsrc2=s=640x360:d=1:r=30', '-vf',
      'zscale=w=1280:h=720:filter=spline36:dither=none:matrixin=709:rangein=limited,format=gbrp,format=yuv420p',
      '-c:v', 'libx264', '-preset', 'veryfast', '-threads', '2', '-y', clip], { env });
    check(r.code === 0, 'ffmpeg encodes through zscale with libx264', r.err);
    r = await run(bin('ffprobe'), ['-v', 'error', '-show_entries', 'stream=codec_name,width', '-of', 'csv=p=0', clip], { env });
    check(r.out.trim() === 'h264,1280', 'ffprobe reads it back: h264, 1280 wide', r.out + r.err);

    // 3. Plain `retake` (here with its options, which are start's): the setup page, then Ctrl+C.
    const p1 = await freePort();
    const start = await serve(['--no-open', '--port', String(p1)], env, 'retake --no-open');
    const page = await fetch(start.url);
    check(page.ok && /<html/i.test(await page.text()), `retake --no-open serves the setup page at ${start.url}`);
    const setup = await json(`${start.url}api/setup`);
    check(setup.ffmpeg === true, 'the setup page finds the bundled ffmpeg');
    check(setup.places?.some((p) => p.id === 'herdr' && p.available && p.bundled), 'and the bundled herdr');
    if (WIN) check(existsSync(join(pkgDir, 'bin', 'conpty', 'conpty.dll')), "herdr.exe's conpty/ is next to it");
    const state = JSON.parse(readFileSync(join(S, 'state', 'retake', 'start.json'), 'utf8'));
    check(state.pid !== start.child.pid, 'the server is the Go binary, a child of the node launcher');
    if (WIN) {
      stopTree(start.child);
      await start.child.exited;
    } else {
      start.child.kill('SIGINT');
      const end = await Promise.race([start.child.exited, sleep(20_000).then(() => null)]);
      check(end !== null, 'SIGINT to the launcher stops it', start.log());
      await waitFor('the Go process to exit', () => !alive(state.pid), 10_000);
      check(!existsSync(join(S, 'state', 'retake', 'start.json')), 'and the Go process, which cleaned up after itself');
    }

    // 4. An agent: its prompt names a retake it can run with nothing on PATH.
    const video = join(S, 'videos', 'promo', 'promo_v1.mp4');
    r = await run(bin('ffmpeg'), ['-hide_banner', '-v', 'error', '-f', 'lavfi', '-i', 'testsrc2=s=320x180:d=2:r=30', '-f', 'lavfi',
      '-i', 'sine=d=2', '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-shortest', '-y', video], { env });
    check(r.code === 0, 'a 2 s test video made with the bundled ffmpeg', r.err);
    const out = join(S, 'out');
    const agent = join(S, 'fake-agent.mjs');
    writeFileSync(agent, FAKE_AGENT);
    mkdirSync(join(S, 'config', 'retake'), { recursive: true });
    writeFileSync(join(S, 'config', 'retake', 'config.toml'), [
      'name = "Smoke Test"',
      'open_browser = false',
      'find_agents = false',
      '',
      '[[agents]]',
      'id = "fake"',
      'name = "Fake agent"',
      `command = ["node", ${JSON.stringify(agent)}]`,
      `env = { SMOKE_OUT = ${JSON.stringify(out)}, SMOKE_VIDEO = ${JSON.stringify(video)} }`,
      '',
    ].join('\n'));
    const p2 = await freePort();
    const open = await serve(['open', video, '--no-open', '--port', String(p2), '--idle-exit', '0'], env, 'retake open');
    try {
      await waitFor('the local worker', async () => (await json(`${open.url}api/automation`)).blocked === undefined, 30_000);
      const note = await json(`${open.url}api/notes`, { version: 'v1', t: 1, text: 'Make the title bigger' });
      check(Number.isInteger(note.id), `a typed comment (note ${note.id})`);
      const sent = await fetch(`${open.url}api/automation/tasks`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ notes: [note.id] }),
      });
      check(sent.ok, 'Send');
      await waitFor('the fake agent', () => existsSync(join(out, 'agent.done')) || existsSync(join(out, 'agent.err')));
      const named = readFileSync(join(out, 'command.txt'), 'utf8');
      if (WIN) {
        // Windows: the prompt says plain `retake`, with retake's own folder first on the agent's PATH (spec/setup.md).
        check(named === 'retake', 'the prompt says plain retake', `it named: ${named}`);
        const first = readFileSync(join(out, 'path0.txt'), 'utf8');
        check(resolve(first).toLowerCase() === dirname(bin('retake')).toLowerCase(),
          `the agent's PATH starts with ${PLATFORM}'s bin folder`, `it starts with: ${first}`);
      } else {
        check(resolve(named) === bin('retake'), `the prompt names ${PLATFORM}'s Go binary`, `it named: ${named}`);
      }
      check(!existsSync(join(out, 'agent.err')), "`retake working` and `retake done` ran with nothing on PATH",
        existsSync(join(out, 'agent.err')) ? readFileSync(join(out, 'agent.err'), 'utf8') : '');
      await waitFor('the comment marked done', async () => {
        const a = await json(`${open.url}api/automation`);
        return a.tasks?.some((t) => t.marked?.some((m) => m.note === note.id) || t.state === 'completed');
      }, 30_000);
      log('ok - the server saw the agent mark it');
    } finally {
      stopTree(open.child);
      await open.child.exited;
    }

    // 5. npx: one download, then none; @latest picks up a newer release. A cache and a prefix of its own: npx would
    //    run the global install from step 1 instead.
    env.npm_config_cache = join(S, 'npx-cache');
    env.npm_config_prefix = join(S, 'npx-prefix');
    mkdirSync(join(env.npm_config_prefix, 'lib'), { recursive: true }); // npm 10 (Node 18) lstats it, and stops if missing
    env.PATH = env.PATH.split(delimiter).slice(1).join(delimiter);
    downloads.length = 0;
    t = Date.now();
    r = await run('npx', ['--yes', NAME, '--version'], { env, cwd: join(S, 'empty') });
    check(r.code === 0 && r.out.startsWith(`retake ${V}`), `npx ${NAME} --version`, r.out + r.err);
    results.npxFirstSeconds = (Date.now() - t) / 1000;
    check(downloads.length === 2, `the first npx downloaded ${NAME} and ${PLATFORM}`, downloads.join(', '));
    t = Date.now();
    r = await run('npx', ['--yes', NAME, '--version'], { env, cwd: join(S, 'empty') });
    check(r.code === 0 && downloads.length === 2, 'a second npx downloads nothing', downloads.join(', '));
    results.npxSecondSeconds = (Date.now() - t) / 1000;

    const next = await newerRelease(join(TARBALLS, `${NAME}-${V}.tgz`), { ...env, npm_config_cache: join(S, 'pack-cache') });
    r = await run('npx', ['--yes', NAME, '--version'], { env, cwd: join(S, 'empty') });
    results.npxPlainAfterRelease = npxVersion(env.npm_config_cache, NAME, NAME);
    log(`plain npx after ${next} came out runs ${results.npxPlainAfterRelease}`);
    r = await run('npx', ['--yes', `${NAME}@latest`, '--version'], { env, cwd: join(S, 'empty') });
    // npm 11 looks up @latest each time; npm 10 (Node 18) runs the copy it has. Both are fine for Retake.
    const npmMajor = Number((await run('npm', ['--version'], { env })).out.trim().split('.')[0]);
    if (npmMajor >= 11) {
      check(r.code === 0 && npxVersion(env.npm_config_cache, NAME, `${NAME}@latest`) === next, `npx ${NAME}@latest runs ${next}`, r.out + r.err);
    } else {
      check(r.code === 0, `npx ${NAME}@latest runs (npm ${npmMajor} keeps the copy it has: ${r.out.trim()})`, r.out + r.err);
    }
    check(downloads.filter((f) => f === platformFile).length === 1, `and npx downloaded ${PLATFORM} only once in all`, downloads.join(', '));
    results.downloadsDuringNpx = [...downloads];
    results.packumentRequests = packuments;
  } finally {
    for (const c of children) stopTree(c);
    server.close();
  }
  log('PASS', JSON.stringify(results));
  if (process.env.RETAKE_SMOKE_KEEP !== '1') rmSync(S, { recursive: true, force: true });
}

/** The version of NAME that npx installed for the command line `spec` (npm keeps one folder per list of specs). */
function npxVersion(cache, name, spec) {
  const root = join(cache, '_npx');
  for (const d of existsSync(root) ? readdirSync(root) : []) {
    try {
      const p = JSON.parse(readFileSync(join(root, d, 'package.json'), 'utf8'));
      if (JSON.stringify(p._npx?.packages) === JSON.stringify([spec])) {
        return JSON.parse(readFileSync(join(root, d, 'node_modules', name, 'package.json'), 'utf8')).version;
      }
    } catch {
      // not an npx folder
    }
  }
  return '';
}

/** A newer release of the command alone (same platform packages): the main tarball with its patch version bumped. */
async function newerRelease(file, env) {
  const dir = join(S, 'next');
  mkdirSync(dir, { recursive: true });
  // From the tarball's folder, by its bare name: GNU tar (Git's, on Windows) reads "D:/…" as a remote host.
  let r = await run('tar', ['-xzf', basename(file), '-C', dir], { env, cwd: dirname(file) });
  if (r.code !== 0) throw new Error(`untar ${file}: ${r.err}`);
  const pj = join(dir, 'package', 'package.json');
  const p = JSON.parse(readFileSync(pj, 'utf8'));
  const [a, b, c] = p.version.split(/[.+-]/).map(Number);
  p.version = `${a}.${b}.${c + 1}-smoke`;
  writeFileSync(pj, JSON.stringify(p, null, 2));
  r = await run('npm', ['pack', join(dir, 'package'), '--pack-destination', EXTRA, '--silent'], { env });
  if (r.code !== 0) throw new Error(`npm pack: ${r.err}`);
  log(`published ${p.name} ${p.version} to the test registry`);
  return p.version;
}

/** The fake agent: records the command its prompt names for `working`, then runs it with nothing on PATH, as an agent
 * in a herdr or tmux pane without node would. On Windows the prompt says plain `retake` and Retake puts its own folder
 * first on the agent's PATH, so there that folder is all the PATH it keeps. */
const FAKE_AGENT = `import { copyFileSync, readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
const out = process.env.SMOKE_OUT;
try {
  const prompt = readFileSync(0, 'utf8');
  const ctx = JSON.parse(readFileSync(process.env.RETAKE_CONTEXT, 'utf8'));
  const id = String(ctx.task.notes[0]);
  const m = prompt.match(/^ {2}(.+?) working <note id> "/m);
  if (!m) throw new Error('no "working" line in the prompt:\\n' + prompt);
  let cmd = m[1];
  if (cmd.startsWith("'") && cmd.endsWith("'")) cmd = cmd.slice(1, -1).split("'\\\\''").join("'");
  writeFileSync(out + '/command.txt', cmd);
  const win = process.platform === 'win32';
  const first = (process.env.PATH || '').split(win ? ';' : ':')[0];
  writeFileSync(out + '/path0.txt', first);
  const env = win
    ? { SystemRoot: process.env.SystemRoot, TEMP: process.env.TEMP, USERPROFILE: process.env.USERPROFILE, PATH: cmd === 'retake' ? first : 'C:\\\\nonexistent' }
    : { HOME: process.env.HOME, PATH: '/nonexistent' };
  execFileSync(cmd, ['working', id, 'checking the npm install'], { env, stdio: ['ignore', 'pipe', 'pipe'] });
  copyFileSync(process.env.SMOKE_VIDEO, process.env.RETAKE_OUTPUT); // the "edit": the video as it was
  execFileSync(cmd, ['done', id, 'Nothing to change: a smoke test'], { env, stdio: ['ignore', 'pipe', 'pipe'] });
  writeFileSync(process.env.RETAKE_REPORT, JSON.stringify({ state: 'completed', message: 'A smoke test: nothing changed' }));
  writeFileSync(out + '/agent.done', 'ok');
} catch (e) {
  writeFileSync(out + '/agent.err', String(e && e.stack || e) + '\\n' + (e.stderr || ''));
  process.exit(1);
}
`;

main().catch((e) => {
  for (const c of children) stopTree(c);
  console.error('npm-smoke:', e.message);
  console.error(`npm-smoke: scratch kept in ${S}`);
  process.exit(1);
});
