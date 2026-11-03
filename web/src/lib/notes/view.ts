// What the sidebar shows, from a GET /api/notes response. The server already picks the items (spec/notes.md); this
// applies the display rules on top, and is pure so it is unit-tested:
//  - newest version: the open questions and this session's notes, nothing answered;
//  - older version: the notes in their final state (✓ with the change, ? with the question);
//  - retracted notes are never shown.
import { fmt } from '../util';
import { draftOf } from '../versions';
import type { Box, HistoryStroke, Version } from '../types';
import type { Anchor, NoteItem, NotesResponse, TaskAsk } from './types';
import type { EditingTask } from '../automation.svelte';

/** ✓ answered (or accepted), ? asked, • open. */
export type Mark = 'answered' | 'question' | 'open';
export const MARKS: Record<Mark, string> = { answered: '✓', question: '?', open: '•' };

export interface Chip {
  label: string;
  /** The version is in the family (else the chip is disabled). */
  known: boolean;
  /** It is the version on screen. */
  on: boolean;
}

export interface Row {
  work?: EditingTask;
  version?: string;
  take?: string;
  seg?: string;
  drawings: string[];
  by: string;
  key: string;
  id: number;
  kind: 'note' | 'question';
  /** A question about a note: the note's id (the question belongs to that note's version). */
  note: number | null;
  mark: Mark;
  t0: number;
  t1: number;
  /** t0 as m:ss. */
  time: string;
  text: string;
  /** ✓: the one-line change. */
  change: string | null;
  /** ✓: the version that answered it. */
  answeredIn: string | null;
  /** "see it in vN": the answering version, when it's in the family and not on screen. */
  see: string | null;
  /** ? on an older version: the question the note became. */
  question: string | null;
  /** Answer: the thread id a recorded answer points at (`answer_to`). */
  answer: number | null;
  /** Reopen: answered (not yet accepted) notes of an older version. */
  reopen: boolean;
  options: Chip[];
  images: { url: string; box: Box }[];
  /** A note's still of its moment with its strokes drawn on it (a URL), for its drawing's thumbnail. */
  snapshot?: string | null;
  /** Carried over from the parent version: what became of its moment ("unchanged", "answered, check it", "context
   * changed"), with the version it came from. */
  carried: string | null;
  /** The agent's open question from inside its task, with its time as m:ss (or, about a document, its place). */
  ask: (TaskAsk & { time: string }) | null;
  /** A typed comment on an extension's version: it has no moment, its chip says where in the document it is. */
  typed?: boolean;
  anchor?: Anchor | null;
  /** A typed comment on a video: its pen lines, to draw on the video and on its card. */
  strokes?: HistoryStroke[];
  /** The version its edit goes into: a comment on v7 becomes an edit in v8 (the server's `target`, else the next
   * number). Null for a typed comment on an extension's document. */
  into: string | null;
  /** A version after `into` that exists already (the server's `later`): the edit updates `into` in place. */
  later: string | null;
}

/** The chip of a comment or question about a document: the heading it's under, else its file. */
export const anchorText = (a: Anchor | null | undefined): string =>
  a ? a.heading?.trim() || a.file.split('/').pop() || a.file : '';

/** The picture under a comment's drawing thumbnail, so its strokes sit on what they were drawn over: the still of
 * its moment (the note's snapshot), else the first frame of the version it was left on (a streamed version's
 * poster, or the server's `api/poster` when the page has one), else none: a plain dark box. A version that plays
 * from another computer has no server poster. */
export function sketchFrame(
  row: Pick<Row, 'snapshot' | 'version'>,
  versions: readonly Pick<Version, 'label' | 'poster' | 'relay'>[],
  shown: string | null,
  posters: boolean,
): string | null {
  if (row.snapshot) return row.snapshot;
  const v = versions.find((x) => x.label === (row.version || shown));
  if (!v) return null;
  if (v.poster) return v.poster;
  return posters && !v.relay ? `api/poster?v=${encodeURIComponent(v.label)}` : null;
}

