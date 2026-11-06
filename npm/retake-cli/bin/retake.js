#!/usr/bin/env node
// The `retake` command of the npm package. Retake itself is a Go program. This script finds the build for this
// computer and runs it with the same arguments, the same terminal, and passes signals in and the exit code out:
//
//   macOS, Linux   npm installs it as an optional dependency, <this package's name>-<os>-<cpu> (only the one
//                  matching this OS and CPU).
//   Windows        there's no such package (npm's spam filter refuses new package names, docs/releasing.md). The
//                  first run downloads this version's release archive from GitHub, checks it against the SHA-256
//                  written into this package's package.json ("archives") when it was made, and unpacks it into
//                  %LOCALAPPDATA%\retake\npm\<version> ($XDG_CACHE_HOME/retake/npm/<version> elsewhere). Every later
//                  run starts it from there. Any OS and CPU listed in "archives" works this way.
//
// Plain `retake` in a terminal starts Retake (`retake start`: the setup page the first time, then your videos).
// Agents never come through here: Retake gives them the Go binary itself (spec/setup.md, by its full path, or on
// Windows its folder first on their PATH), so no node starts on each `retake ask`.
'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const manifest = require('../package.json');

const NAMES = { darwin: 'macOS', linux: 'Linux', win32: 'Windows' };
const INSTALL = 'https://github.com/samahlstrom/retake#install';
const KEEP = 3; // downloaded versions kept (a global install and an npx of another version don't download in turns)

/** The platform package's name for this OS and CPU, e.g. retake-cli-linux-x64. */
function packageFor(platform, arch, name = manifest.name) {
  return `${name}-${platform}-${arch}`;
}

/**
 * Where the Go binary is: its path; {archive, version, platform}, a release archive to download first (fetchBuild);
 * or an Error that says plainly what's wrong and what to do.
 */
function locate({
  platform = process.platform,
  arch = process.arch,
  pkg = manifest,
  resolve = (id) => require.resolve(id),
  glibc = hasGlibc,
} = {}) {
  const want = packageFor(platform, arch, pkg.name);
  const known = Object.keys(pkg.optionalDependencies || {});
  const archives = pkg.archives || {};
  if (!known.includes(want)) {
    const archive = archives[`${platform}-${arch}`];
    if (archive) return { archive, version: pkg.version, platform };
    const builds = [...known.map((n) => n.slice(pkg.name.length + 1)), ...Object.keys(archives)].map((p) => {
      const [os, cpu] = p.split('-');
      return `${NAMES[os] || os} ${cpu}`;
    });
    return new Error(
      `Retake has no build for ${NAMES[platform] || platform} on ${arch} yet` +
        (builds.length ? ` (there are builds for ${builds.join(', ')}).` : '.') +
        `\nOther ways to install it: ${INSTALL}`,
    );
  }
  let dir;
  try {
    dir = path.dirname(resolve(`${want}/package.json`));
  } catch {
    if (platform === 'linux' && !glibc()) {
      return new Error(
        `Retake's Linux build needs glibc (Debian, Ubuntu, Fedora, Arch and most others); this Linux uses musl (Alpine?).` +
          `\nOther ways to install it: ${INSTALL}`,
      );
    }
    return new Error(
      `The ${NAMES[platform] || platform} ${arch} part of Retake (${want}) isn't installed.\n` +
        `npm leaves it out when optional dependencies are turned off (--omit=optional, --no-optional, or optional=false\n` +
        `in an .npmrc). Install again with them:\n` +
        `  npm i -g ${pkg.name} --include=optional`,
    );
  }
  const bin = path.join(dir, 'bin', platform === 'win32' ? 'retake.exe' : 'retake');
  if (!fs.existsSync(bin)) {
    return new Error(`${want} is installed but has no ${path.basename(bin)} (${bin}). Install ${pkg.name} again.`);
  }
  if (platform !== 'win32') {
    try {
      fs.accessSync(bin, fs.constants.X_OK);
    } catch {
      try {
        fs.chmodSync(bin, 0o755); // a package manager that dropped the mode
      } catch {
        return new Error(`${bin} can't be run (it isn't executable). Install ${pkg.name} again.`);
      }
    }
  }
  return bin;
}

