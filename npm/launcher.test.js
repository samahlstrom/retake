// The npm launcher (npm/retake-cli/bin/retake.js): which binary it picks, what it says when it can't, the Windows
// download (from a local stand-in for GitHub Releases), and that exit codes and signals pass through.
// `node --test npm/` (make test runs it). The whole install, with a registry, is scripts/npm-smoke.mjs.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const zlib = require('node:zlib');
const { packageFor, locate, run, cacheRoot, fetchBuild, unzip } = require('./retake-cli/bin/retake.js');

const ZIP = 'retake_1.0.0_windows_amd64.zip';
const pkg = {
  name: 'retake-cli',
  version: '1.0.0',
  optionalDependencies: {
    'retake-cli-linux-x64': '1.0.0',
    'retake-cli-darwin-arm64': '1.0.0',
  },
  archives: {
    'win32-x64': {
      url: `https://github.com/samahlstrom/retake/releases/download/v1.0.0/${ZIP}`,
      sha256: 'c'.repeat(64),
      size: 113_000_000,
    },
  },
};
const posix = process.platform !== 'win32';

/** A platform package in a temp node_modules: bin/<binary> with mode, or none. */
function platformPackage(t, name, { binary = 'retake', mode = 0o755, withBin = true } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'retake-launcher-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const root = path.join(dir, 'node_modules', name);
  fs.mkdirSync(path.join(root, 'bin'), { recursive: true });
  fs.writeFileSync(path.join(root, 'package.json'), JSON.stringify({ name }));
  if (withBin) fs.writeFileSync(path.join(root, 'bin', binary), '#!/bin/sh\nexit 0\n', { mode });
  return {
    root,
    resolve: (id) => {
      if (id !== `${name}/package.json`) throw Object.assign(new Error(`Cannot find module '${id}'`), { code: 'MODULE_NOT_FOUND' });
      return path.join(root, 'package.json');
    },
  };
}

test('the platform package is <name>-<os>-<cpu>, from the package.json name', () => {
  assert.equal(packageFor('linux', 'x64'), 'retake-cli-linux-x64');
  assert.equal(packageFor('darwin', 'arm64', 'other-name'), 'other-name-darwin-arm64');
});

test('finds the Go binary in the platform package', (t) => {
  const p = platformPackage(t, 'retake-cli-linux-x64');
  assert.equal(locate({ platform: 'linux', arch: 'x64', pkg, resolve: p.resolve }), path.join(p.root, 'bin', 'retake'));
});

test('Windows has no npm package: the release archive is downloaded instead', () => {
  const d = locate({ platform: 'win32', arch: 'x64', pkg, resolve: () => assert.fail('no package to look up') });
  assert.deepEqual(d, { archive: pkg.archives['win32-x64'], version: '1.0.0', platform: 'win32' });
});

test('downloads go in %LOCALAPPDATA%\\retake\\npm on Windows, $XDG_CACHE_HOME/retake/npm elsewhere', () => {
  assert.equal(cacheRoot({ platform: 'win32', env: { LOCALAPPDATA: '/L' }, home: '/h' }), path.join('/L', 'retake', 'npm'));
  assert.equal(cacheRoot({ platform: 'win32', env: {}, home: '/h' }), path.join('/h', 'AppData', 'Local', 'retake', 'npm'));
  assert.equal(cacheRoot({ platform: 'linux', env: { XDG_CACHE_HOME: '/c' }, home: '/h' }), path.join('/c', 'retake', 'npm'));
  assert.equal(cacheRoot({ platform: 'darwin', env: {}, home: '/h' }), path.join('/h', '.cache', 'retake', 'npm'));
});

