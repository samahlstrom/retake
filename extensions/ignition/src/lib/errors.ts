// What a failed ctx call means. The page's ctx rejects with an Error that may carry `status` (the HTTP status), the
// files API's `{changed, sha256}`, and a transcription job's `hint`; anything else falls back to its message.

export const OFFLINE = 'The editing computer is offline';

type Bag = Record<string, unknown>;
const bag = (e: unknown): Bag => (e && typeof e === 'object' ? (e as Bag) : {});

export function messageOf(e: unknown): string {
  if (typeof e === 'string') return e;
  const b = bag(e);
  for (const k of ['message', 'error']) if (typeof b[k] === 'string' && b[k]) return b[k] as string;
  return 'Something went wrong';
}

export function statusOf(e: unknown): number {
  const b = bag(e);
  if (typeof b.status === 'number') return b.status;
  const r = bag(b.response);
  if (typeof r.status === 'number') return r.status;
  const m = /\b([45]\d\d)\b/.exec(messageOf(e));
  return m ? Number(m[1]) : 0;
}

/** The file changed since it was read (409 `{"error": "changed", "sha256"}`). */
export const isChanged = (e: unknown): boolean =>
  bag(e).changed === true || bag(e).error === 'changed' || statusOf(e) === 409;

/** The file's sha256 now, from a 409. */
export const shaOf = (e: unknown): string => (typeof bag(e).sha256 === 'string' ? (bag(e).sha256 as string) : '');

export const isMissing = (e: unknown): boolean =>
  statusOf(e) === 404 || /\bnot found\b|no such file/i.test(messageOf(e));

/** Hosted, and the editing computer isn't connected (503 `{"error": "editing machine is offline"}`). */
export const isOffline = (e: unknown): boolean => statusOf(e) === 503 || /\boffline\b/i.test(messageOf(e));

/** A failed transcription's one-line fix (the job's `hint`), else its error. */
export function hintOf(e: unknown): string {
  const b = bag(e);
  for (const v of [b.hint, bag(b.job).hint]) if (typeof v === 'string' && v) return v;
  return messageOf(e);
}

/** One line for a failure, the offline case in the page's words. */
export const sayError = (e: unknown): string => (isOffline(e) ? OFFLINE : messageOf(e));
