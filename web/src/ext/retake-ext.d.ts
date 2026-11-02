// The page API of Retake extensions, `retake-ext/1` (spec/extensions.md, "The page API"): the types of `ctx` as they
// ship to extension authors (`retake ext init` writes this file next to the module). web/src/ext/ctx.ts implements
// it and takes its types from here, so the two can't drift.

/** Where a module mounts. */
export type SlotName = 'page' | 'version' | 'sidebar-tab' | 'review-panel';

/** Who is looking (spec/team.md). */
export type Role = 'viewer' | 'reviewer' | 'approver' | 'owner';

/** What `ctx.on` listens to: the page's own events. `files` is `ext.files` for this extension, while a watch holds.
 * `reveal` is `{anchor}`: the person clicked a comment's or a question's place in the comments column (the version slot
 * shows it). */
export type EventName = 'notes' | 'job' | 'version' | 'automation' | 'files' | 'reveal';

/** One function per slot the module fills. It gets an empty element and `ctx`; the function it returns unmounts. */
export type Mount = (el: HTMLElement, ctx: Ctx) => void | (() => void);

/** What the module exports: `page`, `version`, `sidebarTab`, `reviewPanel`. */
export interface Module {
  page?: Mount;
  version?: Mount;
  sidebarTab?: Mount;
  reviewPanel?: Mount;
}

export interface Ctx {
  api: 1;
  slot: SlotName;
  /** `machine` is false when the video's editing computer lacks this extension (hosted). */
  ext: { id: string; name: string; version: string; base: string; machine: boolean };
  role: Role;
  /** The page slot: the hash after `#ext/<id>/`; '' elsewhere. */
  readonly route: string;
  onRoute(cb: (route: string) => void): () => void;
  /** Under a video. */
  video?: VideoRef;
  /** Under a video: the version on screen. */
  version?: { label: string; kind?: string };
  /** GET api/home: the videos and the folder tree, read-only. */
  home(): Promise<Home>;
  /** Its owned files in a video's folder (default: this one). */
  files(video?: string): Files;
  /** Comments on a video's versions (default: this one). */
  comments(video?: string): Comments;
  /** Makes `<project>/<name>/` with the given owned files (owner). */
  createVideo(v: {
    project: string;
    name: string;
    files: Record<string, string>;
  }): Promise<{ name: string; url: string }>;
  /** Transcribes an owned audio file and waits for the words. Rejects with the job's hint when it fails. */
  transcribe(path: string, video?: string): Promise<{ text: string; words: Word[] }>;
  /** The audio of R (or Record) on this extension's version, when it stops. */
  onRecorded(cb: (a: Recorded) => void): () => void;
  /** Core is recording now (R or Record). */
  recording(): boolean;
  onRecording(cb: (on: boolean) => void): () => void;
  /** The version slot: where a comment typed in the comments column points (the heading above the caret), asked when
   * one is added. Another call replaces it; the function it returns (or unmounting) ends it. */
  onAnchor(fn: () => Anchor | null): () => void;
  /** Scrolls the comments column to a comment (by its note id, or a question's thread id) and selects it. */
  selectComment(id: number): void;
  /** sidebar-tab and review-panel on a video version. */
  player?: Player;
  on(event: EventName, cb: (data: unknown) => void): () => void;
  /** Another video (`/p/<name>/?v=&t=`), another version or time of this one, or a route of its page. */
  navigate(to: { video?: string; version?: string; t?: number; route?: string }): void;
  toast(text: string): void;
  /** Its server: ext/<id>/api/<path>. */
  fetch(path: string, init?: RequestInit): Promise<Response>;
  /** Under a video: what a public web page says about itself (GET api/link-preview), for a link card. Rejects with
   * status 400 for an address that isn't a public web page, 502 for a page that can't be read. */
  linkPreview(url: string): Promise<LinkPreview>;
}

/** A web page's title, site and picture (an https address), as `ctx.linkPreview` reads them; '' when it doesn't say. */
export interface LinkPreview {
  url: string;
  title: string;
  site: string;
  image: string;
  description: string;
}

export interface VideoRef {
  name: string;
  title: string;
  path: string;
  project: string;
}

