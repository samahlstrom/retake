// Job progress from the `job` events of GET /api/events, shown as one quiet line in the sidebar: "Transcribing your
// take…" while a job runs, or a clear error (with the server's hint) when one fails. Pure, so it is unit-tested.
import type { Job } from './types';

const RUNNING: Record<string, string> = {
  transcribe: 'Transcribing your take…',
  notes: 'Writing up your notes…',
};
const FAILED: Record<string, string> = {
  transcribe: 'Transcription failed',
  notes: "Couldn't write up the notes",
};

/** The jobs still worth showing, after event `ev`: running or queued ones, and the latest failure of each kind until
 * a job of that kind succeeds. */
export function applyJob(list: readonly Job[], ev: Job): Job[] {
  const rest = list.filter((j) => j.id !== ev.id);
  if (ev.state === 'done') return rest.filter((j) => !(j.kind === ev.kind && j.state === 'failed'));
  if (ev.state === 'failed') return [...rest.filter((j) => !(j.kind === ev.kind && j.state === 'failed')), ev];
  return [...rest, ev];
}

export interface JobLine {
  text: string;
  error: boolean;
  hint: string | null;
}

/** The one line to show, or null: a failure first (it needs the reviewer), else what is running, else queued. */
export function jobLine(list: readonly Job[]): JobLine | null {
  const failed = list.filter((j) => j.state === 'failed').at(-1);
  if (failed)
    return {
      text: failed.error || FAILED[failed.kind] || `${failed.kind} failed`,
      error: true,
      hint: failed.hint || null,
    };
  const busy = list.find((j) => j.state === 'running') ?? list.find((j) => j.state === 'queued');
  if (!busy) return null;
  return { text: RUNNING[busy.kind] ?? `Working: ${busy.kind}…`, error: false, hint: null };
}