/** Submitted work stays beside the live edit, even as its original reviewed version becomes historical. */
export function inCurrentReview(row: Row, shown: string | null): boolean {
  return (
    !row.version ||
    row.version === shown ||
    row.answeredIn === shown ||
    (!!row.work && row.work.state !== 'completed' && row.work.state !== 'cancelled')
  );
}

/** What a carried note's tag says. */
export const CARRIED: Record<string, string> = {
  unchanged: 'unchanged',
  check: 'answered, check it',
  changed: 'context changed',
};

/** The tag of a carried item: "answered, check it · from v7". */
export const carriedText = (item: Pick<NoteItem, 'carried'>): string | null => {
  const c = item.carried;
  if (!c) return null;
  return `${CARRIED[c.as] ?? c.as} · from ${c.from}`;
};

export interface ViewContext {
  /** Labels of the family. */
  versions: readonly { label: string }[];
  /** The version on screen. */
  shown: string | null;
  /** The page may record (spec/team.md: not a viewer). Without it there is no Answer. Default true. */
  record?: boolean;
  /** The page may approve and reopen (an approver or the owner). Without it there is no Reopen. Default true. */
  approve?: boolean;
}

const ANSWERED = new Set(['answered', 'accepted']);

/** Whether the sidebar shows this item at all. The newest version hides answered notes, except the parent's notes
 * carried onto it (an answered one there is what to check). */
export function visible(item: NoteItem, newest: boolean): boolean {
  if (item.status === 'retracted') return false;
  if (newest && ANSWERED.has(item.status) && !item.carried) return false;
  return true;
}

export function markOf(item: NoteItem): Mark {
  if (ANSWERED.has(item.status)) return 'answered';
  if (item.kind === 'question' || item.status === 'question' || item.work?.state === 'question') return 'question';
  return 'open';
}

export function toRow(item: NoteItem, newest: boolean, ctx: ViewContext): Row {
  const mark = markOf(item),
    known = (l: string) => ctx.versions.some((v) => v.label === l);
  const answeredIn = mark === 'answered' ? item.answered_in || null : null;
  const t1 = typeof item.t1 === 'number' && item.t1 >= item.t0 ? item.t1 : item.t0;
  return {
    work: item.work,
    version: item.version,
    take: item.take,
    seg: item.seg,
    drawings: item.drawings ?? [],
    by: item.by || (item.kind === 'question' ? 'Editor' : 'Reviewer'),
    key: `${item.kind}:${item.id}`,
    id: item.id,
    kind: item.kind,
    note: item.kind === 'question' && typeof item.note === 'number' ? item.note : null,
    mark,
    t0: item.t0,
    t1,
    time: fmt(item.t0, false),
    text: item.text,
    change: mark === 'answered' ? item.change || null : null,
    answeredIn,
    see: answeredIn && known(answeredIn) && answeredIn !== ctx.shown ? answeredIn : null,
    question: item.kind === 'note' && mark === 'question' ? item.question || null : null,
    answer: item.kind === 'question' && mark === 'question' && ctx.record !== false ? item.id : null,
    reopen: !newest && item.status === 'answered' && ctx.approve !== false,
    options: (item.options ?? []).map((label) => ({ label, known: known(label), on: label === ctx.shown })),
    images: item.images ?? [],
    snapshot: item.kind === 'note' ? item.snapshot || null : null,
    carried: carriedText(item),
    ask:
      item.kind === 'note' && item.ask && mark === 'question'
        ? { ...item.ask, time: item.ask.anchor ? anchorText(item.ask.anchor) : fmt(item.ask.t, false) }
        : null,
    // A typed comment on a video has its moment (it wants a version); one on an extension's version has none.
    typed: !!item.anchor || ((item.key ?? '').startsWith('doc/') && item.wants !== 'version'),
    anchor: item.anchor ?? null,
    strokes: item.kind === 'note' && item.strokes?.length ? typedStrokes(item) : undefined,
    into:
      typeof item.target === 'string' && item.target
        ? item.target
        : !!item.anchor || ((item.key ?? '').startsWith('doc/') && item.wants !== 'version')
          ? null
          : intoVersion(item.version || ctx.shown),
    later: typeof item.later === 'string' && item.later ? item.later : null,
  };
}