test('an unsupported platform says which builds there are and where else to look', () => {
  const err = locate({ platform: 'freebsd', arch: 'x64', pkg, resolve: () => assert.fail('not looked up') });
  assert.ok(err instanceof Error);
  assert.match(err.message, /no build for freebsd on x64/);
  assert.match(err.message, /Linux x64, macOS arm64, Windows x64/);
  assert.match(err.message, /github\.com\/samahlstrom\/retake#install/);
});

test('a missing optional dependency says how to install it again', () => {
  const err = locate({ platform: 'linux', arch: 'x64', pkg, resolve: () => { throw new Error('nope'); }, glibc: () => true });
  assert.match(err.message, /retake-cli-linux-x64\) isn't installed/);
  assert.match(err.message, /--omit=optional, --no-optional/);
  assert.match(err.message, /npm i -g retake-cli --include=optional/);
});

test('musl Linux (Alpine) is named as the reason', () => {
  const err = locate({ platform: 'linux', arch: 'x64', pkg, resolve: () => { throw new Error('nope'); }, glibc: () => false });
  assert.match(err.message, /needs glibc/);
  assert.match(err.message, /musl \(Alpine\?\)/);
});

test('a platform package without its binary says to install again', (t) => {
  const p = platformPackage(t, 'retake-cli-darwin-arm64', { withBin: false });
  const err = locate({ platform: 'darwin', arch: 'arm64', pkg, resolve: p.resolve });
  assert.match(err.message, /has no retake/);
});

test('a binary that lost its executable bit gets it back', { skip: !posix }, (t) => {
  const p = platformPackage(t, 'retake-cli-linux-x64', { mode: 0o644 });
  const bin = locate({ platform: 'linux', arch: 'x64', pkg, resolve: p.resolve });
  assert.equal(typeof bin, 'string');
  assert.equal(fs.statSync(bin).mode & 0o111, 0o111);
});

// ---------------------------------------------------------------------------------------------------------------
// The download.

/** A zip of files ({name: content}; a name ending in / is a folder), the way goreleaser writes one: deflated
 * entries, Unix modes. The launcher doesn't read CRCs (the whole file's SHA-256 is checked), so they're 0. */
function zip(files) {
  const locals = [];
  const central = [];
  let offset = 0;
  for (const [name, content] of Object.entries(files)) {
    const data = Buffer.from(content);
    const method = data.length ? 8 : 0;
    const body = method ? zlib.deflateRawSync(data) : data;
    const n = Buffer.from(name);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(method, 8);
    local.writeUInt32LE(body.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(n.length, 26);
    const c = Buffer.alloc(46);
    c.writeUInt32LE(0x02014b50, 0);
    c.writeUInt16LE((3 << 8) | 20, 4); // made on Unix
    c.writeUInt16LE(20, 6);
    c.writeUInt16LE(method, 10);
    c.writeUInt32LE(body.length, 20);
    c.writeUInt32LE(data.length, 24);
    c.writeUInt16LE(n.length, 28);
    c.writeUInt32LE((name.endsWith('/') ? 0o40755 : 0o100755) * 0x10000, 38);
    c.writeUInt32LE(offset, 42);
    locals.push(local, n, body);
    central.push(c, n);
    offset += local.length + n.length + body.length;
  }
  const cd = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(Object.keys(files).length, 8);
  end.writeUInt16LE(Object.keys(files).length, 10);
  end.writeUInt32LE(cd.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, cd, end]);
}

const sha256 = (b) => crypto.createHash('sha256').update(b).digest('hex');
const ARCHIVE = zip({
  'retake.exe': 'MZ the Go binary',
  'retake-bundle.json': '{"bundle": 1}',
  'ffmpeg.exe': 'MZ'.repeat(50_000),
  'conpty/': '',
  'conpty/conpty.dll': 'MZ conpty',
  'licenses/ffmpeg-GPLv3.txt': 'GPL',
  'empty.txt': '',
});

function tmpdir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'retake-launcher-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

/** GitHub Releases on 127.0.0.1: /download/<file> redirects to /blob/<file>, as github.com does to its CDN. */
async function releases(t, files) {
  const hits = [];
  const server = http.createServer((req, res) => {
    hits.push(req.url);
    const [, kind, file] = req.url.match(/^\/(download|blob)\/(.+)$/) || [];
    if (kind === 'download' && files[file]) {
      res.writeHead(302, { Location: `/blob/${file}` });
      res.end();
    } else if (kind === 'blob' && files[file]) {
      res.writeHead(200, { 'Content-Length': files[file].length });
      res.end(files[file]);
    } else {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  t.after(() => server.close());
  return { base: `http://127.0.0.1:${server.address().port}/download`, hits };
}

/** What locate() hands fetchBuild for Windows x64, with this archive's real SHA-256 and size. */
const windows = (archive = ARCHIVE, over = {}) => ({
  archive: { ...pkg.archives['win32-x64'], sha256: sha256(archive), size: archive.length, ...over },
  version: '1.0.0',
  platform: 'win32',
});

function stderr() {
  const lines = [];
  return { lines, write: (s) => lines.push(s) };
}

test('Windows: the first run downloads the archive, checks its SHA-256, unpacks it; later runs use it', async (t) => {
  const gh = await releases(t, { [ZIP]: ARCHIVE });
  const root = tmpdir(t);
  const err = stderr();
  const bin = await fetchBuild(windows(), { root, base: gh.base, stderr: err });
  assert.equal(bin, path.join(root, '1.0.0', 'retake.exe'));
  assert.equal(fs.readFileSync(bin, 'utf8'), 'MZ the Go binary');
  assert.equal(fs.readFileSync(path.join(root, '1.0.0', 'ffmpeg.exe'), 'utf8'), 'MZ'.repeat(50_000));
  assert.equal(fs.readFileSync(path.join(root, '1.0.0', 'conpty', 'conpty.dll'), 'utf8'), 'MZ conpty');
  assert.ok(fs.existsSync(path.join(root, '1.0.0', 'retake-bundle.json')), "retake-bundle.json next to retake.exe");
  assert.equal(fs.statSync(path.join(root, '1.0.0', 'empty.txt')).size, 0);
  assert.deepEqual(fs.readdirSync(root), ['1.0.0'], 'no temporary folder left');
  assert.deepEqual(gh.hits, [`/download/${ZIP}`, `/blob/${ZIP}`], 'followed the redirect');
  assert.deepEqual(err.lines, [`Getting Retake 1.0.0 for Windows (${Math.round(ARCHIVE.length / 1e3)} kB)…\n`]);

  assert.equal(await fetchBuild(windows(), { root, base: gh.base, stderr: err }), bin);
  assert.equal(gh.hits.length, 2, 'the second run downloads nothing');
  assert.equal(err.lines.length, 1, 'and says nothing');
});

test('Windows: a download whose SHA-256 is wrong is refused, and nothing is kept', async (t) => {
  const gh = await releases(t, { [ZIP]: ARCHIVE });
  const root = tmpdir(t);
  const r = await fetchBuild(windows(ARCHIVE, { sha256: '0'.repeat(64) }), { root, base: gh.base, stderr: stderr() });
  assert.ok(r instanceof Error);
  assert.match(r.message, /download of Retake 1\.0\.0 for Windows doesn't match its SHA-256, so it wasn't used/);
  assert.match(r.message, new RegExp(`expected 0{64}\n  got      ${sha256(ARCHIVE)}`));
  assert.deepEqual(fs.readdirSync(root), []);
});

test('Windows: two launches at once both run the one folder', async (t) => {
  const gh = await releases(t, { [ZIP]: ARCHIVE });
  const root = tmpdir(t);
  const opts = { root, base: gh.base, stderr: stderr() };
  const [a, b] = await Promise.all([fetchBuild(windows(), opts), fetchBuild(windows(), opts)]);
  assert.equal(a, path.join(root, '1.0.0', 'retake.exe'));
  assert.equal(b, a);
  assert.deepEqual(fs.readdirSync(root), ['1.0.0']);
});

test('Windows: no network, or no such release, says so', async (t) => {
  const gh = await releases(t, {});
  const root = tmpdir(t);
  const missing = await fetchBuild(windows(), { root, base: gh.base, stderr: stderr() });
  assert.match(missing.message, new RegExp(`Couldn't download Retake 1\\.0\\.0 for Windows from ${gh.base}/${ZIP}: HTTP 404`));
  // A port nothing listens on.
  const port = await new Promise((r) => {
    const s = http.createServer().listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => r(port));
    });
  });
  const offline = await fetchBuild(windows(), { root, base: `http://127.0.0.1:${port}`, stderr: stderr() });
  assert.match(offline.message, /Couldn't download Retake 1\.0\.0 for Windows .*ECONNREFUSED\. Check the internet connection/);
  assert.match(offline.message, /github\.com\/samahlstrom\/retake#install/);
  assert.deepEqual(fs.readdirSync(root), []);
});

test('Windows: a version folder broken by hand is downloaded again; old versions are cleared, the newest kept', async (t) => {
  const gh = await releases(t, { [ZIP]: ARCHIVE });
  const root = tmpdir(t);
  for (const [i, v] of ['0.7.0', '0.8.0', '0.9.0'].entries()) {
    fs.mkdirSync(path.join(root, v));
    fs.writeFileSync(path.join(root, v, 'retake.exe'), v);
    const when = new Date(Date.now() - (10 - i) * 60_000);
    fs.utimesSync(path.join(root, v), when, when);
  }
  fs.mkdirSync(path.join(root, '1.0.0')); // no retake.exe
  const bin = await fetchBuild(windows(), { root, base: gh.base, stderr: stderr() });
  assert.equal(fs.readFileSync(bin, 'utf8'), 'MZ the Go binary');
  assert.deepEqual(fs.readdirSync(root).sort(), ['0.8.0', '0.9.0', '1.0.0']);
});

test('unzip refuses a path out of its folder', async (t) => {
  const dir = tmpdir(t);
  const file = path.join(dir, 'evil.zip');
  fs.writeFileSync(file, zip({ '../evil.exe': 'MZ' }));
  await assert.rejects(unzip(file, path.join(dir, 'out')), /a bad path: \.\.\/evil\.exe/);
  assert.ok(!fs.existsSync(path.join(dir, 'evil.exe')));
});

/** A shell script standing in for the Go binary. */
function script(t, body) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'retake-launcher-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const f = path.join(dir, 'retake');
  fs.writeFileSync(f, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
  return f;
}

test('the exit code passes through', { skip: !posix }, async (t) => {
  assert.deepEqual(await run(script(t, 'exit 3'), []), { code: 3 });
  assert.deepEqual(await run(script(t, '[ "$1" = "open" ] && [ "$2" = "a b" ] && exit 0; exit 9'), ['open', 'a b']), { code: 0 });
});

test('a signal to the launcher reaches retake, and its exit comes back', { skip: !posix }, async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'retake-launcher-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const ready = path.join(dir, 'ready');
  // Exits 42 on SIGINT, like retake stopping cleanly on Ctrl+C.
  const bin = script(t, `trap 'exit 42' INT TERM\ntouch '${ready}'\nwhile :; do sleep 0.05; done`);
  const done = run(bin, []);
  while (!fs.existsSync(ready)) await new Promise((r) => setTimeout(r, 20));
  process.emit('SIGINT'); // what Node does when this process gets SIGINT
  assert.deepEqual(await done, { code: 42 });
  assert.equal(process.listenerCount('SIGINT'), 0, 'the handlers are gone after retake exits');
});

test('retake ended by a signal reports the signal, so the launcher can end the same way', { skip: !posix }, async (t) => {
  assert.deepEqual(await run(script(t, 'kill -TERM $$'), []), { signal: 'SIGTERM' });
});

test('a binary that cannot start is an error, not a hang', async () => {
  const r = await run(path.join(os.tmpdir(), 'no-such-retake-binary'), []);
  assert.equal(r.code, 1);
  assert.ok(r.error);
});
