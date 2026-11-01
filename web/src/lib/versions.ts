// Version family logic: C flips to the base's comparison partner, and showing a version lands on the matching moment.
// Pure, so it is unit-tested.
import { mapTime, type Segment } from './timemap';
import type { StreamState, Version } from './types';
import { fmt } from './util';

type V = Pick<Version, 'label' | 'n' | 'duration' | 'stream'>;
type Span = Pick<Version, 'duration' | 'stream'>;

export const byLabel = <T extends V>(vs: readonly T[], label: string | null): T | undefined =>
  vs.find((v) => v.label === label);

/** The version `dir` steps away from `label` in family order (sorted by n), or null. */
export function neighbor<T extends V>(vs: readonly T[], label: string | null, dir: number): T | null {
  const i = vs.findIndex((v) => v.label === label);
  return i < 0 ? null : (vs[i + dir] ?? null);
}

/** A streamed version that isn't complete yet (spec/bridge.md, "Streaming"): it renders while someone watches it. */
export const incomplete = (v: Pick<Version, 'stream'> | null | undefined): boolean =>
  !!v?.stream && v.stream.state !== 'complete';

/** How far an incomplete streamed version can be played: its playable seconds, or null for any other version. */
export const renderedEnd = (v: Span | null | undefined): number | null =>
  v && incomplete(v) ? Math.max(0, Math.min(v.stream?.seconds ?? 0, v.duration || Infinity)) : null;

/** Whether version `v` has a frame at time `t` (a preview may end early; an incomplete stream only has what it has
 * rendered). Unknown durations cover everything. */
export const covers = (v: Span, t: number): boolean => {
  const r = renderedEnd(v);
  if (r !== null) return t <= r - 0.05;
  return !v.duration || t <= v.duration - 0.05;
};

/** The refusal toast when a version doesn't reach the playhead (C, or the picker, past a preview's end). */
export const coverMessage = (v: Pick<Version, 'label' | 'duration'>): string =>
  `${v.label} only covers 0:00–${fmt(v.duration || 0, false)}`;

/** The toast when showing an incomplete stream lands at its start, the moment asked for not being rendered yet. */
export const fromStartMessage = (v: Pick<Version, 'label' | 'duration' | 'stream'>): string =>
  `${v.label} is rendering: ${fmt(renderedEnd(v) ?? 0, false)} of ${fmt(v.duration || 0, false)} so far, from the start`;

/** The version list's chip for an incomplete stream (`pending`, `rendering 12/50`), or null. */
export function streamChip(v: Pick<Version, 'stream'>): string | null {
  const s = v.stream;
  if (!s || s.state === 'complete') return null;
  if (s.state === 'pending') return 'pending';
  if (s.state === 'stopped') return 'stopped';
  return s.total > 0 ? `rendering ${s.done}/${s.total}` : 'rendering';
}

/** A `progress` event or a watch answer: the family with that streamed version's state replaced (the same array when
 * nothing changes), so the chip and the coverage follow without refetching /api/info. */
export function withStream<T extends V>(vs: readonly T[], label: string, s: StreamState): readonly T[] {
  const i = vs.findIndex((v) => v.label === label);
  const old = vs[i]?.stream;
  if (!old) return vs; // unknown, or not a streamed version
  if (old.state === s.state && old.done === s.done && old.total === s.total && old.seconds === s.seconds) return vs;
  return vs.map((v, j) => (j === i ? { ...v, stream: { ...s } } : v));
}

/** The incomplete streams in `vs` that `known` hasn't seen yet (each is announced once). */
export const newlyStreamed = <T extends V>(vs: readonly T[], known: ReadonlySet<string>): T[] =>
  vs.filter((v) => incomplete(v) && !known.has(v.label));

/** The toast when a streamed version's render failed (it stays; the next watch tries again). */
export const failedToast = (label: string, error?: string): string =>
  error ? `${label}'s render failed: ${error}` : `${label}'s render failed`;

/** The newest version or draft that can play the moment `t`: what following the latest shows (spec/automation.md,
 * "Versions versus drafts"). A stopped stream is skipped, and so is an incomplete one that hasn't rendered `t` yet. */
export const latestPlayable = <T extends V>(vs: readonly T[], t: number): T | undefined =>
  [...vs].reverse().find((v) => v.stream?.state !== 'stopped' && (!incomplete(v) || (renderedEnd(v) ?? 0) > t));

/** Where C back lands when it isn't simply the base (spec/bridge.md, "The page"): following the latest, or with a
 * base the family no longer lists (a replaced draft), the newest version or draft that plays `t`, unless that is the
 * base. null: back to the base. */