/** A typed comment's lines as the pen layer draws them, each at its own time (else the comment's). */
export function typedStrokes(item: Pick<NoteItem, 'strokes' | 't0' | 'version'>): HistoryStroke[] {
  return (item.strokes ?? [])
    .filter((st) => Array.isArray(st.points) && st.points.length)
    .map((st) => ({ video_t: typeof st.t === 'number' ? st.t : item.t0, color: st.color, points: st.points }));
}

/** The rows of the sidebar, in time order. */
export function sidebarRows(res: NotesResponse | null, ctx: ViewContext): Row[] {
  if (!res) return [];
  return res.items
    .filter((it) => visible(it, res.scope ? false : res.newest))
    .map((it) => toRow(it, res.scope ? false : res.newest, ctx))
    .sort((a, b) => a.t0 - b.t0 || (a.kind === b.kind ? a.id - b.id : a.kind === 'note' ? -1 : 1));
}

// ---------------------------------------------------------------- comments belong to their version

/** The version a label belongs to: itself, or for a draft the version it heads for (`v5-draft2` is `v5`'s). */
export const versionOf = (label: string): string => draftOf(label) ?? label;

/** Where a comment on `label` goes (spec/notes.md, "Comments belong to their version"): a comment on v7 becomes an
 * edit in v8, and one on a draft of v8 (`v8-draft2`) one in v9. Null for a label that isn't `v<N>`. */
export function intoVersion(label: string | null | undefined): string | null {
  const m = label ? /^v(\d+)$/.exec(versionOf(label)) : null;
  return m ? `v${Number(m[1]) + 1}` : null;
}

/** The version each row belongs to, by key: its own `version`, or for a question about a note, that note's. A row
 * the server didn't tag (an older server) belongs to whatever is shown, so it has none here. */
export function rowVersions(rows: readonly Row[]): Map<string, string> {
  const notes = new Map<number, Row>();
  for (const r of rows) if (r.kind === 'note') notes.set(r.id, r);
  const out = new Map<string, string>();
  for (const r of rows) {
    const own = (r.note != null ? notes.get(r.note)?.version : null) || r.version;
    if (own) out.set(r.key, versionOf(own));
  }
  return out;
}

/** The rows of the version on screen (spec/notes.md, "Comments belong to their version"): its comments and the
 * agent's questions about them, and nothing of another version. */
export function forVersion(rows: readonly Row[], shown: string | null): Row[] {
  if (!shown) return [...rows];
  const of = rowVersions(rows),
    on = versionOf(shown);
  return rows.filter((r) => (of.get(r.key) ?? on) === on);
}

/** How many comments are still to do on each version (by `versionOf`). An untagged row counts for `shown`. */
export function todoByVersion(
  rows: readonly Row[],
  todo: (row: Row) => boolean,
  shown: string | null,
): Map<string, number> {
  const of = rowVersions(rows),
    out = new Map<string, number>();
  for (const r of rows) {
    const v = of.get(r.key) ?? (shown ? versionOf(shown) : null);
    if (v && todo(r)) out.set(v, (out.get(v) ?? 0) + 1);
  }
  return out;
}

/** The labels whose takes hold the drawings of `label`'s comments: itself first, then the other drafts of its version
 * that comments were recorded on. */
export function historyLabels(rows: readonly Row[], label: string): string[] {
  const out = [label],
    on = versionOf(label);
  for (const r of rows) if (r.version && !out.includes(r.version) && versionOf(r.version) === on) out.push(r.version);
  return out;
}

/** One entry of the version strip at the top of the comments panel. */
export interface VersionEntry {
  /** The version, `v5`: its key. */
  version: string;
  /** What it reads: `v5`, or for an extension's kind of version `v0 · Script`. */
  name: string;
  /** An extension's kind of version (`scripts.doc`): it isn't a video. */
  kind?: string;
  /** What a click shows: its newest listed revision (the version itself, or while it's live the newest draft of it:
   * spec/automation.md, "The live version"). */
  label: string;
  /** That newest revision is a draft: the version is being made or updated (it reads `v5`, live while its task runs). */
  drafting: boolean;
  /** Its comments still to do. */
  todo: number;
  /** It (or a draft of it) is on screen. */
  on: boolean;
  /** Where its comments go: `v8` for v7 (none for an extension's version). */
  into: string | null;
}

