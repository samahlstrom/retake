// Version family logic: [ ] step the base version, C flips to its comparison partner. Pure, so it is unit-tested.
import type { Version } from './types';
import { fmt } from './util';

type V = Pick<Version, 'label' | 'n' | 'duration'>;

export const byLabel = <T extends V>(vs: readonly T[], label: string | null): T | undefined =>
  vs.find((v) => v.label === label);

/** The version `dir` steps away from `label` in family order (sorted by n), or null. */
export function neighbor<T extends V>(vs: readonly T[], label: string | null, dir: number): T | null {
  const i = vs.findIndex((v) => v.label === label);
  return i < 0 ? null : (vs[i + dir] ?? null);
}

/** Whether version `v` has a frame at time `t` (a preview may end early). Unknown durations cover everything. */
export const covers = (v: Pick<Version, 'duration'>, t: number): boolean => !v.duration || t <= v.duration - 0.05;

/** The refusal toast when a version doesn't reach the playhead (C or ] past a preview's end). */
export const coverMessage = (v: Pick<Version, 'label' | 'duration'>): string =>
  `${v.label} only covers 0:00–${fmt(v.duration || 0, false)}`;

/** C compares the base with its partner: the next newer version if there is one (v6 <-> v7), else the previous one. */
export const partner = <T extends V>(vs: readonly T[], label: string | null): T | null =>
  neighbor(vs, label, 1) || neighbor(vs, label, -1);

/** Where C goes: back to the base when flipped, else to the partner. */
export const flipTarget = <T extends V>(vs: readonly T[], base: string | null, flipped: boolean): T | null =>
  (flipped ? byLabel(vs, base) : partner(vs, base)) ?? null;

/** The toast after a successful flip: `v7 (newer)` / `v5 (previous)` while flipped, just the label when back. */
export function flipToast(vs: readonly V[], base: string | null, target: V, nowFlipped: boolean): string {
  if (!nowFlipped) return target.label;
  const b = byLabel(vs, base);
  return `${target.label} (${b && target.n > b.n ? 'newer' : 'previous'})`;
}

/** What [ or ] would do: the neighbor to step to, or the toast that refuses it. */
export function stepTarget<T extends V>(vs: readonly T[], base: string | null, dir: number): T | string {
  return neighbor(vs, base, dir) ?? (dir < 0 ? 'No earlier version' : 'No later version');
}

/** The version button's tooltip. */
export function versionTitle(vs: readonly V[], base: string | null, shown: string | null, flipped: boolean): string {
  const pt = base ? partner(vs, base) : null;
  return flipped
    ? `Comparing: showing ${shown}. C goes back to ${base}.`
    : `Version ${base}. [ and ] step${pt ? `, C flips to ${pt.label}` : ''}`;
}
