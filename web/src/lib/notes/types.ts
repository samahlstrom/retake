// Shapes of the notes routes (spec/notes.md, "What the sidebar shows" and "HTTP additions").
import type { Box } from '../types';

export type NoteStatus = 'open' | 'question' | 'answered' | 'accepted' | 'retracted';

/** One sidebar item from GET /api/notes. For a question, `id` is the thread id (what `answer_to` takes). */
export interface NoteItem {
  id: number;
  kind: 'note' | 'question';
  status: NoteStatus | string;
  t0: number;
  t1?: number | null;
  text: string;
  by?: string;
  /** The one-line change of an answered note. */
  change?: string | null;
  /** The version that answered it. */
  answered_in?: string | null;
  /** The question an asked note became. */
  question?: string | null;
  /** Version labels to flip between. */
  options?: string[] | null;
  images?: { url: string; box: Box }[] | null;
  /** For a question: the note it's about. */
  note?: number | null;
}

export interface NotesResponse {
  version: string;
  newest: boolean;
  items: NoteItem[];
}

export type JobState = 'queued' | 'running' | 'done' | 'failed';

/** A `job` event from GET /api/events. */
export interface Job {
  id: number | string;
  kind: string;
  state: JobState | string;
  error?: string | null;
  /** A failed job's one-line fix, from the server (such as how to set up transcription). */
  hint?: string | null;
}