/** Whether this Linux uses glibc (Node reports its version), not musl. */
function hasGlibc() {
  try {
    return Boolean(process.report.getReport().header.glibcVersionRuntime);
  } catch {
    return true; // can't tell: assume the common case
  }
}

// ---------------------------------------------------------------------------------------------------------------
// The platforms without an npm package: the release archive, downloaded once per version.

/** The folder downloaded versions go in: %LOCALAPPDATA%\retake\npm on Windows, $XDG_CACHE_HOME/retake/npm elsewhere. */
function cacheRoot({ platform = process.platform, env = process.env, home = os.homedir() } = {}) {
  const base =
    platform === 'win32'
      ? env.LOCALAPPDATA || path.join(home, 'AppData', 'Local')
      : (env.XDG_CACHE_HOME && path.isAbsolute(env.XDG_CACHE_HOME) && env.XDG_CACHE_HOME) || path.join(home, '.cache');
  return path.join(base, 'retake', 'npm');
}

/**
 * The Go binary of a release archive locate() asked for: <root>/<version>/retake(.exe) once it's there; else
 * downloads the archive (one line on stderr), refuses it unless its SHA-256 is the one in package.json, unpacks it
 * into a temporary folder in root and renames that into place. Two launches racing both get the one folder: the
 * second rename finds the first one's. Resolves to the path, or an Error that says what went wrong.
 *
 * base (or $RETAKE_NPM_BASE_URL) is another place for the release's files, such as the smoke test's stand-in for
 * GitHub; the SHA-256 is checked all the same.
 */
async function fetchBuild(
  { archive, version, platform },
  { root = cacheRoot({ platform }), base = process.env.RETAKE_NPM_BASE_URL, stderr = process.stderr } = {},
) {
  const exe = platform === 'win32' ? 'retake.exe' : 'retake';
  const dir = path.join(root, version);
  const bin = path.join(dir, exe);
  if (fs.existsSync(bin)) return bin;
  const label = `Retake ${version} for ${NAMES[platform] || platform}`;
  const file = archive.url.slice(archive.url.lastIndexOf('/') + 1);
  const url = base ? `${base.replace(/\/+$/, '')}/${file}` : archive.url;
  let tmp;
  try {
    fs.mkdirSync(root, { recursive: true });
    // Renamed into place whole, so a folder there without the binary was broken by hand: replace it.
    if (fs.existsSync(dir) && !fs.existsSync(bin)) discard(dir, root);
    tmp = fs.mkdtempSync(path.join(root, `.${version}-`));
  } catch (e) {
    return new Error(`Couldn't make a folder for ${label} in ${root}: ${e.message}`);
  }
  const line = progress(stderr, `Getting ${label}${archive.size ? ` (${size(archive.size)})` : ''}…`, archive.size);
  try {
    const zip = path.join(tmp, file);
    let got;
    try {
      got = await download(url, zip, line.update);
    } catch (e) {
      const why = e.status ? `HTTP ${e.status}` : e.code || e.message;
      throw new Error(
        `Couldn't download ${label} from ${url}: ${why}.` +
          (e.status ? '' : ' Check the internet connection, then run retake again.') +
          `\nOther ways to install it: ${INSTALL}`,
      );
    }
    if (got !== archive.sha256) {
      throw new Error(
        `The download of ${label} doesn't match its SHA-256, so it wasn't used.\n` +
          `  ${url}\n  expected ${archive.sha256}\n  got      ${got}\n` +
          `Run retake again. If it happens again, something between this computer and GitHub changes the file; ` +
          `install it another way: ${INSTALL}`,
      );
    }
    const files = path.join(tmp, 'files');
    try {
      await unzip(zip, files);
    } catch (e) {
      throw new Error(`Couldn't unpack ${label} (${file}) in ${root}: ${e.message}`);
    }
    if (!fs.existsSync(path.join(files, exe))) throw new Error(`${file} has no ${exe}. Install ${manifest.name} again.`);
    await settle(files, dir, bin);
    line.end();
    prune(root, version);
    return bin;
  } catch (e) {
    line.end();
    return e instanceof Error ? e : new Error(String(e));
  } finally {
    try {
      fs.rmSync(tmp, { recursive: true, force: true });
    } catch {
      // a scanner still has a file open: prune() removes it on a later run
    }
  }
}

