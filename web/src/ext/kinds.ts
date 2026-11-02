// What extensions add to the pages, from GET api/ext: kinds of versions (how a version strip names `v0 · Script`, which
// slot shows it, whether R records there), the comments column's extra tabs, and the home's nav sections. Pure, so it
// is unit-tested.
import type { Card } from '../lib/home';
import type { ExtInfo, ExtKind } from './loader';

/** The extension and kind of a full kind id (`scripts.doc`), or null when no extension on here has it. */
export function kindOf(
  exts: readonly ExtInfo[],
  kind: string | null | undefined,
): { ext: ExtInfo; kind: ExtKind } | null {
  if (!kind) return null;
  for (const ext of exts) {
    const k = ext.kinds.find((x) => x.id === kind);
    if (k) return { ext, kind: k };
  }
  return null;
}

/** Whether a full kind id is one of `ext`'s. */
export const owns = (ext: ExtInfo, kind: string | null | undefined): boolean =>
  !!kind && (ext.kinds.some((k) => k.id === kind) || kind.startsWith(`${ext.id}.`));

/** How a version reads: `v0 · Script` for a kind's version (`v0` alone when its extension isn't on), else its label. */
export function versionName(exts: readonly ExtInfo[], v: { label: string; kind?: string }): string {
  if (!v.kind) return v.label;
  const label = kindOf(exts, v.kind)?.kind.label;
  return label ? `${v.label} · ${label}` : v.label;
}

/** The extension whose `version` slot shows a version of `kind`. */
export function versionSlot(exts: readonly ExtInfo[], kind: string | null | undefined): ExtInfo | null {
  if (!kind) return null;
  return (
    exts.find((e) => e.slots.some((s) => s.slot === 'version' && (s.kind === kind || (!s.kind && owns(e, kind))))) ??
    null
  );
}

/** Record (and R) on a version of `kind`: only when the kind says `record = true`. */
export const kindRecords = (exts: readonly ExtInfo[], kind: string | null | undefined): boolean =>
  !!kindOf(exts, kind)?.kind.record;

/** The extension of a kind whose version has the agent chat in place of the comments (spec/chat.md), or null. */
export const kindChat = (exts: readonly ExtInfo[], kind: string | null | undefined): ExtInfo | null => {
  const k = kindOf(exts, kind);
  return k?.kind.chat ? k.ext : null;
};

/** One of the comments column's extension tabs. */
export interface ExtTab {
  /** `<id>/<slot>`. */
  key: string;
  ext: ExtInfo;
  slot: 'sidebar-tab' | 'review-panel';
  title: string;
}

/**
 * The comments column's tabs after To do · Done: the extensions' sidebar tabs and review panels. On a video version
 * only (a kind's version has its own slot); `when = "has-kind"` only on videos that have a version of its kind; a
 * review panel never on a phone.
 */
export function sidebarTabs(
  exts: readonly ExtInfo[],
  versions: readonly { label: string; kind?: string }[],
  o: { kind: string | null; phone: boolean },
): ExtTab[] {
  if (o.kind) return [];
  const out: ExtTab[] = [];
  for (const ext of exts) {
    for (const s of ext.slots) {
      if (s.slot !== 'sidebar-tab' && s.slot !== 'review-panel') continue;
      if (s.slot === 'review-panel' && o.phone) continue;
      if (s.when === 'has-kind' && !versions.some((v) => owns(ext, v.kind) && (!s.kind || v.kind === s.kind))) continue;
      const key = `${ext.id}/${s.slot}`;
      if (out.some((t) => t.key === key)) continue;
      out.push({ key, ext, slot: s.slot, title: s.title || ext.name });
    }
  }
  return out;
}

/** The video on screen when the review page opens: the newest cut that plays (as without extensions), a `?v=` deep
 * link when it's in the family, else the current one (a video with only a kind's version opens on it). */
export function openingVersion(
  versions: readonly { label: string; kind?: string; playable?: boolean }[],
  current: string | null | undefined,
  want: string | null | undefined,
): string | null {
  if (want && versions.some((v) => v.label === want)) return want;
  const cut = [...versions].reverse().find((v) => !v.kind && v.playable !== false);
  return cut?.label || current || versions.find((v) => v.kind)?.label || null;
}

// ---------------------------------------------------------------- the home

/** The chip of a home card whose only version is a kind's (`status: "no_cut"`): the kind's status word ("Idea"). */
export function kindStatus(exts: readonly ExtInfo[], card: Pick<Card, 'status' | 'kind'>): string | null {
  if (card.status !== 'no_cut') return null;
  return kindOf(exts, card.kind)?.kind.status || 'No cut yet';
}

/** The URL of an extension's nav icon, relative to the page's root, or ''. */
export const iconUrl = (ext: ExtInfo, root = ''): string =>
  ext.nav?.icon ? `${root}${ext.base}${ext.nav.icon.replace(/^\.?\//, '')}` : '';

/** The extension a home card's kind belongs to, for its icon in the tree (in place of its status dot). */
export function cardExt(exts: readonly ExtInfo[], card: Pick<Card, 'status' | 'kind'>): ExtInfo | null {
  return card.status === 'no_cut'
    ? (kindOf(exts, card.kind)?.ext ?? exts.find((e) => owns(e, card.kind)) ?? null)
    : null;
}

/** One extension's section under the folder tree. */
export interface NavSection {
  ext: ExtInfo;
  title: string;
  icon: string;
  action: { title: string; route: string } | null;
  /** With `list = "kind-only"`: the videos whose only version is its kind, the most recently changed first. */
  rows: Card[];
}

export function navSections(exts: readonly ExtInfo[], cards: readonly Card[]): NavSection[] {
  return exts.flatMap((ext) => {
    if (!ext.nav) return [];
    const rows =
      ext.nav.list === 'kind-only'
        ? cards
            .filter((c) => c.status === 'no_cut' && owns(ext, c.kind))
            .map((c, i) => ({ c, i }))
            .sort((a, b) => (b.c.changed ?? 0) - (a.c.changed ?? 0) || a.i - b.i)
            .map((x) => x.c)
        : [];
    return [{ ext, title: ext.nav.title, icon: iconUrl(ext), action: ext.nav.action, rows }];
  });
}

/** The home's hash of an extension's page: `#ext/<id>/<route>`. */
export const extHash = (id: string, route = ''): string => `#ext/${id}/${route.replace(/^\/+/, '')}`;
