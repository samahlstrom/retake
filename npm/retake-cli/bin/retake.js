#!/usr/bin/env node
// The `retake` command of the npm package. Retake itself is a Go program; npm installs the build for this computer as
// an optional dependency, <this package's name>-<os>-<cpu> (only the one matching this OS and CPU). This script finds
// it and runs it with the same arguments, the same terminal, and passes signals in and the exit code out.
//
// Plain `retake` in a terminal starts Retake (`retake start`: the setup page the first time, then your videos).
// Agents never come through here: Retake gives them the Go binary's own path (spec/setup.md), so no node starts on
// each `retake ask`.
'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const manifest = require('../package.json');

const NAMES = { darwin: 'macOS', linux: 'Linux', win32: 'Windows' };
const INSTALL = 'https://github.com/samahlstrom/retake#install';

/** The platform package's name for this OS and CPU, e.g. retake-cli-linux-x64. */
function packageFor(platform, arch, name = manifest.name) {
  return `${name}-${platform}-${arch}`;
}

/** Where the Go binary is, or an Error that says plainly what's wrong and what to do. */
function locate({
  platform = process.platform,
  arch = process.arch,
  pkg = manifest,
  resolve = (id) => require.resolve(id),
  glibc = hasGlibc,
} = {}) {
  const want = packageFor(platform, arch, pkg.name);
  const known = Object.keys(pkg.optionalDependencies || {});
  if (!known.includes(want)) {
    const builds = known
      .map((n) => n.slice(pkg.name.length + 1))
      .map((p) => {
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
  const bin = locate();
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

module.exports = { packageFor, locate, run };

if (require.main === module) main();
