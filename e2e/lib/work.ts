// The run's scratch root: wiped at the start, removed after a passing run, kept after a failing one.
import { existsSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { AUTHOR, CONFIG_HOME, WORK, WORK_MARKER } from './env.ts';
import { linkFamily, mediaLayout } from './fixtures.ts';

/** Removes WORK, but only if it is ours (marker file) or empty: a wrong RETAKE_E2E_WORK must not delete anything. */
export function removeWork(): void {
  if (!existsSync(WORK)) return;
  if (!existsSync(WORK_MARKER) && readdirSync(WORK).length > 0) {
    throw new Error(`refusing to delete ${WORK}: it is not an e2e scratch dir (no .retake-e2e marker)`);
  }
  rmSync(WORK, { recursive: true, force: true });
}

export function resetWork(): void {
  removeWork();
  mkdirSync(WORK, { recursive: true });
  writeFileSync(WORK_MARKER, 'Scratch data of the retake e2e suite. Safe to delete.\n');
  mkdirSync(join(CONFIG_HOME, 'retake'), { recursive: true });
  // Read by the new server only. Flags override it; the author name lands in session.json. find_agents = false keeps
  // the local editing worker from ever running a real Claude Code or Codex found on PATH.
  writeFileSync(
    join(CONFIG_HOME, 'retake', 'config.toml'),
    `name = "${AUTHOR}"\nopen_browser = false\nfind_agents = false\n`,
  );
}

export type Version = 'v5' | 'v6' | 'v7' | 'v8';
export interface Family { dir: string; files: Record<string, string> }

/** A fresh version family under WORK/<name>, hardlinked from the generated media. */
export function family(name: string, versions: Version[]): Family {
  const dir = join(WORK, name);
  rmSync(dir, { recursive: true, force: true });
  return { dir, files: linkFamily(mediaLayout(WORK), dir, versions) };
}

/** Take folders (YYYY-MM-DD_HH-MM-SS) in a version's .review folder, sorted. */
export function takeDirs(reviewDir: string): string[] {
  if (!existsSync(reviewDir)) return [];
  return readdirSync(reviewDir).filter((d) => /^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$/.test(d)).sort()
    .map((d) => join(reviewDir, d));
}

/** Every take folder anywhere under a directory (for schema validation). */
export function allTakeDirs(root: string): string[] {
  const out: string[] = [];
  const walk = (d: string): void => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      if (!e.isDirectory()) continue;
      const p = join(d, e.name);
      if (e.name.endsWith('.review')) out.push(...takeDirs(p));
      else walk(p);
    }
  };
  if (existsSync(root)) walk(root);
  return out.sort();
}
