// Shapes of the server's responses (spec/api.md) and of the page's in-memory take (spec/take-format.md).
import type { PenColor } from './constants';

export type Point = [number, number];
/** [x, y, w, h], normalized 0-1 to the video frame. */
export type Box = [number, number, number, number];

/** One version of the family, as /api/info lists it. */
export interface Version {
  label: string;
  n: number;
  path: string;
  name: string;
  current: boolean;
  duration: number | null;
  fps: number;
  width: number | null;
  height: number | null;
}

/** A stroke as /api/history (and the timeline) needs it. */
export interface HistoryStroke {
  video_t: number;
  color: string;
  points: Point[];
  undone?: boolean;
}

/** An image of an earlier take, as /api/history lists it (take format v1.1): shown like a stroke near its time. */
export interface HistoryImage {
  url: string;
  video_t: number;
  box: Box;
  undone?: boolean;
}

export interface HistoryEntry {
  id: string;
  inherited_from: string | null;
  voice: { bin?: number; bins?: [number, number][] };
  strokes: HistoryStroke[];
  images?: HistoryImage[];
}

/** GET /api/info: the current version plus the family, sessions and history. Fields are optional because the page
 * starts from a placeholder until the server answers. */
export interface Info {
  name: string;
  path: string;
  duration: number | null;
  fps: number;
  history: HistoryEntry[];
  label?: string;
  n?: number;
  current?: boolean;
  width?: number | null;
  height?: number | null;
  versions?: Version[];
  stem?: string;
  review_dir?: string;
  sessions?: unknown[];
  /** Who the page acts as (spec/team.md). A server without team mode sends none: the owner. */
  role?: Role;
}

/** A page's role: the owner (local), or a share link's. */
export type Role = 'owner' | 'viewer' | 'reviewer' | 'approver' | 'agent';

/** An event in the take's log. `p` is the page clock (seconds); serialize turns it into session_t. */
export interface TakeEvent {
  p: number;
  video_t: number;
  event: string;
  [extra: string]: unknown;
}

/** A pen stroke drawn in this take. */
export interface Stroke extends HistoryStroke {
  id: number;
  seg: string;
  version: string | null;
  flip: boolean;
  color: PenColor;
  p0: number;
  p1?: number;
  video_t_end?: number;
  paused: boolean;
}

/** An image dropped or pasted onto the video while recording (take format v1.1). */
export interface TakeImage extends HistoryImage {
  /** From the take's stroke counter, so Z undoes whichever came last. */
  id: number;
  seg: string;
  version: string | null;
  flip: boolean;
  /** img_NN.<ext> in the take folder. */
  file: string;
  mime: string;
  /** Page clock when it was added. */
  p0: number;
  blob: Blob;
  /** pending: not on the server yet; done: the server has it; dropped: undone before it was sent; refused: the
   * server has no image route (an older server). */
  upload: 'pending' | 'done' | 'dropped' | 'refused';
  /** Settles once the image is in the local buffer (IndexedDB). */
  stored: Promise<void>;
}

/** One R-on/R-off recording segment and its audio file. */
export interface Segment {
  n: number;
  id: string;
  /** The question thread this segment answers (Answer in the sidebar). */
  answer_to?: number;
  rec: MediaRecorder;
  mime: string;
  version: string | null;
  flip: boolean;
  file: string;
  /** Recorded chunks, in order (kept in memory for the upload). */
  chunks: Blob[];
  /** Bytes the server has confirmed. */
  uploaded: number;
  p0: number;
  p1: number | null;
  v0: number;
  v1: number | null;
  vmin: number;
  vmax: number;
  stopped?: Promise<unknown>;
  /** Bytes recorded so far (the next chunk's offset). */
  size: number;
  /** Bytes written to the local audio buffer (IndexedDB, or memory as a fallback). Only these are uploaded. */
  stored: number;
  /** Settles once every chunk recorded so far is in the buffer. */
  persisted: Promise<void>;
}

/** One take: everything between the first R and Done (or a version step). */
export interface Take {
  id: string | null;
  version: string | null;
  created: string | null;
  t0: number | null;
  events: TakeEvent[];
  strokes: Stroke[];
  images: TakeImage[];
  segments: Segment[];
  nextId: number;
  dirty: boolean;
  lastSave: number;
  saving: Promise<void> | null;
  saveAgain: boolean;
  saveErr: string | null;
  closed: boolean;
  bins: Float32Array;
}

/** The POST /data body. Key order matters: it matches the legacy player byte for byte. */
export interface TakeData {
  session: {
    format: 3;
    version: string | null;
    session: string | null;
    created: string | null;
    saved: string;
    clock: string;
    recorded: number;
    video: { duration: number; width: number; height: number };
    display: { show_within: number };
    closed?: true;
  };
  segments: {
    id: string;
    file: string;
    mime: string;
    version: string | null;
    flip: boolean;
    t_start: number;
    t_end: number;
    recording: boolean;
    video_start: number;
    video_end: number;
    video_min: number;
    video_max: number;
    answer_to?: number;
  }[];
  events: ({ session_t: number; video_t: number; event: string } & Record<string, unknown>)[];
  strokes: {
    id: number;
    seg: string;
    version: string | null;
    flip: boolean;
    color: string;
    session_t: number;
    session_t_end: number;
    video_t: number;
    video_t_end: number;
    paused: boolean;
    points: Point[];
  }[];
  voice: { bin: number; bins: [number, number][] };
  /** Take format v1.1: sent once the take has had an image (so [] clears an undone one). */
  images?: {
    id: number;
    seg: string;
    version: string | null;
    flip: boolean;
    file: string;
    mime: string;
    session_t: number;
    video_t: number;
    box: Box;
  }[];
}
