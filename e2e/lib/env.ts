// Where things are, and which server is under test. Every path here is derived, never hard-coded per machine.
import { existsSync } from 'node:fs';
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
/**
 * The legacy launcher, run with its own venv. Read-only reference: the tests only run it. Legacy mode requires
 * RETAKE_LEGACY_REVIEW: there is no default, since the legacy player isn't part of this repo.
 */
const legacyReview = process.env.RETAKE_LEGACY_REVIEW;
if (LEGACY && !legacyReview) {
  throw new Error('RETAKE_SERVER=legacy needs RETAKE_LEGACY_REVIEW, the legacy player\'s `review` launcher');
}
export const LEGACY_REVIEW = legacyReview ? resolve(legacyReview) : '';

/** Scratch root for one run: media, version families, takes and server logs. Wiped at the start of every run. */
export const WORK = resolve(process.env.RETAKE_E2E_WORK ?? join(REPO, '.retake-work', 'e2e'));
export const WORK_MARKER = join(WORK, '.retake-e2e');
export const MEDIA = join(WORK, 'media');
export const MIC_WAV = join(MEDIA, 'mic.wav');
/** The servers get XDG_CONFIG_HOME pointing here, so no real user config leaks in and the author name is known. */
export const CONFIG_HOME = join(WORK, 'config');
export const AUTHOR = 'E2E Reviewer';

/**
 * HTTP ports for the servers under test, and the proxies of the crash and share tests: RETAKE_E2E_PORT_BASE (default
 * 8831) and the next eight, so parallel checkouts can run the suite side by side. The spec files run one after
 * another and each stops its servers, so later files reuse the ports of earlier ones; the nine stay a fixed block.
 */
const PORT_BASE = Number.parseInt(process.env.RETAKE_E2E_PORT_BASE ?? '8831', 10);
if (!(PORT_BASE > 0 && PORT_BASE < 65527)) throw new Error(`bad RETAKE_E2E_PORT_BASE: ${process.env.RETAKE_E2E_PORT_BASE}`);
export const PORTS = {
  scenario: PORT_BASE,
  crash: PORT_BASE + 1,
  hls: PORT_BASE + 2,
  crashProxy: PORT_BASE + 3,
  sidebar: PORT_BASE + 4,
  mobile: PORT_BASE + 5,
  share: PORT_BASE + 6,
  shareProxy: PORT_BASE + 7,
  timemap: PORT_BASE + 8,
  teamServer: PORT_BASE + 4, // 09, after 05-sidebar
  eraser: PORT_BASE + 5, // 10, after 06-mobile
  home: PORT_BASE + 2, // 12, after 11-stream
  stream: PORT_BASE + 2, // 11, after 04-hls
  relay: PORT_BASE + 6, // 14, after 07-share
  nav: PORT_BASE + 2, // 16, after 12-home
} as const;

/** retake-edit, for the specs that render with the editor: $RETAKE_EDIT, else the repo's release build, else $PATH. */
export function retakeEdit(): string | null {
  if (process.env.RETAKE_EDIT) return existsSync(process.env.RETAKE_EDIT) ? resolve(process.env.RETAKE_EDIT) : null;
  const built = join(REPO, 'editor', 'target', 'release', 'retake-edit');
  if (existsSync(built)) return built;
  for (const dir of (process.env.PATH ?? '').split(':')) {
    if (dir && existsSync(join(dir, 'retake-edit'))) return join(dir, 'retake-edit');
  }
  return null;
}

/** Keep the scratch data even when everything passes. */
export const KEEP = !!process.env.RETAKE_E2E_KEEP;

/** Chromium: CHROMIUM_PATH, else the system build when it exists, else Playwright's own. */
export function chromiumPath(): string | undefined {
  if (process.env.CHROMIUM_PATH) return process.env.CHROMIUM_PATH;
  const system = '/usr/lib/chromium/chromium'; // the plain binary; /usr/bin/chromium adds desktop flags
  return existsSync(system) ? system : undefined;
}
