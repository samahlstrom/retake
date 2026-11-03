// The npm launcher (npm/retake-cli/bin/retake.js): which binary it picks, what it says when it can't, and that exit
// codes and signals pass through. `node --test npm/` (make test runs it). The whole install, with a registry, is
// scripts/npm-smoke.mjs.
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { packageFor, locate, run } = require('./retake-cli/bin/retake.js');

const pkg = {
  name: 'retake-cli',
  optionalDependencies: {
    'retake-cli-linux-x64': '1.0.0',
    'retake-cli-darwin-arm64': '1.0.0',
    'retake-cli-win32-x64': '1.0.0',
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
  const w = platformPackage(t, 'retake-cli-win32-x64', { binary: 'retake.exe' });
  assert.equal(locate({ platform: 'win32', arch: 'x64', pkg, resolve: w.resolve }), path.join(w.root, 'bin', 'retake.exe'));
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
