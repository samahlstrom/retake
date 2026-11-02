// An outside edit (spec/ignition.md, "Outside edits"): the watch says a document changed on disk. With no unsaved
// typing it reloads in place; with unsaved typing it asks ("Changed outside Retake · Reload · Keep mine").

export type Decision =
  /** Nothing to do: it's the version we have (our own save coming back, or a stale event). */
  | 'ignore'
  /** Read it again and show it, keeping the caret's line. */
  | 'reload'
  /** Ask: your typing and theirs. */
  | 'conflict'
  /** Wait: a save is on its way, and its answer says which version we have. */
  | 'later';

export interface Change {
  sha256: string;
  deleted?: boolean;
}

export interface Have {
  /** The sha256 we read or last wrote; "" when the file didn't exist. */
  sha: string;
  /** There's typing not saved yet. */
  dirty: boolean;
  /** A save is on its way. */
  saving: boolean;
}

export function decide(c: Change, have: Have): Decision {
  if (have.saving) return 'later';
  const now = c.deleted ? '' : c.sha256;
  if (now === have.sha) return 'ignore';
  return have.dirty ? 'conflict' : 'reload';
}