const size = (n) => (n >= 1e6 ? `${Math.round(n / 1e6)} MB` : `${Math.max(1, Math.round(n / 1e3))} kB`);

/** One line on stderr; in a terminal it ends with a percentage that counts up. */
function progress(stream, text, total) {
  const tty = Boolean(stream.isTTY) && total > 0;
  stream.write(tty ? text : `${text}\n`);
  let shown = -1;
  let open = tty;
  return {
    update(n) {
      const pct = Math.floor((100 * n) / total);
      if (!open || pct === shown) return;
      shown = pct;
      stream.write(`\r${text} ${pct}%`);
    },
    end() {
      if (open) stream.write('\n');
      open = false;
    },
  };
}

/** Downloads url (following redirects, as GitHub's release downloads do) into file; resolves to its SHA-256 (hex). */
async function download(url, file, onBytes = () => {}) {
  const crypto = require('node:crypto');
  const { Transform } = require('node:stream');
  const { pipeline } = require('node:stream/promises');
  const res = await get(url);
  const hash = crypto.createHash('sha256');
  let n = 0;
  const count = new Transform({
    transform(chunk, _enc, done) {
      hash.update(chunk);
      n += chunk.length;
      onBytes(n);
      done(null, chunk);
    },
  });
  await pipeline(res, count, fs.createWriteStream(file));
  return hash.digest('hex');
}

/** GET url: the response once it's 200, after up to 10 redirects; else an Error with .status or .code. */
function get(url, redirects = 10) {
  return new Promise((resolve, reject) => {
    const u = new URL(url);
    if (u.protocol !== 'https:' && u.protocol !== 'http:') return reject(new Error(`can't download ${u.protocol} URLs`));
    const mod = require(u.protocol === 'https:' ? 'node:https' : 'node:http');
    const req = mod.get(u, { headers: { 'User-Agent': `${manifest.name}/${manifest.version} node/${process.version}` } }, (res) => {
      const status = res.statusCode;
      if (status >= 300 && status < 400 && res.headers.location) {
        res.resume();
        if (redirects <= 0) return reject(new Error('too many redirects'));
        return resolve(get(new URL(res.headers.location, u).href, redirects - 1));
      }
      if (status !== 200) {
        res.resume();
        return reject(Object.assign(new Error(`HTTP ${status}`), { status: `${status} ${res.statusMessage || ''}`.trim() }));
      }
      resolve(res);
    });
    // Nothing for 30 s, before or during the body: give up rather than hang.
    req.setTimeout(30_000, () => req.destroy(Object.assign(new Error('no answer for 30 s'), { code: 'ETIMEDOUT' })));
    req.on('error', reject);
  });
}

/**
 * Unpacks a zip file into dest with Node alone: its central directory, then each entry through zlib's raw inflate.
 *
 * Why not Windows' own %SystemRoot%\System32\tar.exe (bsdtar, which reads zips and drive paths)? This is more
 * reliable: tar.exe is only on Windows 10 1803 and later (not Server 2016), app-control policies can block it, and
 * this code runs the same on every OS, so `node --test npm/` checks on Linux exactly what Windows runs. It reads what
 * goreleaser writes, the only zips it's given (their SHA-256 is checked first): stored or deflated entries, no zip64,
 * no encryption, no links, and no path that leaves dest.
 */
