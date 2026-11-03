// The one version menu (spec/notes.md, "The version menu"): its rows and its button's one dot. Pure, so it is
// unit-tested. The rows come from the version strip (lib/notes/view.ts: a version and its revisions are one entry,
// with its To do count), and read the way the page names versions everywhere (`verName`: never "draft").
import type { VersionEntry } from './notes/view';
import type { Version } from './types';
import { fmt } from './util';
import { incomplete, streamChip } from './versions';

export interface MenuRow {
  /** Its key: the version (`v5`), or an extension's version's label (`v0`). */
  version: string;
  /** What a pick shows: its newest listed revision (`v5-draft3` while v5 is live). */
  label: string;
  /** What it reads: `v5`, or an extension's version by its kind's label (`Script`). */
  name: string;
  /** An extension's version (v0): the last row, set apart. */
  kind: boolean;
  /** The agent works on it now: a small red dot right after the name. */
  live: boolean;
  /** It is on screen: in bold. */
  on: boolean;
  /** The one quiet detail at the right: a stream's state, its comments to do, or its length; '' for none. */
  detail: string;
}

type V = Pick<Version, 'label' | 'duration' | 'stream'>;

/** The menu's rows: the versions newest first, then an extension's version (v0). `live` is the version the agent
 * works on (status.ts `liveTarget`), or null. Empty when there is nothing to choose (one version). */
export function menuRows(strip: readonly VersionEntry[], versions: readonly V[], live: string | null): MenuRow[] {
  const row = (e: VersionEntry): MenuRow => {
    const isLive = !e.kind && e.version === live;
    const v = versions.find((x) => x.label === e.label);
    const todo = e.todo ? `${e.todo} to do` : '';
    const length = v?.duration ? fmt(v.duration, false) : '';
    const chip = !e.kind && !isLive && v ? streamChip(v) : null;
    return {
      version: e.version,
      label: e.label,
      name: e.name,
      kind: !!e.kind,
      live: isLive,
      on: e.on,
      detail: e.kind ? todo : (chip ?? (todo || length)),
    };
  };
  return [...strip.filter((e) => !e.kind).reverse(), ...strip.filter((e) => e.kind)].map(row);
}

/** The button's one dot: red while the shown version is live, else a streamed version still rendering, else a quiet
 * one while an earlier version has comments to do; null for none. */
export function menuDot(o: {
  shownLive: boolean;
  shown: Pick<Version, 'stream'> | null | undefined;
  earlier: number;
}): 'live' | 'rendering' | 'earlier' | null {
  if (o.shownLive) return 'live';
  if (incomplete(o.shown)) return 'rendering';
  return o.earlier > 0 ? 'earlier' : null;
}

/** The button's title: the version, what C flips to, and where comments are still to do. */
export function menuTitle(o: {
  name: string;
  flipTo: string | null;
  flipped: { shown: string; base: string } | null;
  earlier: readonly Pick<VersionEntry, 'version' | 'todo'>[];
}): string {
  const head = o.flipped
    ? `Comparing: showing ${o.flipped.shown}. C goes back to ${o.flipped.base}`
    : `Version ${o.name}${o.flipTo ? `. C flips to ${o.flipTo}` : ''}`;
  const open = o.earlier.length
    ? `. Open on earlier versions: ${o.earlier.map((e) => `${e.version} · ${e.todo}`).join(', ')}`
    : '';
  return head + open;
}