/** The version strip: the family's versions oldest first, a version's drafts folded into one entry; an extension's
 * kind of version (`v0`) first, read as `name` names it (`v0 · Script`). Empty with fewer than two entries (a single
 * version has no strip). */
export function versionStrip(
  family: readonly { label: string; kind?: string }[],
  shown: string | null,
  todo: ReadonlyMap<string, number>,
  name: (v: { label: string; kind?: string }) => string = (v) => v.label,
): VersionEntry[] {
  const out: VersionEntry[] = [];
  const on = shown ? versionOf(shown) : null;
  const ordered = [...family.filter((v) => v.kind), ...family.filter((v) => !v.kind)];
  for (const v of ordered) {
    const { label } = v;
    if (v.kind) {
      out.push({
        version: label,
        name: name(v),
        kind: v.kind,
        label,
        drafting: false,
        todo: todo.get(label) ?? 0,
        on: label === shown,
        into: null,
      });
      continue;
    }
    const version = versionOf(label),
      draft = label !== version;
    const e = out.find((x) => x.version === version);
    if (!e)
      out.push({
        version,
        name: version,
        label,
        drafting: draft,
        todo: todo.get(version) ?? 0,
        on: version === on,
        into: intoVersion(version),
      });
    else Object.assign(e, { label, drafting: draft }); // the family lists a version's revisions oldest first: the newest wins
  }
  return out.length < 2 ? [] : out;
}

/** "Open on earlier versions": on the latest version (or a draft ahead of it), the older cuts with comments still to
 * do, newest first. Nothing anywhere else, or when nothing earlier is to do. */
export function earlierOpen(strip: readonly VersionEntry[]): VersionEntry[] {
  const i = strip.findIndex((e) => e.on);
  if (i < 0 || strip[i]!.kind || strip.slice(i + 1).some((e) => !e.drafting)) return [];
  return strip
    .slice(0, i)
    .filter((e) => e.todo > 0 && !e.kind) // an extension's version (v0's script) isn't an earlier cut
    .reverse();
}

/** The row under the playhead (from just before its start to just after its end; the latest start wins), or null. */
export function activeKey(rows: readonly Row[], vt: number): string | null {
  let best: Row | null = null;
  for (const r of rows) if (vt >= r.t0 - 0.3 && vt <= r.t1 + 0.7 && (!best || r.t0 >= best.t0)) best = r;
  return best?.key ?? null;
}

/** Phones get the list under the controls instead of a column (keep in step with app.css). */
export const PHONE_QUERY = '(max-width: 760px)';

/**
 * Whether the sidebar's panel and its toggle are on screen. Nothing at all unless the server has the notes routes and
 * there is (or was, on this page) something to show, so the review page looks exactly as before until then. Once
 * shown, the column stays while it is open, also on a version with nothing to show (no jump on a C flip). On desktop
 * the toggle only appears while the column is closed; on phones it always opens and closes the list.
 */
export function presence(p: {
  available: boolean;
  content: boolean;
  everShown: boolean;
  open: boolean;
  phoneOpen: boolean;
  phone: boolean;
}): { panel: boolean; toggle: boolean } {
  const has = p.available && (p.content || p.everShown),
    open = p.phone ? p.phoneOpen : p.open;
  return { panel: has && open, toggle: has && (p.phone || !open) };
}

/** What an empty sidebar says. */
export const emptyText = (res: NotesResponse | null): string =>
  !res ? '' : res.newest ? 'No open questions' : `No notes on ${res.version}`;

/** The sidebar's heading: "Questions" on the newest version, else "Notes on v7". */
export const heading = (res: NotesResponse | null): string =>
  !res ? '' : res.newest ? 'Questions' : `Notes on ${res.version}`;

/** The collapsed toggle's label: what's waiting in the sidebar. */
export function toggleLabel(rows: readonly Row[], newest: boolean): string {
  const q = rows.filter((r) => r.answer != null || r.ask).length;
  if (newest && q) return q === 1 ? '1 question' : `${q} questions`;
  return rows.length === 1 ? '1 note' : `${rows.length} notes`;
}