export interface FileInfo {
  path: string;
  size: number;
  /** Unix milliseconds. */
  modified: number;
  sha256: string;
  /** In an `ext.files` event: it's gone. */
  deleted?: boolean;
}

/** An agent's edit of an owned file (newest first in `history`). */
export interface Edit {
  id: string;
  path: string;
  at: number;
  task?: string;
  /** The sha256 before and after ('' when the file wasn't there). */
  before: string;
  after: string;
}

export interface Files {
  /** Its owned files, sorted by path. */
  list(glob?: string): Promise<FileInfo[]>;
  read(path: string): Promise<{ sha256: string; text(): Promise<string>; blob(): Promise<Blob> }>;
  /** `ifMatch`: the sha256 it was read at, or '*' for a new file. Rejects with `changed` and the sha256 now. */
  write(path: string, data: string | Blob, o?: { ifMatch?: string }): Promise<FileInfo>;
  remove(path: string, o?: { ifMatch?: string }): Promise<void>;
  /** For <img> and <audio>. */
  url(path: string): string;
  /** Changes made outside the page (editors, the agent), every 2 s while watched. */
  watch(cb: (changed: FileInfo[]) => void): () => void;
  /** The agent's edits, newest first ('' or none: every file). */
  history(path?: string): Promise<Edit[]>;
  /** Puts back what an edit replaced, while the file is still as that edit left it. */
  undo(id: string, path: string): Promise<FileInfo>;
}

/** What a failed call rejects with. */
export interface ExtError extends Error {
  status: number;
  /** A write or undo that lost a race: the file's sha256 now. */
  changed?: boolean;
  sha256?: string;
  /** A failed job's one-line fix. */
  hint?: string;
}

export interface Comments {
  /** api/notes items (spec/notes.md); without a version, the whole video's. */
  list(version?: string): Promise<Item[]>;
  /** A typed comment on this extension's version. */
  add(c: { text: string; anchor?: Anchor; wants?: 'version' }): Promise<Item>;
  /** Sends notes to the agent (POST api/automation/tasks); none given: the open ones nobody sent yet. */
  send(ids?: number[]): Promise<void>;
  /** Answers the agent's question (its thread id) with words. */
  answer(question: number, text: string): Promise<void>;
}

export interface Anchor {
  file: string;
  /** From 1. */
  line: number;
  heading?: string;
  quote?: string;
}

/** One api/notes item. */
export interface Item {
  id: number;
  kind: 'note' | 'question';
  status: string;
  key?: string;
  version?: string;
  t0: number;
  t1?: number | null;
  text: string;
  by?: string;
  change?: string | null;
  anchor?: Anchor | null;
  wants?: string;
  /** The task that owns it. */
  work?: { id: string; state: string; [k: string]: unknown };
  /** The agent's open question about it, from inside its task. */
  ask?: {
    id: number;
    task: string;
    text: string;
    anchor?: Anchor | null;
    options?: string[] | null;
    visuals?: { options?: { text: string }[] | null } | null;
    [k: string]: unknown;
  } | null;
  [k: string]: unknown;
}

export interface Word {
  w: string;
  /** Start and end, in seconds. */
  s: number;
  e: number;
  /** Confidence, 0 to 1. */
  p?: number;
}

export interface Recorded {
  blob: Blob;
  type: string;
  /** Seconds. */
  duration: number;
  /** When it started: local ISO 8601 with the offset. */
  started: string;
}

export interface Player {
  time(): number;
  duration(): number;
  /** Ignored while recording. */
  seek(t: number): void;
  onTime(cb: (t: number) => void): () => void;
}

export interface HomeVideo {
  name: string;
  title: string;
  video: string;
  path: string;
  project: string;
  url: string;
  version: string;
  versions: number;
  status: string;
  /** A video whose only version is an extension's kind (`status: "no_cut"`). */
  kind?: string;
  kinds?: string[];
  /** Unix milliseconds of its newest owned-file change or version. */
  changed?: number;
  [k: string]: unknown;
}

export interface HomeFolder {
  title: string;
  path: string;
  project: boolean;
  videos: { name: string }[];
  children: HomeFolder[];
}

export interface Home {
  title: string;
  role: string;
  projects: HomeVideo[];
  tree: HomeFolder;
  [k: string]: unknown;
}
