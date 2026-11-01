// Shapes of the notes routes (spec/notes.md, "What the sidebar shows" and "HTTP additions").
import type { Box } from '../types';
import type { EditingTask } from '../automation.svelte';

export type NoteStatus = 'open' | 'question' | 'answered' | 'accepted' | 'retracted';

/** One sidebar item from GET /api/notes. For a question, `id` is the thread id (what `answer_to` takes). */
export interface NoteItem {
  work?: EditingTask;
  version?: string;
  take?: string;
  seg?: string;
  drawings?: string[];
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
  /** A note's still of its moment with its strokes drawn on it: a URL (spec/notes.md, "What the sidebar shows"). */
  snapshot?: string | null;
  /** For a question: the note it's about. */
  note?: number | null;
  /** A note of the parent version shown on the newest one at its mapped time (spec/bridge.md, "Carrying notes over"):
   * `as` says what became of its moment, `t0`/`t1` are its times on `from`. */
  carried?: Carried | null;
  /** The timeline elements under the note, best first (versions rendered from a timeline). */
  targets?: { id: string; kind: string; score: number }[] | null;
  /** The open question its task's agent asked about it from inside the task (`retake ask`), shown under it. */
  ask?: TaskAsk | null;
}

/** What an agent drew to ask (spec/automation.md, "Questions from inside a task"), normalized 0–1 to the frame. */
export interface AgentVisuals {
  boxes?: { box: Box; label?: string }[] | null;
  arrows?: { from: [number, number]; to: [number, number]; label?: string }[] | null;
  strokes?: { points: [number, number][] }[] | null;
  /** With a box it is placed on the frame; without, it shows in the question's card. */
  images?: { url: string; box?: Box | null }[] | null;
  options?: { text: string; image?: { url: string } | null }[] | null;
  /** The still of the question's moment. */
  frame?: string | null;
}

/** A task's question under its comment: `id` is the thread (what an answer points at), `t`, `from` and `to` its
 * moment and shot on the shown version. */
export interface TaskAsk {
  id: number;
  task: string;
  t: number;
  from?: number | null;
  to?: number | null;
  version: string;
  text: string;
  visuals?: AgentVisuals | null;
}

export interface Carried {
  from: string;
  as: 'unchanged' | 'check' | 'changed' | string;
  t0: number;
  t1: number;
}

export interface NotesResponse {
  scope?: 'video' | 'review';
  jobs?: Job[];
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
