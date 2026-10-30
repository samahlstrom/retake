// Where things are, and which server is under test. Every path here is derived, never hard-coded per machine.
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const E2E_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const REPO = resolve(E2E_DIR, '..');

export type Mode = 'new' | 'legacy';
const mode = process.env.RETAKE_SERVER ?? 'new';
if (mode !== 'new' && mode !== 'legacy') throw new Error(`RETAKE_SERVER must be "new" or "legacy", not "${mode}"`);
/** The server under test: `new` = bin/retake (Go + Svelte), `legacy` = the Python review player. */
export const MODE: Mode = mode;
export const LEGACY = MODE === 'legacy';

/** The new server's binary (`make build`). */
export const RETAKE_BIN = resolve(process.env.RETAKE_BIN ?? join(REPO, 'bin', 'retake'));
/** The legacy launcher, run with its own venv. Read-only reference: the tests only run it. */
export const LEGACY_REVIEW = resolve(process.env.RETAKE_LEGACY_REVIEW
  ?? join(homedir(), 'deadlock-editing-style', 'skill', 'scripts', 'review', 'review'));

/** Scratch root for one run: media, version families, takes and server logs. Wiped at the start of every run. */
export const WORK = resolve(process.env.RETAKE_E2E_WORK ?? join(REPO, '.retake-work', 'e2e'));
export const WORK_MARKER = join(WORK, '.retake-e2e');
export const MEDIA = join(WORK, 'media');
export const MIC_WAV = join(MEDIA, 'mic.wav');
/** The servers get XDG_CONFIG_HOME pointing here, so no real user config leaks in and the author name is known. */
export const CONFIG_HOME = join(WORK, 'config');
export const AUTHOR = 'E2E Reviewer';

/** HTTP ports for the servers under test, one per spec file, and the crash test's proxy (all inside 8831-8839). */
export const PORTS = { scenario: 8831, crash: 8832, hls: 8833, crashProxy: 8834 } as const;

/** Keep the scratch data even when everything passes. */
export const KEEP = !!process.env.RETAKE_E2E_KEEP;

/** Chromium: CHROMIUM_PATH, else the system build when it exists, else Playwright's own. */
export function chromiumPath(): string | undefined {
  if (process.env.CHROMIUM_PATH) return process.env.CHROMIUM_PATH;
  const system = '/usr/lib/chromium/chromium'; // the plain binary; /usr/bin/chromium adds desktop flags
  return existsSync(system) ? system : undefined;
}
