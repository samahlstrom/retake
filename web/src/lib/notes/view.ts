// What the sidebar shows, from a GET /api/notes response. The server already picks the items (spec/notes.md); this
// applies the display rules on top, and is pure so it is unit-tested:
//  - newest version: the open questions and this session's notes, nothing answered;
//  - older version: the notes in their final state (✓ with the change, ? with the question);
//  - retracted notes are never shown.
import { fmt } from '../util';
import type { Box } from '../types';
import type { NoteItem, NotesResponse } from './types';

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
  key: string;
  id: number;
  kind: 'note' | 'question';
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
}

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

/** Whether the sidebar shows this item at all. */
export function visible(item: NoteItem, newest: boolean): boolean {
  if (item.status === 'retracted') return false;
  if (newest && ANSWERED.has(item.status)) return false;
  return true;
}

export function markOf(item: NoteItem): Mark {
  if (ANSWERED.has(item.status)) return 'answered';
  if (item.kind === 'question' || item.status === 'question') return 'question';
  return 'open';
}

export function toRow(item: NoteItem, newest: boolean, ctx: ViewContext): Row {
  const mark = markOf(item),
    known = (l: string) => ctx.versions.some((v) => v.label === l);
  const answeredIn = mark === 'answered' ? item.answered_in || null : null;
  const t1 = typeof item.t1 === 'number' && item.t1 >= item.t0 ? item.t1 : item.t0;
  return {
    key: `${item.kind}:${item.id}`,
    id: item.id,
    kind: item.kind,
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
  };
}

/** The rows of the sidebar, in time order. */
export function sidebarRows(res: NotesResponse | null, ctx: ViewContext): Row[] {
  if (!res) return [];
  return res.items
    .filter((it) => visible(it, res.newest))
    .map((it) => toRow(it, res.newest, ctx))
    .sort((a, b) => a.t0 - b.t0 || (a.kind === b.kind ? a.id - b.id : a.kind === 'note' ? -1 : 1));
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
  const q = rows.filter((r) => r.answer != null).length;
  if (newest && q) return q === 1 ? '1 question' : `${q} questions`;
  return rows.length === 1 ? '1 note' : `${rows.length} notes`;
}