async function unzip(file, dest) {
  const zlib = require('node:zlib');
  const { pipeline } = require('node:stream/promises');
  const fd = fs.openSync(file, 'r');
  try {
    const read = (length, position) => {
      const b = Buffer.alloc(length);
      if (length && fs.readSync(fd, b, 0, length, position) !== length) throw new Error('the zip file is cut short');
      return b;
    };
    const total = fs.fstatSync(fd).size;
    const tail = read(Math.min(total, 22 + 0xffff), Math.max(0, total - 22 - 0xffff));
    let end = tail.length - 22;
    while (end >= 0 && tail.readUInt32LE(end) !== 0x06054b50) end--;
    if (end < 0) throw new Error('not a zip file');
    const count = tail.readUInt16LE(end + 10);
    const cdOffset = tail.readUInt32LE(end + 16);
    if (count === 0xffff || cdOffset === 0xffffffff) throw new Error('a zip64 file');
    const cd = read(tail.readUInt32LE(end + 12), cdOffset);
    const root = path.resolve(dest);
    fs.mkdirSync(root, { recursive: true });
    for (let p = 0, i = 0; i < count; i++) {
      if (cd.readUInt32LE(p) !== 0x02014b50) throw new Error('a broken zip directory');
      const madeBy = cd.readUInt16LE(p + 4) >> 8;
      const flags = cd.readUInt16LE(p + 8);
      const method = cd.readUInt16LE(p + 10);
      const packed = cd.readUInt32LE(p + 20);
      const unpacked = cd.readUInt32LE(p + 24);
      const nameLength = cd.readUInt16LE(p + 28);
      const attrs = cd.readUInt32LE(p + 38);
      const at = cd.readUInt32LE(p + 42);
      const name = cd.toString('utf8', p + 46, p + 46 + nameLength);
      p += 46 + nameLength + cd.readUInt16LE(p + 30) + cd.readUInt16LE(p + 32);

      const parts = name.split(/[\\/]+/).filter(Boolean);
      if (/^([\\/]|[a-zA-Z]:)/.test(name) || parts.includes('..') || !parts.length) throw new Error(`a bad path: ${name}`);
      const to = path.join(root, ...parts);
      if (/[\\/]$/.test(name)) {
        fs.mkdirSync(to, { recursive: true });
        continue;
      }
      const mode = madeBy === 3 ? attrs >>> 16 : 0; // Unix permissions, when the zip was made on Unix
      if ((mode & 0o170000) === 0o120000) throw new Error(`a link: ${name}`);
      if (flags & 1) throw new Error(`an encrypted entry: ${name}`);
      if ([packed, unpacked, at].includes(0xffffffff)) throw new Error(`a zip64 entry: ${name}`);
      if (method !== 0 && method !== 8) throw new Error(`${name} is compressed with method ${method}`);
      const local = read(30, at);
      if (local.readUInt32LE(0) !== 0x04034b50) throw new Error(`a broken entry: ${name}`);
      const start = at + 30 + local.readUInt16LE(26) + local.readUInt16LE(28);

      fs.mkdirSync(path.dirname(to), { recursive: true });
      if (packed === 0) {
        fs.writeFileSync(to, '');
      } else {
        const stages = [fs.createReadStream(file, { start, end: start + packed - 1 })];
        if (method === 8) stages.push(zlib.createInflateRaw());
        await pipeline(...stages, fs.createWriteStream(to));
      }
      const wrote = fs.statSync(to).size;
      if (wrote !== unpacked) throw new Error(`${name} unpacked to ${wrote} bytes, not ${unpacked}`);
      if (mode & 0o111) fs.chmodSync(to, mode & 0o777);
    }
  } finally {
    fs.closeSync(fd);
  }
}

