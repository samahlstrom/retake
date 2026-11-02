// WebKit for the phone checks (iPhone Safari's engine). Playwright's WebKit build doesn't run on every Linux host, so
// the browser runs in Playwright's own Docker image as a browser server, and the tests (and the servers, ffmpeg and
// the fixtures) stay on the host and connect to it over a websocket. RETAKE_E2E_WEBKIT turns the `webkit` project on:
// - `docker`: the global setup starts the container (host network, so the browser reaches 127.0.0.1), the teardown
//   stops it;
// - a `ws://…` URL: a browser server you started yourself (see e2e/README.md).
// Without it the project isn't there, and the WebKit specs skip under Chromium.
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createConnection } from 'node:net';
import { join } from 'node:path';
import { REPO } from './env.ts';

const MODE = process.env.RETAKE_E2E_WEBKIT ?? '';
/** The image that matches the suite's @playwright/test (1.64.1): its WebKit is the one the client expects. */
export const WEBKIT_IMAGE = process.env.RETAKE_E2E_WEBKIT_IMAGE ?? 'mcr.microsoft.com/playwright:v1.64.1-noble';
/** The browser server's port on the host (the container shares the host's network). */
export const WEBKIT_PORT = Number.parseInt(process.env.RETAKE_E2E_WEBKIT_PORT ?? '8950', 10);
/** The websocket the `webkit` project connects to, or null: no WebKit this run. */
export const WEBKIT_WS: string | null = MODE.startsWith('ws://') || MODE.startsWith('wss://')
  ? MODE
  : MODE === 'docker'
    ? `ws://127.0.0.1:${WEBKIT_PORT}/`
    : null;
/** The container this run started (its id), so the teardown stops only that one. */
const ID_FILE = join(REPO, '.retake-work', 'e2e-webkit-container');

function portOpen(port: number): Promise<boolean> {
  return new Promise((done) => {
    const s = createConnection({ host: '127.0.0.1', port });
    s.once('connect', () => { s.destroy(); done(true); });
    s.once('error', () => done(false));
  });
}

/**
 * Starts the browser server in Docker (RETAKE_E2E_WEBKIT=docker): the host's e2e/node_modules mounted read-only, so
 * the server is the suite's own Playwright, and the image's browsers. Waits for its port.
 */
export async function startWebkit(): Promise<void> {
  if (MODE !== 'docker') return;
  stopWebkit(); // one a crashed run left behind
  if (await portOpen(WEBKIT_PORT)) throw new Error(`webkit: port ${WEBKIT_PORT} is taken; set RETAKE_E2E_WEBKIT_PORT`);
  const modules = join(REPO, 'e2e', 'node_modules');
  const id = execFileSync('docker', [
    'run', '-d', '--rm', '--init', '--network', 'host', '--ipc=host',
    '-v', `${modules}:/pw/node_modules:ro`,
    WEBKIT_IMAGE,
    'node', '/pw/node_modules/playwright-core/cli.js', 'run-server', '--port', String(WEBKIT_PORT), '--host', '127.0.0.1',
  ]).toString().trim();
  writeFileSync(ID_FILE, id);
  const end = Date.now() + 60_000;
  while (!(await portOpen(WEBKIT_PORT))) {
    if (Date.now() > end) {
      stopWebkit();
      throw new Error(`webkit: the browser server in ${WEBKIT_IMAGE} didn't open port ${WEBKIT_PORT}`);
    }
    await new Promise((r) => setTimeout(r, 300));
  }
}

/** Stops the container this run started (by its id), if any. */
export function stopWebkit(): void {
  if (!existsSync(ID_FILE)) return;
  const id = readFileSync(ID_FILE, 'utf8').trim();
  rmSync(ID_FILE, { force: true });
  if (!/^[0-9a-f]{12,64}$/.test(id)) return;
  try {
    execFileSync('docker', ['stop', '-t', '2', id], { stdio: 'ignore' });
  } catch {
    /* already gone (--rm) */
  }
}