export function catchUp<T extends V>(vs: readonly T[], base: string | null, following: boolean, t: number): T | null {
  if (!following && byLabel(vs, base)) return null;
  const latest = latestPlayable(vs, t);
  return latest && latest.label !== base ? latest : null;
}

/** C compares the base with its partner: the next newer version if there is one (v6 <-> v7), else the previous one.
 * A draft (`v5-draft2`, the agent's work heading for v5) compares with the version it is an edit of, the newest
 * listed version before it that isn't a draft (v4): following the agent's work live, C shows what it changed. */
export function partner<T extends V>(vs: readonly T[], label: string | null): T | null {
  const i = vs.findIndex((v) => v.label === label);
  if (i > 0 && draftOf(label)) {
    const from = vs.slice(0, i).findLast((v) => !draftOf(v.label));
    if (from) return from;
  }
  return neighbor(vs, label, 1) || neighbor(vs, label, -1);
}

/** Where C goes: back to the base when flipped, else to the partner. */
export const flipTarget = <T extends V>(vs: readonly T[], base: string | null, flipped: boolean): T | null =>
  (flipped ? byLabel(vs, base) : partner(vs, base)) ?? null;

/**
 * Why C would be refused with the playhead at `t`: the toast it shows then ('Only one version', or the partner only
 * covers part of the video). null when C would flip; an incomplete stream never refuses (it lands at its start).
 * `mapT` maps `t` onto the target version (the time map); without it the time stays.
 */
export function flipRefusal(
  vs: readonly V[],
  base: string | null,
  flipped: boolean,
  t: number,
  mapT?: (label: string, t: number) => number,
): string | null {
  const target = flipTarget(vs, base, flipped);
  if (!target) return 'Only one version';
  if (incomplete(target)) return null;
  return covers(target, mapT ? mapT(target.label, t) : t) ? null : coverMessage(target);
}

/**
 * Where showing version `v` lands from time `t` of the version on screen: the matching moment through the time map
 * (the same time without one), or the toast that refuses it when `v` doesn't reach that moment. An incomplete stream
 * is never refused: a moment it hasn't rendered yet lands at its start, with `note` to say so (none when the moment
 * was the start anyway).
 */
export function landing(
  v: Pick<Version, 'label' | 'duration' | 'stream'>,
  t: number,
  segs: readonly Segment[] | null | undefined,
): { t: number; note?: string } | { refuse: string } {
  const at = mapTime(segs, t);
  if (covers(v, at)) return { t: at };
  if (incomplete(v)) return at <= 0.05 ? { t: 0 } : { t: 0, note: fromStartMessage(v) };
  return { refuse: coverMessage(v) };
}

/** The two versions C toggles between, for the flip button: [base, partner], or null with a single version. */
export function flipPair(vs: readonly V[], base: string | null): [string, string] | null {
  const pt = partner(vs, base);
  return base && pt ? [base, pt.label] : null;
}

/** The toast after a successful flip: `v7 (newer)` / `v5 (previous)` while flipped, just the label when back. A
 * draft reads as on screen elsewhere: `v5 draft`. */
export function flipToast(vs: readonly V[], base: string | null, target: V, nowFlipped: boolean): string {
  if (!nowFlipped) return verName(target.label);
  const b = byLabel(vs, base);
  return `${verName(target.label)} (${b && target.n > b.n ? 'newer' : 'previous'})`;
}

/** The version button's tooltip. */
export function versionTitle(vs: readonly V[], base: string | null, shown: string | null, flipped: boolean): string {
  const pt = base ? partner(vs, base) : null;
  return flipped
    ? `Comparing: showing ${shown}. C goes back to ${base}.`
    : `Version ${base}${pt ? `. C flips to ${pt.label}` : ''}`;
}

/** A draft render (contract C4): `v5-draft2` is the agent's second draft heading for v5. Drafts aren't versions of
 * their own, so the page shows them quietly as "v5" with a draft tag. Returns the version it heads for, or null. */
export const draftOf = (label: string | null | undefined): string | null =>
  /^(v\d+)-draft\d+$/.exec(label ?? '')?.[1] ?? null;

/** How a label reads on screen: "v5" for a version, "v5 draft" for a draft of it. */
export const verName = (label: string | null | undefined): string => {
  const d = draftOf(label);
  return d ? `${d} draft` : (label ?? '');
};

/** The newest real version (not a draft), for the list's "Latest". */
export const latestVersion = <T extends { label: string }>(vs: readonly T[]): T | undefined =>
  [...vs].reverse().find((v) => !draftOf(v.label));