/** Renames the unpacked folder to dir. Another launch got there first: its folder is used. */
async function settle(from, dir, bin) {
  for (let attempt = 1; ; attempt++) {
    try {
      fs.renameSync(from, dir);
      return;
    } catch (e) {
      if (fs.existsSync(bin)) return;
      // Windows: a virus scanner still reading the new programs makes the rename fail for a moment.
      if (attempt >= 30 || !['EPERM', 'EACCES', 'EBUSY'].includes(e.code)) {
        throw new Error(`Couldn't put Retake in ${dir}: ${e.message}`);
      }
      await new Promise((r) => setTimeout(r, Math.min(100 * attempt, 1000)));
    }
  }
}

/** Moves a folder aside (one with a program running in it can't be, on Windows: it stays), then deletes it. */
function discard(dir, root) {
  const trash = path.join(root, `.trash-${path.basename(dir)}-${process.pid}-${Date.now()}`);
  try {
    fs.renameSync(dir, trash);
  } catch {
    return;
  }
  try {
    fs.rmSync(trash, { recursive: true, force: true });
  } catch {
    // removed on a later run
  }
}

/** Keeps the KEEP newest versions in root (current always) and drops leftovers of runs that stopped half way. */
function prune(root, current) {
  try {
    const now = Date.now();
    const entries = fs
      .readdirSync(root, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => ({ name: e.name, p: path.join(root, e.name), t: fs.statSync(path.join(root, e.name)).mtimeMs }));
    for (const e of entries) {
      // A download in progress is younger than a day.
      if (e.name.startsWith('.') && now - e.t > 86_400_000) fs.rmSync(e.p, { recursive: true, force: true });
    }
    const old = entries.filter((e) => !e.name.startsWith('.') && e.name !== current).sort((a, b) => b.t - a.t);
    for (const e of old.slice(KEEP - 1)) discard(e.p, root);
  } catch {
    // best effort
  }
}

// ---------------------------------------------------------------------------------------------------------------

/** Runs bin with args in this terminal; resolves to what to exit with: {code} or {signal}. */
function run(bin, args, { platform = process.platform } = {}) {
  return new Promise((resolve) => {
    const child = spawn(bin, args, { stdio: 'inherit' });
    // Signals sent to this process go to Retake, which stops cleanly. Ctrl+C in a terminal reaches both already;
    // Retake treats the second one the same. On Windows, Ctrl+C reaches Retake through the console, and kill() would
    // end it abruptly, so this process only waits.
    const signals = platform === 'win32' ? ['SIGINT', 'SIGBREAK'] : ['SIGINT', 'SIGTERM', 'SIGHUP', 'SIGQUIT'];
    const handlers = signals.map((sig) => {
      const h = () => {
        if (platform !== 'win32' && child.exitCode === null && child.signalCode === null) {
          try {
            child.kill(sig);
          } catch {
            // already gone
          }
        }
      };
      process.on(sig, h);
      return [sig, h];
    });
    const done = (result) => {
      for (const [sig, h] of handlers) process.removeListener(sig, h);
      resolve(result);
    };
    child.on('error', (err) => done({ code: 1, error: err }));
    child.on('exit', (code, signal) => done(signal ? { signal } : { code: code ?? 1 }));
  });
}

async function main() {
  let bin = locate();
  if (bin && typeof bin === 'object' && !(bin instanceof Error)) bin = await fetchBuild(bin);
  if (bin instanceof Error) {
    process.stderr.write(`retake: ${bin.message}\n`);
    process.exit(1);
  }
  const result = await run(bin, process.argv.slice(2));
  if (result.error) {
    process.stderr.write(`retake: couldn't run ${bin}: ${result.error.message}\n`);
  }
  if (result.signal) {
    process.kill(process.pid, result.signal); // ended by a signal: end the same way, so the shell sees it
    return;
  }
  process.exit(result.code);
}

module.exports = { packageFor, locate, run, cacheRoot, fetchBuild, unzip };

if (require.main === module) main();
