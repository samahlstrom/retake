// The review page's state and behavior: a port of the legacy player's script, function for function. Values the
// markup shows are runes ($state); the hot per-frame data (take, strokes, voice bins, waveform) stays plain, and the
// canvases are drawn every animation frame, as before.
import { memoryChunkStore, openChunkStore, recoverPending, type ChunkStore } from './audio-buffer';
import { Automation } from './automation.svelte';
import {
  AUTOSAVE_MS,
  MSG_SAVE_FAILED,
  PING_MS,
  SEEK,
  TICK_MS,
  VIEW_ONLY,
  type PenColor,
  COLOR_KEYS,
} from './constants';
import { memoryImageStore, openImageStore, recoverImages, type ImageStore } from './image-buffer';
import {
  dragMayHoldImage,
  droppedImageUrl,
  firstImageFile,
  HINT_IMAGE,
  imageExt,
  imageName,
  MSG_IMAGE_BAD,
  MSG_IMAGE_NOFILE,
  placeBox,
  refuseImage,
  resizeBox,
} from './images';
import { ERASE_RADIUS, strokesHit, sweep, undoTarget } from './eraser';
import {
  crumb,
  homeContext,
  nextToReview,
  NO_MORE,
  normalizeHome,
  projectUrl,
  unwatchUrl,
  videoName,
  viewerId,
  type Card,
} from './home';
import { backPlace, findNode, hashOf, rememberedPlace } from './tree';
import { Scrubber } from './scrub';
import {
  DEFAULT_SHORTCUTS,
  hintSeen,
  keyAction,
  markHintSeen,
  playerShortcuts,
  shortcutLabel,
  validateShortcuts,
  type Action,
  type PlayerShortcuts,
  type ShortcutAction,
} from './keys';
import { layoutMode, type LayoutMode } from './layout';
import { Viewport } from './viewport.svelte';
import { composeMode } from './viewport';
import { browserStorage } from './side-width';
import { closeMic, MIC_BLOCKED, MIC_BLOCKED_TOUCH, openMic, pickMime, recorderMime, type Mic } from './mic';
import { Notes } from './notes/notes.svelte';
import { NoteEdit } from './notes/edit.svelte';
import type { NoteItem } from './notes/types';
import {
  activeKey,
  earlierOpen,
  forVersion,
  withLive,
  historyLabels,
  intoVersion,
  PHONE_QUERY,
  presence,
  sidebarRows,
  todoByVersion,
  versionStrip,
  type Row,
  type VersionEntry,
} from './notes/view';
import { inRange } from './notes/agent';
import { addPoint, alphaAt, drawPen, fitFrame, isClick, renderInk } from './pen';
import { Saver } from './saver';
import { VideoSource } from './source';
import {
  addChunk,
  archive,
  binCount,
  grow,
  insertEv,
  newSegment,
  newTake,
  serialize as serializeTake,
  settleMime,
} from './take';
import { covers, drawTimeline, pendingStrokes, pendingVoiceBins, tlT, type VoiceTrack } from './timeline';
import {
  activeTask,
  agentStatus,
  commentStatus,
  liveTarget,
  managesAgent,
  onItLine,
  pendingNotes,
  sendButton,
  unsent,
  type AgentStatus,
  type CommentStatus,
  type SendButton,
} from './status';
import type { EditingTask } from './automation.svelte';
import { Toast } from './toast.svelte';
import type {
  Box,
  HistoryEntry,
  HistoryImage,
  HistoryStroke,
  Info,
  Point,
  Role,
  Segment,
  Stroke,
  StreamState,
  Take,
  TakeData,
  TakeEvent,
  TakeImage,
  Version,
  VersionEvent,
} from './types';
import { mapTime, TimeMaps } from './timemap';
import {
  byLabel,
  catchUp,
  failedToast,
  flipRefusal,
  flipTarget,
  flipToast,
  incomplete,
  landing,
  latestPlayable,
  neighbor,
  newlyStreamed,
  renderedEnd,
  revisionOf,
  pageName,
  verName,
  withStream,
} from './versions';
import { Watcher } from './watch';
import { addVoice, drawWave, levelDb, newAcc, newVad, pushBars, vadStep, type WaveBar } from './voice';
import { palette } from './theme';
import { brandOf, reviewTitle } from './brand';
import { clamp, fmt, isoLocal, now, r3, stamp } from './util';
import { ExtLoader, type ExtInfo } from '../ext/loader';
import { makeCtx, type Host } from '../ext/ctx';
import type { Anchor, Player, Recorded, Role as ExtRole, SlotName } from '../ext/retake-ext';
import { addTyped } from './notes/typed';
import {
  kindChat,
  kindRecords,
  openingVersion,
  sidebarTabs,
  versionName,
  versionSlot,
  type ExtTab,
} from '../ext/kinds';
import { Chat, type ChatFiles } from './chat/chat.svelte';
import { sendState } from './chat/thread';
import { Downloads } from './download.svelte';

export interface Elements {
  stage: HTMLElement;
  video: HTMLVideoElement;
  freeze: HTMLCanvasElement;
  ink: HTMLCanvasElement;
  tl: HTMLCanvasElement;
  wave: HTMLCanvasElement;
  /** The comment box at the bottom of the comments (spec/notes.md, "Typed comments on a video"). */
  comment?: HTMLTextAreaElement | null;
  /** Record, whose ring follows your voice while recording. */
  rec?: HTMLElement | null;
  /** The chat's composer, on a kind's version with a chat (spec/chat.md). */
  chatBox?: HTMLTextAreaElement | null;
}

/** An image card on the video this frame: a take's own image (resizable while recording) or an earlier take's. */
export interface ImageCard {
  key: string;
  url: string;
  box: Box;
  alpha: number;
  /** The image's id when its corner can be dragged (this take's, while recording), else null. */
  id: number | null;
}

/** What the sidebar shows (the test hook's `sidebar`). */
export interface SidebarView {
  /** The column (desktop) or the list under the controls (phone) is on screen. */
  panel: boolean;
  /** The open/close toggle is on screen. */
  toggle: boolean;
  phone: boolean;
  rows: Row[];
  /** The row under the playhead. */
  active: string | null;
}

/** The test hook (`window.__review`): the same shape as the legacy page's, see spec/api.md, plus `notes` and
 * `sidebar` (milestone 2). */
export interface ReviewHook {
  readonly take: Take;
  readonly seg: Segment | null;
  readonly view: { base: string | null; shown: string | null; flipped: boolean; switching: boolean };
  readonly info: Info;
  readonly hist: { strokes: HistoryStroke[]; bins: Float32Array; images: HistoryImage[] };
  readonly vad: { floor: number; hang: number; active: boolean };
  readonly bars: WaveBar[];
  readonly mic: Mic | null;
  readonly notes: Notes['snapshot'];
  readonly sidebar: SidebarView;
  /** The page is a project of a home (spec/home.md), and the next project to review. */
  readonly home: { available: boolean; next: string | null };
  /** The typed comment being written (spec/notes.md, "Typed comments on a video"): its version, time, how many lines
   * and its words; null when there is none. */
  readonly draft: { version: string; t: number; lines: number; text: string } | null;
  /** Typing on a phone (spec/notes.md): the visual viewport as the page lays itself out from it, which field has the
   * focus, and compose mode. */
  readonly viewport: {
    top: number;
    kb: number;
    height: number;
    up: boolean;
    typing: string | null;
    compose: boolean;
  };
  serialize(t: Take): TakeData;
  save(t?: Take): Promise<void> | undefined;
}

/** How often a page whose video's computer is offline looks again. */
const OFFLINE_RETRY_MS = 5000;

/** The page's role as an extension's ctx names it: the owner when the server has no roles (local). */
const extRole = (r: Role | null): ExtRole =>
  r === 'reviewer' || r === 'approver' || r === 'viewer' ? r : r === 'agent' ? 'viewer' : 'owner';

/** Where typing goes, so the player's keys don't: text fields (any editable element) and open menus or dialogs. */
const TEXT = 'input, textarea, select, [contenteditable]:not([contenteditable=false])';
const TYPING = `${TEXT}, [role=combobox], [role=listbox], [role=option], [role=menu], [role=menuitem], [role=menuitemradio], [role=dialog]`;
/** On an extension's version there is no video: these are the keys core still handles. */
const KIND_KEYS = new Set<Action>(['toggleRec', 'escape', 'notes', 'help', 'next', 'save']);

/** The review page's deep link (ctx.navigate from another page): `?v=<label>&t=<seconds>`. */
function deepLink(): { v: string | null; t: number } {
  if (typeof location === 'undefined') return { v: null, t: 0 };
  const q = new URLSearchParams(location.search);
  const t = Number(q.get('t'));
  return { v: q.get('v') || null, t: isFinite(t) && t > 0 ? t : 0 };
}

const FATAL = (name: string) =>
  `This file won't play in the browser. Make an H.264 copy first: ffmpeg -i "${name}" -c:v libx264 -crf 18 -c:a aac review.mp4`;

export class Review {
  // ---------------------------------------------------------------- what the markup shows
  paused = $state(true);
  /** The media rate is a browser preference, kept when this page changes source. */
  playbackRate = $state(1);
  shortcuts = $state<PlayerShortcuts>(playerShortcuts());
  shortcutError = $state('');
  curText = $state('0:00.0');
  durText = $state('0:00');
  timelineDuration = $state(0);
  /** The recording segment in progress. */
  seg = $state.raw<Segment | null>(null);
  starting = $state(false);
  recElapsed = $state('0:00');
  penColor = $state<PenColor>('red');
  /** The eraser is the tool (E, or its button): a swipe over a line removes it. A pen color brings the pen back. */
  eraser = $state(false);
  versions = $state.raw<Version[]>([]);
  base = $state<string | null>(null);
  shown = $state<string | null>(null);
  flipped = $state(false);
  verOpen = $state(false);
  /** The hint is hidden until the page knows whether to show it (start()), so a returning reviewer never sees it
   * flash. */
  hintGone = $state(true);
  doneSaved = $state(false);
  fatal = $state('');
  /** The version on screen plays from the editing computer, which is offline (spec/automation.md, "Offline"): the
   * stage shows the poster and says so, and the page looks again every 5 s. */
  offline = $state(false);
  #offlineTimer: ReturnType<typeof setTimeout> | null = null;
  freezeShown = $state(false);
  tip = $state<{ show: boolean; left: number | null; text: string }>({ show: false, left: null, text: '' });
  hoverRows = $state.raw<Row[]>([]);
  commentSearch = $state('');
  /** The sidebar's two tabs: what's still to do (Open, Working, Question) and what's Done. */
  commentFilter = $state<'todo' | 'done'>('todo');
  /** The keys sheet (? or its button). */
  keysOpen = $state(false);
  /** Send's list of agents for one send (held Send, or a right-click), when the computer offers more than one. */
  pickerOpen = $state(false);
  /** The pen's colours and eraser. */
  penOpen = $state(false);
  /** The last finish also sent the take to the agent (the button then says Sent). */
  sent = $state(false);
  /** Send is at work: finishing the take, submitting the other comments, reading back what's on its way. */
  sending = $state(false);
  /** A clock for the agent's online state (a worker that stops polling goes offline without a new answer). */
  clock = $state(Date.now() / 1000);
  followLatest = true;
  finishing = $state(false);
  #historyRequest = 0;
  readonly toast = new Toast();
  /** The shown video frame inside the stage (the pen canvas's box), for the image layer. */
  frame = $state({ left: 0, top: 0, width: 0, height: 0 });
  /** Image cards on the video this frame. */
  cards = $state.raw<ImageCard[]>([]);
  /** Who the page acts as (spec/team.md), from /api/info; null until it answers, or from a server without roles. */
  role = $state<Role | null>(null);
  /** The version menu's Download entry (spec/api.md, "Download"): whether this page may, and the files it asked for. */
  mayDownload = $state(false);
  readonly downloads = new Downloads();
  /** A viewer's share link only watches: no Record, no pen, no Answer. */
  get canRecord(): boolean {
    return this.role !== 'viewer' && this.role !== 'agent';
  }
  /** Approve and Reopen: an approver's link, or the owner. */
  get canApprove(): boolean {
    return this.role === null || this.role === 'owner' || this.role === 'approver';
  }
  /** The comments: the whole family's (`scope=video`), each shown on its own version (spec/notes.md, "Comments belong
   * to their version"). */
  readonly notes = new Notes({
    continuous: true,
    onVersion: (e) => this.onVersionEvent(e),
    onAutomation: () => {
      void this.automation.refresh();
    },
    onLoad: (label) => {
      void this.loadHistory(label);
    },
  });
  readonly automation = new Automation();
  /** A comment's words being edited (spec/notes.md, "Editing a comment"): in its card, or in the comment box on a
   * phone. */
  readonly edit = new NoteEdit({
    onSaved: () => this.notes.refresh(),
    onError: (why) => this.toast.say(why, 5000),
  });
  /** ⋯ → Edit, or a double-click (a tap on a phone) on a comment's words: its words become a field in its card; on a
   * phone the comment box is the field (compose mode) and the video shows the comment's moment. Called in the tap
   * itself, so iOS raises the keyboard for the box. */
  editComment = (row: Row): void => {
    if (!this.touch) {
      this.edit.start(row);
      return;
    }
    this.edit.start(row, true);
    this.el.comment?.focus();
    this.jumpToComment(row);
  };
  /** Time maps between versions: switching versions lands on the matching moment (spec/bridge.md). */
  readonly maps = new TimeMaps();
  readonly rows = $derived(
    sidebarRows(this.notes.res, {
      versions: this.versions,
      shown: this.shown,
      record: this.canRecord,
      approve: this.canApprove,
    }),
  );
  /** The rows of the version on screen: the list, the markers, the counts and Send are about these only. During a
   * switch it's the version the notes answer is for, until the new one's is in (its times are on that clock). */
  readonly versionRows = $derived.by(() => {
    const label = this.notes.res?.version ?? this.shown;
    const own = forVersion(this.rows, label);
    // The live version also lists the comments being worked into it (spec/notes.md, "What a version shows").
    const t = (this.automation.state?.tasks ?? []).find((x) => x.state === 'running' || x.state === 'stopping');
    if (!t?.target || !label || verName(label) !== t.target) return own;
    return withLive(
      this.rows,
      own,
      t.notes.filter((id) => !t.dropped?.includes(id)),
    );
  });
  /** The sidebar row under the playhead. */
  historyVoice = $state.raw<VoiceTrack[]>([]);
  transcribedTakes = $state.raw<string[]>([]);
  transcribedClips = $state.raw<string[]>([]);
  readonly pendingHistoryVoice = $derived(pendingVoiceBins(this.historyVoice, this.rows));
  /** One marker per comment on the timeline (a drawing-only comment too); its pen ticks fold into it. */
  readonly timelineComments = $derived(this.sidebar.rows);
  /** The agent's questions from inside its tasks: a marker each, at the question's own moment. */
  readonly timelineAsks = $derived(this.sidebar.rows.filter((row) => row.ask));
  /** The question whose visuals stay on the video (its marker or time was clicked), by thread id. */
  selectedAsk = $state<number | null>(null);
  /** The questions whose visuals are on the video now; `pulse` counts entries into their range, for one highlight. */
  shownAsks = $state.raw<{ row: Row; pulse: number }[]>([]);
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #askIn = new Map<number, number>();
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #askPulses = new Map<number, number>();
  #askSig = '';
  /** The task that owns each note, freshest first: the automation state, else the one the notes route carried. */
  readonly tasks = $derived.by(() => {
    const all = this.automation.state?.tasks ?? [];
    const live: Record<string, EditingTask> = Object.fromEntries(all.map((t) => [t.id, t]));
    const out: Record<string, EditingTask> = {};
    for (const row of this.rows) {
      if (row.work) out[row.key] = live[row.work.id] ?? row.work;
      else if (row.kind === 'note') {
        // Queued since the notes were read (a sent take, just processed): the automation state knows first.
        const t = all.findLast((t) => !!activeTask(t) && pendingNotes(t).includes(row.id));
        if (t) out[row.key] = t;
      }
    }
    return out;
  });
  /** Takes sent and still being processed (the server's `sending`): their notes are on their way to the agent. */
  readonly sendingTakes = $derived(this.automation.state?.sending ?? []);
  /** The note's take was sent and is still being processed. */
  onItsWay(row: Row): boolean {
    return (
      !!row.take && this.sendingTakes.some((t) => t.take === row.take && (!row.version || t.version === row.version))
    );
  }
  readonly statuses: Record<string, CommentStatus> = $derived(
    Object.fromEntries(
      this.rows.map((row) => [
        row.key,
        commentStatus(row, this.tasks[row.key], { sending: this.onItsWay(row), agent: this.agent }),
      ]),
    ),
  );
  readonly todoCount = $derived(this.versionRows.filter((row) => this.statusOf(row).state !== 'done').length);
  readonly doneCount = $derived(this.versionRows.length - this.todoCount);
  /** The version strip over the comments: every version with its To do count (empty for a single version). */
  readonly versionStrip: VersionEntry[] = $derived(
    versionStrip(
      this.versions,
      this.shown,
      todoByVersion(this.rows, (row) => this.statusOf(row).state !== 'done', this.shown),
      (v) => versionName(this.exts, v),
    ),
  );
  /** "Open on earlier versions: v6 · 2, v3 · 91", on the latest version. */
  readonly earlierOpen: VersionEntry[] = $derived(earlierOpen(this.versionStrip));
  /** This version's open notes nobody has sent to the agent yet. */
  readonly unsentRows = $derived(
    this.versionRows.filter((row) => unsent(row, this.tasks[row.key], this.onItsWay(row))),
  );
  /** More than one person commented: then (only then) cards say who. */
  readonly manyAuthors = $derived.by(() => {
    const notes = this.rows.filter((row) => row.kind === 'note');
    return notes.some((row) => row.by !== notes[0]!.by);
  });
  readonly agent: AgentStatus = $derived(agentStatus(this.automation.state, this.clock));
  /** The version a running task of this video makes or updates, live (spec/automation.md, "The live version"). */
  readonly liveTarget = $derived(liveTarget(this.automation.state?.tasks));
  /** A finished batch stays on its source until this quiet, explicit action is chosen. */
  readonly viewTarget = $derived.by(() => {
    const tasks = this.automation.state?.tasks ?? [];
    for (const task of tasks) {
      if (!task.target || verName(task.version) !== verName(this.shown)) continue;
      // A task update can beat the notes response. Never call a target ready against an empty or older list.
      if (this.notes.res?.version !== this.shown) continue;
      const render = this.automation.state?.renders.find((r) => r.label === task.target || r.task === task.id);
      if (!render?.complete) continue;
      const sourceVersion = verName(task.version);
      const related = tasks.filter((candidate) => verName(candidate.version) === sourceVersion);
      if (!related.length || related.some((candidate) => candidate.state !== 'completed')) continue;
      const source = this.rows.filter((row) => row.version && verName(row.version) === sourceVersion);
      if (source.some((row) => this.statusOf(row).state !== 'done')) continue;
      const required = related.flatMap((candidate) => candidate.notes.filter((id) => !candidate.dropped?.includes(id)));
      if (required.some((id) => !source.some((row) => row.id === id && this.statusOf(row).state === 'done'))) continue;
      if (source.some((row) => this.statusOf(row).state === 'question')) continue;
      return task.target;
    }
    return null;
  });
  /** "The agent is on it · 15 comments": the one quiet line at the top of To do until the agent marks a comment
   * (spec/automation.md, "While the agent works"). */
  readonly onIt = $derived(
    onItLine(
      this.versionRows.filter((row) => this.statusOf(row).state === 'working').map((row) => this.tasks[row.key]),
    ),
  );
  /** Runs the agent: chooses it and connects computers. Everyone who can record sends. */
  readonly owner = $derived(this.canRecord && managesAgent(this.role));
  historyTakeIds = $state.raw<string[]>([]);
  activeRow = $state<string | null>(null);
  /**
   * The sidebar's phone mode (a list under the controls, behind a toggle): narrow windows (PHONE_QUERY) and the phone
   * layout in either orientation, so a touch tablet in the phone layout never gets the desktop column.
   */
  narrow = $state(false);
  get phone(): boolean {
    return this.narrow || this.layoutMode !== 'desktop';
  }
  /** Desktop (the bar), or the phone layout in portrait or landscape. The prerendered page is desktop. */
  layoutMode = $state<LayoutMode>('desktop');
  /** Typing on a phone (spec/notes.md): the visual viewport and the focus, as the layout needs them. */
  readonly viewport = new Viewport({ active: () => this.touch, storage: browserStorage });
  /** Compose mode (spec/notes.md, "Typing on a phone"): the comment box on a video has the focus on a phone. */
  get composing(): boolean {
    return composeMode({
      phone: this.touch,
      typing: this.viewport.typing,
      kind: !!this.kindShown,
      canRecord: this.canRecord,
    });
  }
  /** The video's aspect ratio (CSS), for the phone layout's stage. */
  aspect = $state('16 / 9');
  /** What C would say if it were refused now, else null: the phone layout's flip button shows it disabled. */
  flipBlocked = $state<string | null>(null);
  /** The page is a project of a home (retake open <folder>, spec/home.md): it's under /p/<name>/ and the home
   * answered. Then "All videos", "Next video" and > are there. */
  homeAvailable = $state(false);
  /** The next project to review, from the home. */
  homeNext = $state.raw<Card | null>(null);
  /** Where the video is: "Project › video" (or the project alone when the two are one name), from the home's tree. */
  homeCrumb = $state.raw<string[]>([]);
  /** Where the back link goes: the home at the place the reviewer came from, or at the video's project. */
  homeBack = $state.raw<{ href: string; title: string }>({ href: '/', title: '' });
  /** Where the page sits in its home, from its path; null outside one. */
  homeCtx: { home: string; project: string } | null = null;
  /** This page load's id for the unwatch call. */
  readonly viewer = viewerId();
  /** This video's card in its home (its name, path and project); null outside one. */
  homeMe = $state.raw<Card | null>(null);

  // ---------------------------------------------------------------- extensions (spec/extensions.md)
  /** GET api/ext once; an extension's module is imported the first time one of its slots shows. */
  readonly extLoader = new ExtLoader();
  /** The extensions on for this person. */
  exts = $state.raw<ExtInfo[]>([]);
  /** GET api/ext answered (until then a kind's version shows nothing, not "isn't on here"). */
  extsLoaded = $state(false);
  /** The comments column's extension tab on screen (`<id>/<slot>`), or null: the comments. */
  sideTab = $state<string | null>(null);
  /** The versions that are videos: following latest and comparing (C) only ever land on these, never on an
   * extension's kind of version (v0). */
  get cuts(): Version[] {
    return this.versions.filter((v) => !v.kind);
  }
  /** The extension kind of the version on screen (`scripts.doc`): no video, its extension's slot shows it. */
  get kindShown(): string | null {
    return this.shownVersion()?.kind ?? null;
  }
  /** The extension whose `version` slot shows the version on screen. */
  get versionExt(): ExtInfo | null {
    return versionSlot(this.exts, this.kindShown);
  }
  /** The extension whose kind's version on screen has the agent chat in place of the comments (spec/chat.md). */
  get chatExt(): ExtInfo | null {
    return kindChat(this.exts, this.kindShown);
  }
  /** The version on screen has the chat: the comments column is the chat, Send makes the video. */
  get chatShown(): boolean {
    return !!this.chatExt;
  }
  /** The agent the last held Send picked on this page: the chat's messages go to it too. */
  chatAgent = $state('');
  #chatCtx: { ext: ExtInfo; files: ChatFiles } | null = null;
  /** The chat's way to its kind's extension: its files (images, voice) and transcription. */
  chatFiles(): ChatFiles | null {
    const ext = this.chatExt;
    if (!ext) return null;
    if (this.#chatCtx?.ext !== ext) {
      const { ctx } = makeCtx(ext, 'sidebar-tab', this.extHost('sidebar-tab'));
      this.#chatCtx = { ext, files: { id: ext.id, files: () => ctx.files(), transcribe: (p) => ctx.transcribe(p) } };
    }
    return this.#chatCtx.files;
  }
  /** The chat (spec/chat.md). */
  readonly chat = new Chat({
    ext: () => this.chatFiles(),
    folder: () => this.homeMe?.path?.split('/').filter(Boolean).pop() || this.homeCtx?.project || this.info.name || '',
    toast: (text) => this.toast.say(text, 4000),
  });
  /** A phone's pill in the bar: the chat opens with its composer focused (the keyboard comes up). */
  openChat = (): void => {
    if (!this.chatShown) return;
    if (!this.sidebar.panel) this.toggleSidebar();
    const focus = () => this.el.chatBox?.focus();
    focus();
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(() => requestAnimationFrame(focus));
  };
  /** Record and R work here: always on a video; on a kind's version when the kind says `record`. */
  get recordHere(): boolean {
    return !this.kindShown || kindRecords(this.exts, this.kindShown);
  }
  /** How a version reads: `v0 · Script` for an extension's kind, else its label. */
  nameOf = (label: string | null): string => {
    const v = byLabel(this.versions, label);
    return v ? versionName(this.exts, v) : (label ?? '');
  };
  /** The comments column's tabs after To do · Done (sidebar tabs and review panels, on video versions). */
  readonly extTabs: ExtTab[] = $derived(
    sidebarTabs(this.exts, this.versions, { kind: this.kindShown, phone: this.phone }),
  );
  get extTab(): ExtTab | null {
    return this.extTabs.find((t) => t.key === this.sideTab) ?? null;
  }
  /** To do or Done: the comments again (an extension's tab closes). */
  showComments = (filter: 'todo' | 'done'): void => {
    this.sideTab = null;
    this.commentFilter = filter;
  };

  // ---------------------------------------------------------------- typed comments on a kind's version
  /** Where the version slot says a typed comment points (ctx.onAnchor), or null. */
  #anchorFn: (() => Anchor | null) | null = null;
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- listeners, never shown
  readonly #revealSubs = new Set<(d: { anchor: Anchor }) => void>();
  /** The comments column's line on a kind's version: "Comment for the agent…". */
  commentDraft = $state('');
  commentBusy = $state(false);
  /** A phone in portrait on a kind's version: the document fills the page, the comments are a drawer over it whose
   * header has the version picker and the agent. */
  get kindDrawer(): boolean {
    return !!this.kindShown && this.layoutMode === 'portrait';
  }
  /** A phone in portrait on a video: the comments always fill the space between the controls and Record (spec/notes.md,
   * "On a phone"), so it is never empty; the speech bubble goes to them instead of hiding them. */
  get commentsPinned(): boolean {
    return this.layoutMode === 'portrait' && !this.kindShown;
  }

  /** The anchor the version slot gives now, or null (none, or it failed). */
  anchorHere(): Anchor | null {
    try {
      return this.#anchorFn?.() ?? null;
    } catch {
      return null;
    }
  }

  /** A comment's or a question's place clicked on a kind's version: the slot shows it (ctx.on('reveal')), and on a
   * phone the drawer closes so the document is in view. */
  reveal = (anchor: Anchor | null | undefined): void => {
    if (!anchor || !this.kindShown) return;
    for (const cb of [...this.#revealSubs]) {
      try {
        cb({ anchor });
      } catch (err) {
        console.error('retake: an extension failed to show a place:', err);
      }
    }
    if (this.phone && this.sidebar.panel) this.toggleSidebar();
  };

  /** Enter (add) or Ctrl+Enter (add and send) in the comments column's line: a typed comment on this kind's version,
   * anchored where the slot says. */
  addTypedComment = async (send: boolean): Promise<boolean> => {
    const text = this.commentDraft.trim(),
      version = this.shown;
    if (!text || !version || !this.kindShown || !this.canRecord || this.commentBusy) return false;
    this.commentBusy = true;
    try {
      const item = await addTyped({ fetch: (...a) => fetch(...a), version, text, anchor: this.anchorHere() });
      this.commentDraft = '';
      if (send) {
        const w = this.agent.worker;
        if (this.owner && w && !this.automation.state?.config?.worker)
          await this.automation.configure({ worker: w.id, agent: w.agents[0]?.id ?? '', fallback: '' });
        if (await this.automation.send('/tasks', { notes: [item.id] }))
          this.toast.say(
            this.agent.online ? 'Sent to the agent.' : 'Sent. The agent starts when the editing computer is online.',
            3000,
          );
        else this.toast.say(this.automation.error || 'Added, but not sent. Press Send to try again.', 5000);
      }
      void this.notes.refresh();
      return true;
    } catch (err) {
      this.toast.say(err instanceof Error ? err.message : 'Could not add the comment', 5000);
      return false;
    } finally {
      this.commentBusy = false;
    }
  };
  // ---------------------------------------------------------------- a typed comment on a video
  // spec/notes.md, "Typed comments on a video": draw, type or record a comment at any moment. Focusing the box, typing
  // or drawing while not recording pauses the video and starts a pending comment; Enter saves it, Esc cancels it.

  /** The comment being written on a video, or null: the version it's on and how many lines it has. Its words are
   * `commentDraft`, its lines `draftStrokes`. */
  draft = $state.raw<{ version: string; lines: number } | null>(null);
  /** The pending comment's lines (plain: the frame loop draws them). */
  draftStrokes: Stroke[] = [];
  /** The playhead as m:ss, for "Comment at 0:18…". */
  curShort = $state('0:00');

  /** The pending comment's lines that are still there (not undone or erased). */
  get draftLines(): Stroke[] {
    void this.draft;
    return this.draftStrokes.filter((st) => !st.undone);
  }
  /** Its moment: its first line's, else the playhead's. */
  get draftT(): number {
    const first = this.draftLines[0];
    return first ? first.video_t : (this.video?.currentTime ?? 0);
  }
  /** "0:18", the time "Comment at …" says. */
  get draftTime(): string {
    const first = this.draftLines[0];
    return first ? fmt(first.video_t, false) : this.curShort;
  }
  /** There is something to save: words or a line. */
  get draftHasContent(): boolean {
    return !this.kindShown && (!!this.commentDraft.trim() || this.draftLines.length > 0);
  }

  /** A pending comment starts (the box got the focus, a key was typed in it, or a line was drawn while not
   * recording): the video pauses. Nothing on an extension's version or for a viewer. */
  startDraft = (): void => {
    if (this.kindShown || !this.canRecord) return;
    if (!this.draft) {
      this.draft = { version: this.shown ?? '', lines: this.draftLines.length };
      this.dismissHint();
      if (!this.sidebar.panel && this.layoutMode !== 'landscape') this.toggleSidebar();
    }
    const v = this.video;
    if (v && !v.paused) v.pause();
    if (this.#posterOnly) {
      this.#posterOnly.play = false;
      this.paused = true;
    }
  };

  /** The comment box gets the focus (the comments open first when they're hidden). */
  focusComment = (): void => {
    if (!this.sidebar.panel) this.toggleSidebar();
    requestAnimationFrame(() => this.el.comment?.focus());
  };

  #draftChanged(): void {
    if (this.draft) this.draft = { ...this.draft, lines: this.draftLines.length };
  }

  /** Esc: the pending comment and its lines are gone. */
  cancelDraft = (): void => {
    this.draft = null;
    this.draftStrokes = [];
    this.commentDraft = '';
    this.el.comment?.blur();
  };

  /** Enter (or Ctrl+Enter: `send`) in the box: the pending comment becomes a comment (POST api/notes), with a still
   * of its moment when the video is there. Resolves to its note id, or null. */
  saveDraft = async (send: boolean): Promise<number | null> => {
    const d = this.draft ?? { version: this.shown ?? '', lines: 0 };
    const text = this.commentDraft.trim(),
      lines = this.draftLines;
    if (this.kindShown || !this.canRecord || this.commentBusy) return null;
    if (!text && !lines.length) {
      this.cancelDraft();
      return null;
    }
    this.commentBusy = true;
    try {
      const t = Math.max(0, lines[0]?.video_t ?? this.video.currentTime);
      const v = this.video;
      const item = await addTyped({
        fetch: (...a) => fetch(...a),
        version: d.version || this.shown || '',
        text,
        anchor: null,
        t,
        strokes: lines.map((st) => ({ color: st.color, points: st.points, t: st.video_t })),
        frame: v.videoWidth && v.videoHeight ? [v.videoWidth, v.videoHeight] : undefined,
        snapshot: this.#still(t, lines),
      });
      this.draft = null;
      this.draftStrokes = [];
      this.commentDraft = '';
      this.el.comment?.blur(); // back to the player shortcuts
      if (send) {
        const w = this.agent.worker;
        if (this.owner && w && !this.automation.state?.config?.worker)
          await this.automation.configure({ worker: w.id, agent: w.agents[0]?.id ?? '', fallback: '' });
        const into = intoVersion(d.version || this.shown);
        const what = into ? `Sent to ${into}.` : 'Sent.';
        if (await this.automation.send('/tasks', { notes: [item.id] }))
          this.toast.say(
            this.agent.online ? what : `${what} The agent starts when the editing computer is online.`,
            3000,
          );
        else this.toast.say(this.automation.error || 'Saved, but not sent. Press Send to try again.', 5000);
      }
      await this.notes.refresh();
      return item.id;
    } catch (err) {
      this.toast.say(err instanceof Error ? err.message : 'Could not save the comment', 5000);
      return null;
    } finally {
      this.commentBusy = false;
    }
  };

  /** The comment box's Enter or Ctrl+Enter: a typed comment on this version (a video's, or an extension's). */
  addComment = (send: boolean): void => {
    if (this.kindShown) void this.addTypedComment(send);
    else void this.saveDraft(send);
  };

  /** A JPEG of the frame at `t` with the lines drawn on it (spec/notes.md: the note's snapshot), at most 1280 px wide;
   * undefined when the video isn't on that frame (or can't be read): the worker then makes one. */
  #still(t: number, lines: Stroke[]): string | undefined {
    const v = this.video;
    if (!v || v.readyState < 2 || !v.videoWidth || Math.abs(v.currentTime - t) > 0.1 || this.#posterOnly)
      return undefined;
    try {
      const scale = Math.min(1, 1280 / v.videoWidth),
        w = Math.round(v.videoWidth * scale),
        h = Math.round(v.videoHeight * scale);
      const c = document.createElement('canvas');
      c.width = w;
      c.height = h;
      const g = c.getContext('2d');
      if (!g) return undefined;
      g.drawImage(v, 0, 0, w, h);
      for (const st of lines) drawPen(g, st, w, h);
      const url = c.toDataURL('image/jpeg', 0.85);
      return url.startsWith('data:image/jpeg;base64,') ? url : undefined;
    } catch {
      return undefined; // a frame the page may not read
    }
  }

  /** Saved typed comments' lines on the shown version (rows with `strokes`), drawn like an earlier take's around their
   * time, while their comment is in the list. They aren't erasable: deleting the comment removes them. */
  readonly typedStrokes: HistoryStroke[] = $derived(this.sidebar.rows.flatMap((row) => row.strokes ?? []));

  /** What a sidebar tab or a review panel gets as `ctx.player`. Seeking does nothing while recording. */
  readonly #player: Player = {
    time: () => this.video?.currentTime ?? 0,
    duration: () => this.mediaDuration(),
    seek: (t: number) => {
      if (!this.seg && !this.starting && !this.kindShown && isFinite(t)) this.seekTo(t);
    },
    onTime: (cb: (t: number) => void) => {
      this.#timeSubs.add(cb);
      return () => void this.#timeSubs.delete(cb);
    },
  };
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- one per slot name, made once
  readonly #hosts = new Map<SlotName, Host>();
  /** What a slot of this page gives its extension's ctx (one per slot name, so a slot remounts only when it should). */
  extHost(slot: SlotName): Host {
    let h = this.#hosts.get(slot);
    if (!h) {
      h = this.#makeHost();
      this.#hosts.set(slot, h);
    }
    return h;
  }
  #makeHost(): Host {
    // eslint-disable-next-line @typescript-eslint/no-this-alias
    const r = this;
    return {
      where: 'video',
      get homeRoot() {
        return r.homeCtx?.home ?? '/';
      },
      get video() {
        const me = r.homeMe;
        return {
          name: r.homeCtx?.project ?? r.info.name,
          title: me ? videoName(me) : r.info.name,
          path: me?.path ?? '',
          project: me?.project ?? '',
        };
      },
      get version() {
        const kind = r.kindShown;
        return r.shown ? { label: r.shown, ...(kind ? { kind } : {}) } : undefined;
      },
      get role() {
        return extRole(r.role);
      },
      viewer: r.viewer,
      toast: (text) => r.toast.say(text, 4000),
      events: (name, cb) => r.notes.on(name, cb),
      onRecorded: (cb) => {
        r.#recorded.add(cb);
        return () => void r.#recorded.delete(cb);
      },
      recording: () => !!r.seg || r.starting,
      onRecording: (cb) => {
        r.#recordingSubs.add(cb);
        return () => void r.#recordingSubs.delete(cb);
      },
      player: () => (r.kindShown ? undefined : r.#player),
      show: (label, t) => void r.navigateTo(label, t),
      selectComment: (id) => r.selectComment(id),
      onAnchor: (fn) => {
        r.#anchorFn = fn;
        return () => {
          if (r.#anchorFn === fn) r.#anchorFn = null;
        };
      },
      onReveal: (cb) => {
        r.#revealSubs.add(cb);
        return () => void r.#revealSubs.delete(cb);
      },
    };
  }

  /** ctx.navigate within this video: another version (as the picker does), and/or a time on a video. */
  async navigateTo(label: string | undefined, t: number | undefined): Promise<void> {
    if (label && label !== this.shown && byLabel(this.versions, label)) {
      this.verOpen = false;
      this.followLatest = !byLabel(this.versions, label)?.kind && label === this.cuts.at(-1)?.label;
      await this.setBase(label);
    }
    if (t !== undefined && !this.kindShown && !this.seg && !this.starting) this.seekTo(t);
  }

  /** ctx.selectComment: the comments column shows that comment (by its note id, or its question's thread id),
   * scrolled into view and selected; on a video it also goes to its moment. */
  selectComment = (id: number): void => {
    const rows = this.versionRows;
    const row =
      rows.find((x) => x.kind === 'note' && (x.id === id || x.ask?.id === id)) ?? rows.find((x) => x.id === id);
    if (!row) return;
    this.sideTab = null;
    this.commentSearch = '';
    this.commentFilter = this.statusOf(row).state === 'done' ? 'done' : 'todo';
    if (!this.sidebar.panel) this.toggleSidebar();
    if (row.ask) this.selectedAsk = row.ask.id;
    if (this.kindShown) this.activeRow = row.key;
    else this.jumpToComment(row);
    if (typeof requestAnimationFrame === 'function')
      requestAnimationFrame(() =>
        document.querySelector(`#side [data-key="${CSS.escape(row.key)}"]`)?.scrollIntoView({ block: 'nearest' }),
      );
  };

  /** The talk-out (R on a kind's version) is over: on a chat's version it's a voice message (spec/chat.md), else its
   * audio goes to the extension's `ctx.onRecorded`. */
  #deliverTalk(a: Recorded): void {
    if (this.chatShown) {
      void this.chat.voice(a, this.chatAgent);
      return;
    }
    if (!this.#recorded.size) {
      this.toast.say('Nothing kept that recording: the version’s extension isn’t listening', 5000);
      return;
    }
    for (const cb of [...this.#recorded]) {
      try {
        cb(a);
      } catch (err) {
        console.error('retake: an extension failed to take a recording:', err);
      }
    }
  }

  /** Every frame: recording on/off and the playhead go to the extensions that listen. */
  #tellExtensions(vt: number): void {
    const on = !!this.seg;
    if (on !== this.#wasRecording) {
      this.#wasRecording = on;
      for (const cb of [...this.#recordingSubs]) {
        try {
          cb(on);
        } catch (err) {
          console.error('retake: an extension failed on a recording change:', err);
        }
      }
    }
    if (this.#timeSubs.size && vt !== this.#lastTime && !this.kindShown) {
      this.#lastTime = vt;
      for (const cb of [...this.#timeSubs]) {
        try {
          cb(vt);
        } catch (err) {
          console.error('retake: an extension failed on a time update:', err);
        }
      }
    }
  }
  /** The home's Settings (spec/home.md), where the agent and the computers are chosen; null without a home. */
  get settingsHref(): string | null {
    return this.homeCtx ? `${this.homeCtx.home}#settings` : null;
  }

  get homeHref(): string {
    return this.homeCtx?.home ?? '/';
  }
  get nextHref(): string {
    return this.homeCtx && this.homeNext ? projectUrl(this.homeCtx.home, this.homeNext.name) : '';
  }

  /** The phone layout: touch wording (tap ●) instead of keys. */
  get touch(): boolean {
    return this.layoutMode !== 'desktop';
  }

  // ---------------------------------------------------------------- hot state (plain, read by the frame loop)
  info: Info = { name: '', path: '', duration: 0, fps: 30, history: [] };
  take: Take = newTake(null, binCount(1));
  /** The stroke being drawn. */
  drawing: Stroke | null = null;
  switching = false;
  frozenT = 0;
  prevVT: number | null = null;
  lastFrame = now();
  drawHintShown = false;
  mic: Mic | null = null;
  vad = newVad();
  bars: WaveBar[] = [];
  acc = newAcc();
  /** Earlier takes on this version stay on the timeline. */
  hist: { strokes: HistoryStroke[]; bins: Float32Array; images: HistoryImage[] } = {
    strokes: [],
    bins: new Float32Array(1),
    images: [],
  };
  inkW = 1;
  inkH = 1;
  scrubbing = false;
  /** Where the pointer last was over the video (normalized): a pasted image lands there. */
  lastPointer: Point | null = null;
  #cardSig = '';
  /** Streamed versions seen incomplete on this page (spec/bridge.md, "Streaming"): each is announced once, and keeps
   * its family duration after it completes (see mediaDuration). */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, not state
  #seenStreams = new Set<string>();
  /** The family refresh in flight, so a burst of events asks /api/info once. */
  #refreshing: Promise<void> | null = null;
  /** Tells the server which incomplete stream this page shows, so it renders while someone watches. */
  readonly watcher = new Watcher({ viewer: this.viewer, onStream: (label, s) => this.#applyStream(label, s) });
  /**
   * The shown version is an incomplete stream with nothing rendered yet: the page shows its poster, and loads it
   * once its first segment is out, at `t`, playing if `play` (it was playing, or Space was pressed meanwhile).
   */
  #posterOnly: { label: string; t: number; play: boolean } | null = null;
  /** That version's load is under way (a progress event and a watch answer may both say it's playable). */
  #loadingWait = false;
  /** A note the last version switch left (it landed at an incomplete stream's start): replaces the switch's toast. */
  #landNote: string | null = null;

  el = {} as Elements;
  #ictx!: CanvasRenderingContext2D;
  #layer!: HTMLCanvasElement;
  #lctx!: CanvasRenderingContext2D;
  #source!: VideoSource;
  /** The audio buffer. Memory until IndexedDB has opened (every chunk waits for `#storeReady`). */
  #store: ChunkStore = memoryChunkStore();
  #storeReady: Promise<ChunkStore> = Promise.resolve(this.#store);
  /** The image buffer, the audio buffer's twin. */
  #imgStore: ImageStore = memoryImageStore();
  #imgReady: Promise<ImageStore> = Promise.resolve(this.#imgStore);
  /** Phone layout, while recording: the screen stays on (iOS locks after 30 s without a touch, and the mic with it). */
  #awake = false;
  #wakeLock: WakeLockSentinel | null = null;
  /** Takes started on this page: the saver owns their uploads, so recovery leaves them alone. */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #ownTakes = new Set<string>();
  /** The extension slots' `ctx.onRecorded`, `onRecording` and `player.onTime` callbacks. */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #recorded = new Set<(a: Recorded) => void>();
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #recordingSubs = new Set<(on: boolean) => void>();
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #timeSubs = new Set<(t: number) => void>();
  #wasRecording = false;
  #lastTime = -1;
  /** When the talk-out in progress started (a kind's version records audio only, for its extension). */
  #talkStarted = '';
  /** Closed takes (Done, or a version step) whose last save failed: autosave keeps trying them. */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #unsaved = new Set<Take>();
  /** Closed takes whose POST /done waits for their save to go through. */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #donePending = new Set<Take>();
  /** The versions this page played: leaving sends the unwatch call for each (spec/home.md). */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #watched = new Set<string>();
  /** The last Done's POST /done, which "Next video" waits for before it leaves. */
  #doneCall: Promise<void> = Promise.resolve();
  #leaving = false;
  readonly #clipRequests = new WeakMap<Take, string>();
  readonly #saver = new Saver({
    onSaved: (t, data) => {
      const completed = data.segments
        .filter((s) => !s.recording)
        .map((s) => s.file)
        .join('|');
      if (!completed || this.#clipRequests.get(t) === completed) return;
      this.#clipRequests.set(t, completed);
      void fetch(
        `api/session/${encodeURIComponent(t.id ?? '')}/transcribe?v=${encodeURIComponent(String(t.version))}`,
        { method: 'POST' },
      )
        .then((r) => {
          if (!r.ok) {
            this.#clipRequests.delete(t);
            t.dirty = true;
          }
          return r.body?.cancel();
        })
        .catch(() => {
          this.#clipRequests.delete(t);
          t.dirty = true;
        });
    },
    serialize: (t) => this.serialize(t),
    toast: (m, ms) => this.toast.say(m, ms),
    store: () => this.#store,
    images: () => this.#imgStore,
  });

  get video(): HTMLVideoElement {
    return this.el.video;
  }

  readonly hook: ReviewHook = (() => {
    // eslint-disable-next-line @typescript-eslint/no-this-alias
    const r = this;
    return {
      get take() {
        return r.take;
      },
      get seg() {
        return r.seg;
      },
      get view() {
        return { base: r.base, shown: r.shown, flipped: r.flipped, switching: r.switching };
      },
      get info() {
        return r.info;
      },
      get hist() {
        return r.hist;
      },
      get vad() {
        return r.vad;
      },
      get bars() {
        return r.bars;
      },
      get mic() {
        return r.mic;
      },
      get notes() {
        return r.notes.snapshot;
      },
      get sidebar() {
        return r.sidebar;
      },
      get home() {
        return { available: r.homeAvailable, next: r.homeNext?.name ?? null };
      },
      get draft() {
        const d = r.draft;
        return d ? { version: d.version, t: r.draftT, lines: r.draftLines.length, text: r.commentDraft } : null;
      },
      get viewport() {
        const v = r.viewport;
        return { top: v.top, kb: v.kb, height: v.height, up: v.up, typing: v.typing, compose: r.composing };
      },
      serialize: (t: Take) => r.serialize(t),
      save: (t?: Take) => r.save(t),
    };
  })();

  // ---------------------------------------------------------------- take, segments, clocks, event log
  nBins(): number {
    return binCount(this.mediaDuration() || 1);
  }

  /** The version on screen, from the family. */
  shownVersion(): Version | undefined {
    return byLabel(this.versions, this.shown);
  }

  /**
   * The length of the version on screen, for the timeline, scrubbing and the voice bins. An incomplete stream's
   * player only knows the playlist so far (hls.js: its length, which grows; Safari: Infinity; nothing at all before
   * its first segment), so a version that is or was incomplete on this page uses the expected (then the final)
   * duration from the family, and the scale never grows. Otherwise the video's own, else /api/info's.
   */
  mediaDuration(): number {
    const v = this.shownVersion();
    if (v?.duration && (incomplete(v) || this.#seenStreams.has(v.label))) return v.duration;
    const d = this.video?.duration;
    return (d && isFinite(d) ? d : 0) || v?.duration || this.info.duration || 0;
  }

  newTake(): void {
    this.take = newTake(this.base, this.nBins());
    this.doneSaved = false;
    this.sent = false;
  }

  /** The reviewer recorded something this take and hasn't finished it (reads the runes that change with it). */
  get takeOpen(): boolean {
    void [this.seg, this.starting, this.finishing, this.doneSaved, this.base];
    return !!this.take.id && !this.take.closed;
  }

  /** The bar's one button: Send N to agent, Sent, … (lib/status.ts). On a chat's version: make the video from the
   * script, unless nothing changed since the last Send (then it says so, and does nothing else). */
  get button(): SendButton {
    if (this.chatShown) {
      if (this.sending) return { label: 'Sending…', action: 'none', title: 'Sending…' };
      const st = sendState(this.chat.view, this.chat.doc);
      return st.ok
        ? { label: 'Send', action: 'send', title: 'Make the video from the script' }
        : { label: 'Send', action: 'none', title: st.why, disabled: true };
    }
    return sendButton({
      owner: this.owner,
      status: this.agent,
      takeOpen: this.takeOpen,
      unsent: this.unsentRows.length,
      finishing: this.finishing || this.sending,
      saved: this.doneSaved,
      sent: this.sent,
    });
  }

  /** The bar's button pressed (or an agent picked from its list: that send goes to it). A pending typed comment is
   * saved first and goes with it. */
  primary = async (agent = ''): Promise<void> => {
    this.pickerOpen = false;
    if (this.chatShown) {
      await this.sendScript(agent);
      return;
    }
    const extra: number[] = [];
    if (this.draftHasContent) {
      const id = await this.saveDraft(false);
      if (id == null) return;
      extra.push(id);
    }
    const b = this.button;
    switch (extra.length ? 'send' : b.action) {
      case 'send':
        if (b.action === 'save') {
          if (await this.done()) this.toast.say('Saved. The agent can’t edit this project yet.', 5000);
          break;
        }
        await this.sendAll(agent, extra);
        break;
      case 'save':
        // Local mode that can't edit: the take is saved, and the notice over Send says what's missing.
        if (await this.done()) this.toast.say('Saved. The agent can’t edit this project yet.', 5000);
        break;
      case 'none':
        if (!this.finishing && !this.sending)
          this.toast.say(
            this.notes.job
              ? 'Your notes are still being transcribed. They go to the agent when ready.'
              : this.touch
                ? 'Nothing new to send. Draw or type a comment, or tap Record and talk.'
                : `Nothing new to send. Draw or type a comment, or press ${this.shortcutLabel('toggleRec')} and talk.`,
            4000,
          );
        break;
    }
  };

  /** Send on a chat's version: "I like it, make the video" (spec/chat.md). No confirmation: the chat's status line
   * says what started ("Building v1…"). Never silent: unchanged, it says so. */
  async sendScript(agent = ''): Promise<void> {
    if (this.sending) return;
    if (agent) this.chatAgent = agent;
    const b = this.button;
    if (b.disabled) {
      this.toast.say(b.title, 3000);
      return;
    }
    this.sending = true;
    try {
      const w = this.agent.worker;
      if (this.owner && w && !this.automation.state?.config?.worker)
        await this.automation.configure({ worker: w.id, agent: w.agents[0]?.id ?? '', fallback: '' });
      const into = await this.chat.send(agent);
      if (into === null) return;
      void this.automation.refresh();
      if (!this.agent.online)
        this.toast.say(`Sent to ${into}. The agent starts when the editing computer is online.`, 4000);
    } finally {
      this.sending = false;
    }
  }

  /** Send: the take in progress goes with `send: true` (its notes go once transcribed, contract C3), and every other
   * open note nobody sent yet goes now. Anyone who can record sends; with no computer online it all waits in the
   * queue and starts when one connects. */
  async sendAll(agent = '', extra: number[] = []): Promise<void> {
    if (this.sending) return;
    const t = this.take,
      open = !!t.id && !t.closed;
    const mine = this.unsentRows.filter((row) => !open || row.take !== t.id).map((row) => row.id);
    const others = [...mine, ...extra.filter((id) => !mine.includes(id))];
    this.sending = true;
    try {
      // Nothing chosen yet (a first send by the owner): the computer that's online and its first agent.
      const w = this.agent.worker;
      if (this.owner && w && !this.automation.state?.config?.worker)
        await this.automation.configure({ worker: w.id, agent: w.agents[0]?.id ?? '', fallback: '' });
      if (open && !(await this.done(true, agent))) return;
      if (others.length) {
        if (!(await this.automation.send('/tasks', agent ? { notes: others, agent } : { notes: others }))) {
          this.toast.say(this.automation.error || 'Could not send. Try again.', 5000);
          return;
        }
        void this.notes.refresh();
      } else await this.automation.refresh(); // the take is on its way now (`sending`): its comments show Working
      this.sent = true;
      // A comment on v7 becomes an edit in v8: the toast says where they went.
      const into = intoVersion(this.shown);
      const what = into ? `Sent to ${into}.` : 'Sent.';
      this.toast.say(
        !this.agent.online
          ? `${what} The agent starts when the editing computer is online.`
          : open
            ? `${what} The agent starts as soon as your notes are transcribed.`
            : what,
        4000,
      );
    } finally {
      this.sending = false;
    }
  }

  /** The keyboard hint is shown until the first recording, once per browser (it's then behind ?). */
  dismissHint(): void {
    if (this.hintGone || !this.canRecord) return;
    this.hintGone = true;
    markHintSeen();
  }

  ensureTake(): void {
    if (this.take.closed) {
      this.historyVoice = [
        ...this.historyVoice,
        ...this.take.segments.map((seg) => ({
          take: this.take.id ?? '',
          seg: seg.id,
          bins: seg.voiceBins ?? new Float32Array(0),
        })),
      ];
      if (this.take.id) this.historyTakeIds = [...this.historyTakeIds, this.take.id];
      archive(this.take, this.hist, this.nBins());
      this.newTake();
    }
    const t = this.take,
      video = this.video;
    if (t.id) return;
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- a timestamp, not state
    const d = new Date();
    t.id = stamp(d);
    this.#ownTakes.add(t.id);
    t.created = isoLocal(d);
    t.t0 = now();
    t.version = this.base;
    insertEv(t, {
      p: t.t0,
      video_t: video.currentTime,
      event: 'start',
      playing: !video.paused,
      rate: video.playbackRate,
      version: this.shown,
      base: this.base,
      flip: this.flipped,
    });
  }

  logEv(event: string, extra?: Record<string, unknown>, t: Take = this.take): TakeEvent | null {
    if (!t || !t.id || t.closed) return null;
    return insertEv(t, { p: now(), video_t: this.video.currentTime, event, ...(extra || {}) });
  }

  serialize = (t: Take): TakeData => {
    const v = this.video;
    return serializeTake(t, {
      now: now(),
      videoTime: v.currentTime,
      duration: this.mediaDuration(),
      width: v.videoWidth,
      height: v.videoHeight,
      // eslint-disable-next-line svelte/prefer-svelte-reactivity -- a timestamp, not state
      date: new Date(),
    });
  };

  // ---------------------------------------------------------------- saving (automatic)
  save = (t: Take | null = this.take): Promise<void> | undefined => this.#saver.save(t);

  /** Page closing: best-effort keepalive upload of the last few seconds, and the stream on screen is unwatched. */
  flushOnExit = (): void => {
    this.watcher.leave();
    const t = this.take,
      s = this.seg;
    if (!t || !t.id || t.closed) return;
    if (s) {
      s.p1 = now();
      s.v1 = this.video.currentTime;
      try {
        s.rec.requestData();
      } catch {
        /* ignore */
      }
    }
    this.#saver.flush(t);
  };

  /** The page is going away (closed, reloaded, or navigating): the last audio, and the unwatch calls. */
  onPageHide = (): void => {
    this.flushOnExit();
    this.leave();
  };

  /** Tell the server this page no longer watches the versions it played, so the streaming side can stop a render no
   * one watches (spec/home.md, "Close means close"). A beacon: it goes out even as the page unloads. */
  leave = (): void => {
    this.watcher.leave();
    if (typeof navigator === 'undefined' || typeof navigator.sendBeacon !== 'function') return;
    for (const label of this.#watched) {
      try {
        navigator.sendBeacon(unwatchUrl(label, this.viewer));
      } catch {
        /* best effort */
      }
    }
  };

  /** A recorded chunk goes into the local buffer first; the next save uploads it. */
  #onChunk(t: Take, s: Segment, data: Blob): void {
    const take = t.id ?? '',
      version = String(t.version),
      path = byLabel(this.versions, t.version)?.path ?? '';
    addChunk(s, data, async (offset) => {
      const store = await this.#storeReady;
      await store.put({
        take,
        version,
        file: s.file,
        offset,
        data,
        size: data.size,
        mime: s.mime,
        path,
        saved: Date.now(),
      });
    });
  }

  /**
   * Resend audio that earlier takes left in the buffer (the page closed or crashed before the server had it). Retries
   * every AUTOSAVE_MS while the server is unreachable or refusing, until it has everything.
   */
  async recover(): Promise<void> {
    const store = await this.#storeReady;
    const res = await recoverPending(
      store,
      this.versions,
      async (url, body) => {
        const r = await fetch(url, { method: 'POST', body });
        return { status: r.status, json: (await r.json().catch(() => ({}))) as { size?: number } };
      },
      (take) => this.#ownTakes.has(take),
    );
    if (res.recovered || res.kept)
      console.info(`retake: audio buffer: ${res.recovered} file(s) recovered (${res.sent} bytes), ${res.kept} kept`);
    if (res.retry) setTimeout(() => void this.recover().catch(() => {}), AUTOSAVE_MS);
  }

  #recordingIntent = false;
  #recordingStart = 0;
  #startOperation: Promise<void> = Promise.resolve();
  #silenceSeconds = 0;
  #rotateAfter = 0;

  // ---------------------------------------------------------------- recording: R toggles a segment
  async openMic(): Promise<boolean> {
    try {
      this.mic = await openMic();
      return true;
    } catch {
      this.toast.say(this.touch ? MIC_BLOCKED_TOUCH : MIC_BLOCKED, 7000);
      return false;
    }
  }

  closeMic(): void {
    if (!this.mic) return;
    closeMic(this.mic);
    this.mic = null;
  }

  /** R on. `answerTo`: the question thread this segment answers (Answer in the sidebar). The comments show To do,
   * so new comments show (an extension's tab or panel closes). On an extension's kind of version (v0) it records
   * audio only, for that extension, and only when the kind says `record`. */
  startRec = (answerTo: number | null = null): Promise<void> => {
    if (this.seg || this.starting || this.switching || this.finishing) return Promise.resolve();
    if (!this.recordHere) return Promise.resolve();
    this.showComments('todo');
    this.#recordingIntent = true;
    this.#recordingStart = now();
    this.#startOperation = this.kindShown ? this.#beginTalk() : this.#beginClip(answerTo);
    return this.#startOperation;
  };

  /** A talk-out on an extension's version: R and Record as on a video (REC, the timer, the red edge, the level), but
   * no take and no pen. When it stops, its audio goes to the extension (`ctx.onRecorded`). */
  async #beginTalk(): Promise<void> {
    if (!this.canRecord) {
      this.toast.say(VIEW_ONLY, 2600);
      this.#recordingIntent = false;
      return;
    }
    this.starting = true;
    this.dismissHint();
    try {
      if (this.mic) void this.mic.ac.resume();
      if (!this.mic && !(await this.openMic())) {
        this.#recordingIntent = false;
        return;
      }
      const mic = this.mic as Mic;
      const mime = pickMime((m) => MediaRecorder.isTypeSupported(m));
      const rec = new MediaRecorder(mic.stream, mime ? { mimeType: mime, audioBitsPerSecond: 64000 } : {});
      const chunks: Blob[] = [];
      rec.ondataavailable = (e) => {
        if (e.data && e.data.size) chunks.push(e.data);
      };
      await new Promise((res, rej) => {
        rec.onstart = res;
        rec.onerror = (ev) => rej((ev as Event & { error?: unknown }).error);
        rec.start(1000);
      });
      // eslint-disable-next-line svelte/prefer-svelte-reactivity -- a timestamp, not state
      this.#talkStarted = isoLocal(new Date());
      const s = {
        n: 1,
        id: 'R',
        talk: true,
        rec,
        mime: recorderMime(rec.mimeType, mime),
        version: this.shown,
        flip: false,
        file: '',
        chunks,
        uploaded: 0,
        p0: now(),
        p1: null,
        v0: 0,
        v1: null,
        vmin: 0,
        vmax: 0,
        size: 0,
        stored: 0,
        persisted: Promise.resolve(),
      } satisfies Segment;
      this.seg = s;
      this.#keepAwake(true);
      this.bars = [];
      this.recElapsed = fmt(now() - this.#recordingStart, false);
      if (!this.#recordingIntent) {
        this.seg = null;
        this.#keepAwake(false);
        await this.#finishTalk(s);
      }
    } catch (err) {
      this.#recordingIntent = false;
      this.toast.say('Could not start recording: ' + ((err as Error)?.message || err), 6000);
    } finally {
      this.starting = false;
    }
  }

  /** Stops a talk-out: its recorder's last chunk, then the audio to the extension, and the mic closes. */
  #finishTalk(s: Segment): Promise<unknown> {
    if (s.stopped) return s.stopped;
    s.p1 = now();
    const started = this.#talkStarted;
    s.stopped = new Promise((res) => {
      s.rec.onstop = res;
    }).then(() => {
      if (!this.seg) this.closeMic();
      const type = s.mime || s.rec.mimeType || 'audio/webm';
      this.#deliverTalk({
        blob: new Blob(s.chunks, { type }),
        type,
        duration: Math.max(0, (s.p1 ?? now()) - s.p0),
        started,
      });
    });
    s.rec.stop();
    return s.stopped;
  }

  async #beginClip(answerTo: number | null): Promise<void> {
    const previous = this.seg;
    if (!this.canRecord) {
      this.toast.say(VIEW_ONLY, 2600);
      return;
    }
    this.starting = true;
    this.dismissHint();
    const video = this.video;
    try {
      if (this.mic) void this.mic.ac.resume();
      if (!this.mic && !(await this.openMic())) return;
      this.ensureTake();
      const t = this.take,
        mic = this.mic as Mic;
      const mime = pickMime((m) => MediaRecorder.isTypeSupported(m));
      const rec = new MediaRecorder(mic.stream, mime ? { mimeType: mime, audioBitsPerSecond: 64000 } : {});
      const s = newSegment(t, rec, recorderMime(rec.mimeType, mime), {
        version: this.shown,
        flip: this.flipped,
        videoTime: video.currentTime,
        answerTo,
      });
      rec.ondataavailable = (e) => {
        if (e.data && e.data.size) this.#onChunk(t, s, e.data);
      };
      await new Promise((res, rej) => {
        rec.onstart = res;
        rec.onerror = (ev) => rej((ev as Event & { error?: unknown }).error);
        rec.start(1000);
      });
      settleMime(s, rec.mimeType); // Safari only says audio/mp4 once it runs: the file is then seg_NN.m4a
      s.p0 = now();
      t.segments.push(s);
      this.seg = s;
      // Start the next encoder before stopping the previous one, keeping the microphone continuous.
      if (previous) void this.#finishClip(t, previous);
      this.#rotateAfter = now() + 10;
      this.#silenceSeconds = 0;
      this.#keepAwake(true);
      if (!previous) this.bars = [];
      this.recElapsed = fmt(now() - this.#recordingStart, false);
      this.logEv(
        'rec_start',
        {
          seg: s.id,
          file: s.file,
          playing: !video.paused,
          rate: video.playbackRate,
          version: this.shown,
          flip: this.flipped,
        },
        t,
      );
      if (!this.#recordingIntent) {
        this.seg = null;
        this.#keepAwake(false);
        await this.#finishClip(t, s);
      }
    } catch (err) {
      this.#rotateAfter = now() + 5;
      if (!previous) this.#recordingIntent = false;
      this.toast.say('Could not start recording: ' + ((err as Error)?.message || err), 6000);
    } finally {
      this.starting = false;
    }
  }

  stopRec = (): Promise<unknown> => {
    this.#recordingIntent = false;
    const s = this.seg;
    this.seg = null;
    this.#keepAwake(false);
    const finish = !s ? Promise.resolve() : s.talk ? this.#finishTalk(s) : this.#finishClip(this.take, s);
    return Promise.all([finish, this.#startOperation]);
  };

  #finishClip(t: Take, s: Segment): Promise<unknown> {
    if (s.stopped) return s.stopped;
    s.p1 = now();
    s.v1 = this.video.currentTime;
    this.logEv('rec_stop', { seg: s.id }, t);
    s.stopped = new Promise((res) => {
      s.rec.onstop = res;
    }).then(() => {
      s.finalized = true;
      void this.save(t);
      if (!this.#recordingIntent) void this.followCurrent();
    });
    s.rec.stop();
    return s.stopped;
  }

  #keepAwake(on: boolean): void {
    this.#awake = on && this.touch;
    if (this.#awake && !this.#wakeLock && 'wakeLock' in navigator) {
      navigator.wakeLock.request('screen').then(
        (lock) => {
          if (this.#awake && !this.#wakeLock) this.#wakeLock = lock;
          else void lock.release().catch(() => {});
        },
        () => {}, // not allowed (low power mode, say): the screen may sleep
      );
    } else if (!this.#awake && this.#wakeLock) {
      void this.#wakeLock.release().catch(() => {});
      this.#wakeLock = null;
    }
  }

  toggleRec = (): void => {
    if (this.seg || this.starting) void this.stopRec();
    else void this.startRec();
  };

  /** Voice activity (only while recording) feeds the waveform and the voice bins under the playhead. */
  listen(dt: number, vt: number): void {
    const s = this.seg,
      mic = this.mic,
      video = this.video;
    if (s && mic) {
      mic.an.getFloatTimeDomainData(mic.buf);
      const level = vadStep(this.vad, levelDb(mic.buf), dt);
      s.vmin = Math.min(s.vmin, vt);
      s.vmax = Math.max(s.vmax, vt);
      this.bars = pushBars(this.bars, this.acc, dt, level, this.vad.active);
      this.#silenceSeconds = this.vad.active ? 0 : this.#silenceSeconds + dt;
      const age = now() - s.p0;
      if (
        !s.talk &&
        this.#recordingIntent &&
        !this.starting &&
        !this.drawing &&
        now() >= this.#rotateAfter &&
        (age >= 20 || (age >= 10 && this.#silenceSeconds >= 0.6))
      ) {
        this.#startOperation = this.#beginClip(s.answer_to ?? null);
      }
    } else {
      this.vad.active = false;
      this.acc = newAcc();
    }
    if (this.vad.active && !s?.talk && this.mediaDuration()) {
      this.take.bins = grow(this.take.bins, this.nBins());
      addVoice(this.take.bins, dt, vt, this.prevVT, !video.paused, video.seeking);
      if (s) {
        s.voiceBins = grow(s.voiceBins ?? new Float32Array(0), this.nBins());
        addVoice(s.voiceBins, dt, vt, this.prevVT, !video.paused, video.seeking);
      }
    }
  }

  // ---------------------------------------------------------------- player
  setPlaybackRate = (rate: number): void => {
    const next = clamp(Math.round(rate * 4) / 4, 0.5, 3);
    this.playbackRate = next;
    this.video.playbackRate = next;
  };

  slower = (): void => this.setPlaybackRate(this.playbackRate - 0.25);
  faster = (): void => this.setPlaybackRate(this.playbackRate + 0.25);
  stop = (): void => this.video.pause();

  setShortcut = (action: ShortcutAction, value: string): void => {
    const key = value.trim() || value;
    const checked = validateShortcuts({ ...this.shortcuts, [action]: key });
    if (!('shortcuts' in checked)) {
      this.shortcutError = checked.error;
      return;
    }
    this.shortcuts = checked.shortcuts;
    this.shortcutError = '';
    try {
      localStorage.setItem('retake.shortcuts', JSON.stringify(this.shortcuts));
    } catch {
      /* The current page still uses the changed key when browser storage is blocked. */
    }
  };

  resetShortcuts = (): void => {
    this.shortcuts = { ...DEFAULT_SHORTCUTS };
    this.shortcutError = '';
    try {
      localStorage.removeItem('retake.shortcuts');
    } catch {
      /* blocked storage has no effect on this page */
    }
  };

  togglePlay = (): void => {
    const video = this.video,
      wait = this.#posterOnly;
    if (wait) {
      // nothing rendered yet: it plays (or not) once its first segment is out
      wait.play = !wait.play;
      this.paused = !wait.play;
      return;
    }
    if (video.paused || video.ended)
      video.play().catch((err: Error) => this.toast.say('Could not play: ' + err.message));
    else video.pause();
  };

  shortcutLabel = (action: ShortcutAction): string => shortcutLabel(this.shortcuts[action]);

  seekTo(t: number): void {
    this.video.currentTime = this.#clampT(t);
  }

  #clampT(t: number): number {
    const r = renderedEnd(this.shownVersion());
    if (r !== null) return clamp(t, 0, Math.max(0, r - 0.1));
    const d = this.video.duration || 0;
    return clamp(t, 0, d ? d - 0.001 : Math.max(0, t));
  }

  #scrubber: Scrubber | null = null;
  /** The timeline drag (lib/scrub.ts): one seek in flight at a time, paused while dragging. */
  get scrubber(): Scrubber {
    return (this.#scrubber ??= new Scrubber(this.video));
  }

  frameStep(dir: number): void {
    const video = this.video;
    if (!video.paused) video.pause();
    const fps = this.info.fps || 30,
      k = Math.floor(video.currentTime * fps + 1e-3);
    this.seekTo((k + dir) / fps + 0.1 / fps);
  }

  onPlay = (): void => {
    this.logEv('play');
    this.paused = this.video.paused;
  };
  onPause = (): void => {
    this.logEv('pause');
    // On a stream's poster the button shows whether it plays once its first segment is out.
    this.paused = this.#posterOnly ? !this.#posterOnly.play : this.video.paused;
  };
  onEnded = (): void => {
    // An incomplete stream waits for its next segments; it hasn't ended.
    if (incomplete(this.shownVersion())) return;
    this.logEv('ended');
  };
  onSeeking = (): void => {
    if (this.switching) return;
    const t = this.take,
      last = t.events[t.events.length - 1],
      p = now();
    if (t.id && !t.closed && last && last.event === 'seek' && p - last.p < 0.5) {
      last.p = p;
      last.video_t = this.video.currentTime;
    } else this.logEv('seek', { from: r3(this.prevVT ?? 0) });
  };
  onLoadedMetadata = (): void => {
    this.video.playbackRate = this.playbackRate;
    this.layout();
    const { videoWidth: w, videoHeight: h } = this.video;
    if (w && h) this.aspect = `${w} / ${h}`;
    this.syncDuration();
  };
  /** The video's duration changed (a streamed render's player learns more of the playlist, or its end). */
  onDurationChange = (): void => {
    if (this.video.readyState >= 1) this.syncDuration();
  };
  /** The duration shown and the voice bins follow mediaDuration. */
  syncDuration(): void {
    this.timelineDuration = this.mediaDuration();
    this.durText = fmt(this.timelineDuration, false);
    this.take.bins = grow(this.take.bins, this.nBins());
    this.hist.bins = grow(this.hist.bins, this.nBins());
  }
  onVideoError = (): void => {
    if (this.kindShown) return; // nothing plays there (its slot shows it)
    const v = this.shownVersion();
    if (v?.relay) {
      if (!this.switching) void this.#checkOffline(v); // a switch that fails checks its own version
      return;
    }
    this.#videoFailed(v);
  };

  /** A relayed version that won't play: when its computer is offline (its video answers 503), the poster stays with
   * "The editing computer is offline" and the page looks again every 5 s, then plays it once it's back. Anything else
   * is the usual failure. */
  async #checkOffline(v: Version): Promise<void> {
    if (this.#offlineTimer) clearTimeout(this.#offlineTimer);
    this.#offlineTimer = null;
    const status = await fetch(`video/${encodeURIComponent(v.label)}`, { method: 'HEAD' }).then(
      (r) => r.status,
      () => 0,
    );
    if (this.shown !== v.label) return; // the page moved on meanwhile
    if (status === 0) {
      this.#offlineTimer = setTimeout(() => void this.#checkOffline(v), OFFLINE_RETRY_MS); // the server is unreachable
      return;
    }
    if (status === 503) {
      if (!this.offline) {
        this.offline = true;
        if (this.#posterOnly?.label !== v.label)
          this.#posterOnly = { label: v.label, t: this.video.currentTime, play: false };
        this.paused = true;
        this.#source.clear(); // the poster shows instead of a broken player
        this.syncDuration();
      }
      this.#offlineTimer = setTimeout(() => void this.#checkOffline(v), OFFLINE_RETRY_MS);
      return;
    }
    if (this.offline) {
      this.offline = false;
      void this.#loadWaiting(); // it's back: play from where it was
      return;
    }
    this.#videoFailed(v);
  }

  #videoFailed(v: Version | undefined): void {
    if (incomplete(v)) {
      // Its render may have just failed (the `failed` event says so): not a file that won't play.
      if (v) this.toast.say(`Could not play ${verName(v.label)} yet`, 3000);
      return;
    }
    this.fatal = FATAL(this.info.name);
  }

  // ---------------------------------------------------------------- pen (only while recording)
  layout = (): void => {
    const { stage, video, ink } = this.el;
    if (!stage || !ink) return;
    const f = fitFrame(stage.clientWidth, stage.clientHeight, video.videoWidth || 16, video.videoHeight || 9),
      dpr = devicePixelRatio || 1;
    Object.assign(ink.style, {
      left: f.left + 'px',
      top: f.top + 'px',
      width: f.width + 'px',
      height: f.height + 'px',
    });
    ink.width = this.#layer.width = Math.max(1, Math.round(f.width * dpr));
    ink.height = this.#layer.height = Math.max(1, Math.round(f.height * dpr));
    this.inkW = f.width;
    this.inkH = f.height;
    this.frame = f;
  };

  #norm(e: { clientX: number; clientY: number }): Point {
    const r = this.el.ink.getBoundingClientRect();
    return [clamp((e.clientX - r.left) / r.width, 0, 1), clamp((e.clientY - r.top) / r.height, 0, 1)];
  }

  /** The ink canvas: pointer handlers. No recording, no ink (a one-time hint says so). */
  attachInk = (ink: HTMLCanvasElement) => {
    this.el.ink = ink;
    this.#ictx = ink.getContext('2d') as CanvasRenderingContext2D;
    this.#layer = document.createElement('canvas');
    this.#lctx = this.#layer.getContext('2d') as CanvasRenderingContext2D;
    const down = (e: PointerEvent) => {
      const s = this.seg,
        video = this.video;
      if (e.button !== 0 || this.drawing) return;
      if (this.eraser && this.canRecord) {
        // The eraser works recording or not: it only ever removes.
        e.preventDefault();
        try {
          ink.setPointerCapture(e.pointerId);
        } catch {
          /* the pointer is already gone */
        }
        this.#erasing = true;
        this.#eraseLast = null;
        this.#eraseAt(e);
        return;
      }
      if (!s || s.talk) {
        if (!this.canRecord) {
          if (!this.drawHintShown) this.toast.say(VIEW_ONLY, 2600);
          this.drawHintShown = true;
          return;
        }
        if (s?.talk || this.kindShown) return;
        // Not recording: the line goes to a typed comment (spec/notes.md, "Typed comments on a video"), and the
        // video pauses there.
        e.preventDefault();
        try {
          ink.setPointerCapture(e.pointerId);
        } catch {
          /* the pointer is already gone */
        }
        this.startDraft();
        this.drawing = {
          id: 0,
          seg: '',
          version: this.shown,
          flip: this.flipped,
          color: this.penColor,
          p0: now(),
          video_t: video.currentTime,
          paused: true,
          points: [this.#norm(e)],
        };
        return;
      }
      e.preventDefault();
      try {
        ink.setPointerCapture(e.pointerId);
      } catch {
        /* the pointer is already gone: the stroke still ends on its pointerup */
      }
      this.drawing = {
        id: 0,
        seg: s.id,
        version: this.shown,
        flip: this.flipped,
        color: this.penColor,
        p0: now(),
        video_t: video.currentTime,
        paused: video.paused,
        points: [this.#norm(e)],
      };
    };
    const move = (e: PointerEvent) => {
      if (this.#erasing) {
        const co = e.getCoalescedEvents ? e.getCoalescedEvents() : [];
        for (const ce of co.length ? co : [e]) this.#eraseAt(ce);
        return;
      }
      const st = this.drawing;
      if (!st) return;
      const co = e.getCoalescedEvents ? e.getCoalescedEvents() : [];
      for (const ce of co.length ? co : [e]) addPoint(st.points, this.#norm(ce), this.inkW, this.inkH);
    };
    const up = () => {
      this.#erasing = false;
      const st = this.drawing,
        t = this.take;
      if (!st) return;
      this.drawing = null;
      if (isClick(st.points, this.inkW, this.inkH)) {
        // A click draws nothing. In compose mode a tap on the video ends it (spec/notes.md, "Typing on a phone").
        if (this.composing) this.el.comment?.blur();
        return;
      }
      if (!st.seg) {
        // a line of the pending typed comment
        st.id = this.draftStrokes.length + 1;
        st.p1 = now();
        st.video_t_end = this.video.currentTime;
        this.draftStrokes.push(st);
        this.startDraft();
        this.#draftChanged();
        return;
      }
      st.id = t.nextId++;
      st.p1 = now();
      st.video_t_end = this.video.currentTime;
      insertEv(t, { p: st.p0, video_t: st.video_t, event: 'draw_start', id: st.id, seg: st.seg, color: st.color });
      this.logEv('draw_end', { id: st.id, seg: st.seg });
      t.strokes.push(st);
    };
    const cancel = () => {
      this.drawing = null;
      this.#erasing = false;
    };
    ink.addEventListener('pointerdown', down);
    ink.addEventListener('pointermove', move);
    ink.addEventListener('pointerup', up);
    ink.addEventListener('pointercancel', cancel);
    return () => {
      ink.removeEventListener('pointerdown', down);
      ink.removeEventListener('pointermove', move);
      ink.removeEventListener('pointerup', up);
      ink.removeEventListener('pointercancel', cancel);
    };
  };

  /**
   * Z: undo the last stroke or image, whichever came last (they share one id counter); or, when the last thing done
   * was an erase, bring that line back.
   */
  undo = (): void => {
    // Not recording, with a pending typed comment: its last line goes first.
    if (!this.seg && this.draft) {
      const last = this.draftStrokes.findLast((st) => !st.undone);
      if (last) {
        last.undone = true;
        this.#draftChanged();
        return;
      }
    }
    const t = this.take;
    const target = undoTarget(t);
    if (t.closed && target?.kind !== 'unerase') return;
    if (!target) return;
    if (target.kind === 'unerase') {
      t.erased.pop();
      const { take, id } = target.mark;
      if (take) {
        const hs = this.hist.strokes.find((x) => x.take === take && x.id === id);
        if (hs) {
          hs.undone = false;
          void this.#saveErase(hs, false);
        }
        return;
      }
      const st = t.strokes.find((x) => x.id === id);
      if (!st) return;
      st.undone = false;
      st.erased = false;
      this.logEv('unerase', { id: st.id });
      return;
    }
    target.item.undone = true;
    this.logEv('undo', { id: target.item.id });
  };

  #erasing = false;
  /** Where the eraser was at the last pointer event of this swipe (normalized), so a fast swipe misses nothing. */
  #eraseLast: Point | null = null;

  /**
   * The eraser at a pointer position: remove every line on screen it touches. The take in progress drops its own
   * lines; a line of an earlier take is erased on the server (erased.json).
   */
  #eraseAt(e: { clientX: number; clientY: number }): void {
    const t = this.take,
      vt = this.video.currentTime;
    const r = this.touch ? ERASE_RADIUS.touch : ERASE_RADIUS.mouse,
      p = this.#norm(e),
      from = this.#eraseLast ?? p;
    this.#eraseLast = p;
    const own = t.id && !t.closed ? t.strokes.filter(this.#onShown) : [];
    const hist = this.hist.strokes.filter(
      (st) => typeof st.id === 'number' && !!st.take && !st.undone && !st.sourceVersion,
    );
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- local scratch sets, not state
    const ownHits = new Set<number>(),
      // eslint-disable-next-line svelte/prefer-svelte-reactivity -- local scratch sets, not state
      histHits = new Set<HistoryStroke>();
    for (const q of sweep(from, p, this.inkW, this.inkH, r / 2)) {
      for (const id of strokesHit(own, q, vt, this.inkW, this.inkH, r)) ownHits.add(id);
      for (const st of hist)
        if (!histHits.has(st) && strokesHit([st as Stroke], q, vt, this.inkW, this.inkH, r).length) histHits.add(st);
    }
    // The pending typed comment's lines: gone from it.
    const draft = this.draftStrokes.filter((st) => !st.undone && this.#onShown(st));
    let draftHit = false;
    for (const q of draft.length ? sweep(from, p, this.inkW, this.inkH, r / 2) : []) {
      for (const id of strokesHit(draft, q, vt, this.inkW, this.inkH, r)) {
        const st = draft.find((x) => x.id === id);
        if (st && !st.undone) {
          st.undone = st.erased = true;
          draftHit = true;
        }
      }
    }
    if (draftHit) this.#draftChanged();
    for (const id of ownHits) {
      const st = t.strokes.find((x) => x.id === id);
      if (!st) continue;
      st.undone = true;
      st.erased = true;
      t.erased.push({ id, seq: t.nextId });
      this.logEv('erase', { id });
    }
    for (const st of histHits) {
      st.undone = true;
      t.erased.push({ id: st.id as number, take: st.take, seq: t.nextId });
      void this.#saveErase(st, true);
    }
  }

  /** Erase (or bring back) a line of an earlier take on the server; if that fails, the line comes back on screen. */
  async #saveErase(st: HistoryStroke, erased: boolean): Promise<void> {
    try {
      const r = await fetch(`api/erase?v=${encodeURIComponent(this.shown ?? '')}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ take: st.take, id: st.id, erased }),
      });
      if (!r.ok) throw new Error(String(r.status));
    } catch {
      st.undone = !erased;
      this.toast.say(erased ? "Couldn't erase that line" : "Couldn't bring that line back", 3000);
    }
  }

  /** E or the eraser button: switch between the eraser and the pen. */
  toggleEraser = (): void => {
    this.eraser = !this.eraser;
  };

  // ---------------------------------------------------------------- images (only while recording, like the pen)
  /** A pointer position as a point normalized to the video frame (clamped to it). */
  normPoint(e: { clientX: number; clientY: number }): Point {
    return this.#norm(e);
  }

  /** Whether a pointer position is over the stage (the video area). */
  #overStage(e: { clientX: number; clientY: number }): boolean {
    const r = this.el.stage?.getBoundingClientRect();
    return !!r && e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
  }

  /** Remember where the pointer is over the video, so a paste lands there. */
  onPointerMove = (e: PointerEvent): void => {
    const r = this.el.ink?.getBoundingClientRect();
    if (!r || !r.width || !r.height) return;
    const x = (e.clientX - r.left) / r.width,
      y = (e.clientY - r.top) / r.height;
    if (x >= 0 && x <= 1 && y >= 0 && y <= 1) this.lastPointer = [x, y];
  };

  /** An image file was dropped or pasted at `at` (normalized; null = where the pointer was, else the center). */
  addImage = async (file: Blob, at: Point | null): Promise<TakeImage | null> => {
    const s = this.seg?.talk ? null : this.seg,
      t = this.take;
    const refusal = this.canRecord ? refuseImage(file, !!s) : VIEW_ONLY;
    if (refusal || !s) {
      this.toast.say(refusal ?? HINT_IMAGE, 2600);
      return null;
    }
    const mime = file.type.toLowerCase().split(';')[0],
      ext = imageExt(mime) as string;
    const video_t = this.video.currentTime,
      p0 = now(),
      version = this.shown,
      flip = this.flipped;
    const url = URL.createObjectURL(file);
    const pic = new Image();
    try {
      pic.src = url;
      await pic.decode();
    } catch {
      URL.revokeObjectURL(url);
      this.toast.say(MSG_IMAGE_BAD, 2600);
      return null;
    }
    if (t.closed) {
      URL.revokeObjectURL(url);
      return null; // Done while it decoded
    }
    const img: TakeImage = {
      id: t.nextId++,
      seg: s.id,
      version,
      flip,
      file: imageName(t.images.length + 1, ext),
      mime,
      p0,
      video_t,
      box: placeBox(at ?? this.lastPointer ?? [0.5, 0.5], pic.naturalWidth, pic.naturalHeight, this.inkW, this.inkH),
      blob: file,
      url,
      upload: 'pending',
      stored: Promise.resolve(),
    };
    const rec = {
      take: t.id ?? '',
      version: String(t.version),
      file: img.file,
      mime,
      data: file,
      size: file.size,
      path: byLabel(this.versions, t.version)?.path ?? '',
      saved: Date.now(),
    };
    img.stored = this.#imgReady.then((store) => store.put(rec));
    t.images.push(img);
    t.dirty = true; // the next save sends it (within AUTOSAVE_MS): a wrong paste undone by then never leaves the page
    return img;
  };

  /** Resize one of this take's images by its corner, dragged to `p` (normalized). */
  resizeImage = (id: number, p: Point): void => {
    const t = this.take,
      img = t.images.find((x) => x.id === id);
    if (!img || img.undone || t.closed || !this.seg) return;
    img.box = resizeBox(img.box, p);
    t.dirty = true;
  };

  /** Files dragged over the page: never let the browser open them (the take would be lost); copy onto the video. */
  onDragOver = (e: DragEvent): void => {
    const dt = e.dataTransfer;
    if (this.kindShown) {
      // The extension's slot takes what it wants (it prevents the default); anywhere else nothing opens the file.
      if (dt && !e.defaultPrevented && dt.types.includes('Files')) {
        e.preventDefault();
        dt.dropEffect = 'none';
      }
      return;
    }
    if (!dt || !dragMayHoldImage(dt.types)) return;
    e.preventDefault();
    dt.dropEffect = this.#overStage(e) ? 'copy' : 'none';
  };

  onDrop = (e: DragEvent): void => {
    const dt = e.dataTransfer;
    if (this.kindShown) {
      if (dt?.types.includes('Files')) e.preventDefault(); // never opens the file over the page
      return;
    }
    if (!dt || !dragMayHoldImage(dt.types)) return;
    e.preventDefault();
    if (!this.#overStage(e)) return;
    const at = this.#norm(e),
      file = firstImageFile(dt);
    if (file) {
      void this.addImage(file, at);
      return;
    }
    if (!this.seg) {
      this.toast.say(this.canRecord ? HINT_IMAGE : VIEW_ONLY, 2600);
      return;
    }
    // An image dragged from another tab without a file: fetch it if its site allows that.
    const url = droppedImageUrl((type) => dt.getData(type));
    if (!url) {
      this.toast.say(MSG_IMAGE_NOFILE, 3000);
      return;
    }
    void fetch(url)
      .then((r) => (r.ok ? r.blob() : null))
      .catch(() => null)
      .then((b) => {
        if (b && b.type.startsWith('image/')) void this.addImage(b, at);
        else this.toast.say(MSG_IMAGE_NOFILE, 3000);
      });
  };

  /** Ctrl/Cmd+V with an image on the clipboard. */
  onPaste = (e: ClipboardEvent): void => {
    if (this.kindShown) return; // the extension's text takes it
    const file = firstImageFile(e.clipboardData);
    if (!file) return;
    e.preventDefault();
    void this.addImage(file, this.lastPointer);
  };

  /** The agent questions to show at playhead `vt`: in their range, or selected. Playing out of a selected one's range
   * lets it go. Assigned only when something changed. */
  #updateAsks(vt: number): void {
    if (this.kindShown) {
      if (this.shownAsks.length) this.shownAsks = [];
      this.#askSig = '';
      return;
    }
    const out: { row: Row; pulse: number }[] = [];
    const playing = !this.video.paused;
    for (const row of this.versionRows) {
      const ask = row.ask;
      if (!ask) continue;
      const inside = inRange(ask, vt);
      if (inside && !this.#askIn.has(ask.id)) this.#askIn.set(ask.id, (this.#askPulses.get(ask.id) ?? 0) + 1);
      if (!inside) this.#askIn.delete(ask.id);
      if (inside) this.#askPulses.set(ask.id, this.#askIn.get(ask.id)!);
      if (this.selectedAsk === ask.id && !inside && playing) this.selectedAsk = null;
      if (inside || this.selectedAsk === ask.id) out.push({ row, pulse: this.#askPulses.get(ask.id) ?? 0 });
    }
    const sig = out.map((a) => `${a.row.key}:${a.row.ask!.id}:${a.pulse}`).join('|');
    if (sig === this.#askSig) return;
    this.#askSig = sig;
    this.shownAsks = out;
  }

  /** Click a question's marker or time: go to its moment and keep its visuals on the video. */
  selectAsk = (row: Row): void => {
    if (!row.ask) return;
    if (row.ask.anchor || this.kindShown) {
      this.selectedAsk = row.ask.id; // a question about a document: no moment to go to, its place in the slot
      this.reveal(row.ask.anchor);
      return;
    }
    this.video.pause();
    this.selectedAsk = row.ask.id;
    this.jumpTo(row.ask.t);
  };

  /** Answer an agent's question with typed words or a tapped choice: back to the same task. */
  answerAsk = async (row: Row, message: string): Promise<boolean> => {
    const ask = row.ask;
    if (!ask) return false;
    const ok = await this.automation.send(`/tasks/${encodeURIComponent(ask.task)}/answer`, {
      question: ask.id,
      message,
    });
    if (ok) {
      if (this.selectedAsk === ask.id) this.selectedAsk = null;
      await this.notes.refresh();
    } else this.toast.say(this.automation.error || 'Could not send your answer. Try again.', 5000);
    return ok;
  };

  /** The image cards to show at playhead `vt`: like strokes, within ±SHOW s of their time, dimmer when not recording.
   * Only assigned when something changed, so the layer re-renders only then. */
  #updateCards(vt: number): void {
    const dim = this.seg ? 1 : 0.85,
      out: ImageCard[] = [],
      t = this.take;
    const add = (im: HistoryImage, key: string, id: number | null) => {
      if (im.undone) return;
      const a = Math.round(alphaAt(im, vt) * dim * 100) / 100;
      if (a > 0.02) out.push({ key, url: im.url, box: im.box, alpha: a, id: a > 0.3 ? id : null });
    };
    this.hist.images.forEach((im, i) => add(im, `h${i}`, null));
    for (const im of t.images) if (this.#onShown(im)) add(im, `t${t.id}:${im.id}`, this.seg ? im.id : null);
    const sig = out.map((c) => `${c.key} ${c.alpha} ${c.box.join(',')} ${c.id}`).join('|');
    if (sig === this.#cardSig) return;
    this.#cardSig = sig;
    this.cards = out;
  }

  // ---------------------------------------------------------------- notes sidebar
  taskOf(row: Row): EditingTask | undefined {
    return this.tasks[row.key];
  }

  statusOf(row: Row): CommentStatus {
    return this.statuses[row.key] ?? commentStatus(row);
  }

  /** The pen strokes drawn for a comment (its card's sketch), in drawing order. */
  strokesOf(row: Row): HistoryStroke[] {
    if (row.strokes) return row.strokes; // a typed comment's own lines
    void this.historyTakeIds; // re-read once an earlier take's strokes arrive
    return [...this.hist.strokes, ...this.take.strokes].filter((st) => !st.undone && covers(row, st, this.take.id));
  }

  get sidebar(): SidebarView {
    const n = this.notes,
      rows = this.versionRows;
    const shown = presence({
      available: n.available,
      content: true,
      everShown: n.everShown,
      open: n.open,
      phoneOpen: n.phoneOpen || this.commentsPinned,
      phone: this.phone,
    });
    return {
      ...shown,
      // Pinned (a phone in portrait, on a video), the comments are always in view: no bubble over the video.
      toggle: shown.toggle && !this.commentsPinned,
      phone: this.phone,
      rows: rows.filter((row) => {
        const query = this.commentSearch.trim().toLowerCase();
        return (
          (!query || `${row.text} ${row.by} ${row.change ?? ''}`.toLowerCase().includes(query)) &&
          (this.statusOf(row).state === 'done') === (this.commentFilter === 'done')
        );
      }),
      active: this.activeRow,
    };
  }

  /** N, the pill, or the column's ›. Nothing without the notes routes (so the remembered state isn't flipped blind).
   * Where the comments are pinned (a phone in portrait) it goes to them: To do, from the top. */
  toggleSidebar = (): void => {
    if (!this.notes.available) return;
    if (this.commentsPinned) {
      this.showComments('todo');
      if (typeof document !== 'undefined') document.querySelector('#side .vp')?.scrollTo?.({ top: 0 });
      return;
    }
    this.notes.toggle(this.phone);
  };

  /** Seek and wait until the frame is there (or a short while). */
  async #seekSettled(t: number): Promise<void> {
    const video = this.video;
    this.seekTo(t);
    if (!video.seeking) return;
    await new Promise((res) => {
      const done = () => {
        clearTimeout(timer);
        res(null);
      };
      const timer = setTimeout(done, 2000);
      video.addEventListener('seeked', done, { once: true });
    });
  }

  /** Click a sidebar item: jump to its moment. */
  jumpTo = (t: number): void => {
    this.seekTo(t);
  };

  /** Show version `label` at the same time, as a comparison (C goes back to the base); the base itself flips back.
   * The take stays open, so the reviewer can answer right after comparing. */
  peek = async (label: string): Promise<void> => {
    if (this.switching || label === this.shown) return;
    if (label === this.base) {
      if (this.flipped) await this.flip();
      return;
    }
    const target = byLabel(this.versions, label);
    if (!target || !(await this.showVersion(label))) return;
    this.flipped = true;
    this.logEv('version', { shown: this.shown, base: this.base, flip: this.flipped });
    this.toast.say(flipToast(this.versions, this.base, target, true), 1200);
  };

  /** "see it in vN": that note's moment, in the version that answered it. */
  seeIn = async (label: string, t: number): Promise<void> => {
    if (this.switching) return;
    await this.#seekSettled(t);
    await this.peek(label);
  };

  /** Answer a question: jump to it and record, exactly like R, with the segment pointing at the thread. Pressed again
   * while that answer is recording, it stops (like R). */
  answer = async (thread: number, t: number): Promise<void> => {
    if (this.switching || this.starting) return;
    if (this.seg) {
      const same = this.seg.answer_to === thread;
      await this.stopRec();
      if (same) return;
    }
    await this.#seekSettled(t);
    await this.startRec(thread);
  };

  reopen = async (item: Pick<NoteItem, 'id'>): Promise<void> => {
    if (!(await this.notes.reopen(item.id))) this.toast.say("Couldn't reopen that note", 3000);
  };

  /**
   * A `version` event (spec/bridge.md, "Streaming"). `progress` moves a streamed version's chip and coverage in place
   * (no refetch); anything else (`push`, `rendering`, `stopped`, `complete`, `failed`) reloads the family.
   */
  onVersionEvent = (e: VersionEvent): void => {
    if (e.why === 'progress' && e.label && e.stream) {
      if (!byLabel(this.versions, e.label)) {
        if (!this.#refreshing) void this.refreshFamily(); // its arrival is still on the way
        return;
      }
      this.#applyStream(e.label, e.stream);
      return;
    }
    if (e.why === 'failed' && e.label) this.toast.say(failedToast(e.label, e.error), 5000);
    void this.refreshFamily(e);
  };

  /** A streamed version's state from a progress event or a watch answer: the chip and the coverage move in place, and
   * a version on screen that waited for its first segment loads now. */
  #applyStream(label: string, s: StreamState): void {
    const vs = withStream(this.versions, label, s);
    if (vs !== this.versions) this.versions = this.info.versions = vs as Version[];
    this.#afterStream();
    void this.followCurrent();
  }

  /** After the family's stream states changed: load a version that waited for its first segment, and watch what's on
   * screen while it's incomplete (and stop once it's complete). */
  #afterStream(): void {
    const v = this.shownVersion();
    const wait = this.#posterOnly;
    if (wait && v && wait.label === v.label && (!incomplete(v) || (renderedEnd(v) ?? 0) > 0)) void this.#loadWaiting();
    const base = byLabel(this.versions, this.base);
    const latest =
      this.followLatest && base && !base.kind
        ? this.versions
            .filter((candidate) => !candidate.kind && verName(candidate.label) === verName(base.label))
            .at(-1)
        : this.versions.at(-1);
    this.watcher.watch(
      this.followLatest && latest && incomplete(latest) ? latest.label : v && incomplete(v) ? v.label : null,
    );
  }

  /** The family changed (a version was pushed, or a stream started, stopped, completed or failed). The video on
   * screen is never reloaded: a completed stream's player sees the playlist's end by itself. */
  refreshFamily(e: VersionEvent = {}): Promise<void> {
    // One at a time, in order: the answer for an older event never lands after a newer one (a stale state).
    const run: Promise<void> = (this.#refreshing ?? Promise.resolve())
      .then(() => this.#refreshFamily(e))
      .finally(() => {
        if (this.#refreshing === run) this.#refreshing = null;
      });
    this.#refreshing = run;
    return run;
  }

  async #refreshFamily(e: VersionEvent): Promise<void> {
    // A stream starting or stopping changes no manifest: the maps and the sidebar stay. Anything else may (a new
    // version, or a stream that completed and now has its manifest, so its parent's notes carry over).
    const light = e.why === 'rendering' || e.why === 'stopped';
    if (!light) this.maps.clear();
    try {
      const info = (await (await fetch('api/info')).json()) as Info;
      if (info.versions?.length) {
        this.info.versions = info.versions;
        this.versions = info.versions;
      }
      if (info.label && info.label === this.info.label) this.info.duration = info.duration;
      this.#announceStreams();
      if (this.video?.readyState >= 1) this.syncDuration();
      const base = byLabel(this.versions, this.base);
      const latest = this.versions.filter((candidate) => !candidate.kind).at(-1);
      if (base && !base.kind && latest && verName(latest.label) !== verName(base.label)) this.#rememberShown();
      this.#afterStream();
      await this.followCurrent();
      if (!light) this.#prefetchMaps();
    } catch {
      /* server gone */
    }
    if (!light) void this.notes.refresh();
  }

  /** Says once that a streamed version was pushed, and how to get to it. */
  #announceStreams(): void {
    const fresh = newlyStreamed(this.versions, this.#seenStreams);
    for (const v of fresh) this.#seenStreams.add(v.label);
    // A live version's next revision is no news: its comments say what landed (spec/automation.md).
    const v = fresh.filter((f) => !this.#announced.has(verName(f.label))).at(-1);
    for (const f of fresh) this.#announced.add(verName(f.label));
    if (v) this.toast.say('Updating video…', 2500);
  }
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, not state
  #announced = new Set<string>();

  /** A line or image of the open take shows on the version it was drawn on only: flipped with C to the other version,
   * the take's lines on the base wait for C back (another version's drawings never show on this one). */
  #onShown = (x: { version?: string | null }): boolean => !x.version || x.version === this.shown;

  /** Following the latest (spec/automation.md, "Versions versus drafts"). It waits while recording and while C
   * compares (flipped): a new draft never pulls the video away under the reviewer; C back catches up. */
  async followCurrent(): Promise<void> {
    while (this.followLatest && !this.flipped && !this.finishing && !this.seg && !this.starting && !this.switching) {
      const base = byLabel(this.versions, this.base);
      const allCuts = this.versions.filter((v) => !v.kind);
      // Once a video is selected, revisions of that logical version are the only automatic changes. A new v2 must
      // not stop v1-draft revisions from arriving, or pull a reviewer into v2. A v0 extension still enters v1.
      const choices =
        this.base && base && !base.kind ? allCuts.filter((v) => verName(v.label) === verName(base.label)) : allCuts;
      const current = latestPlayable(choices, this.video.currentTime);
      if (!current || current.label === this.shown) return;
      await this.setBase(current.label);
      if (this.shown !== current.label) return;
      // More segments or a newer revision can arrive while the media element is switching. Catch up now;
      // those events already happened, so waiting for another event can leave the viewer on the old cut.
    }
  }

  /** Takes finished with Send: their /done says `send: true` (also when it's retried later). */
  /** Takes finished by Send, and the agent that send picked ("" = the configured one). */
  #sendOnDone = new WeakMap<Take, string>();

  async #postDone(t: Take): Promise<void> {
    if (!t.id) return;
    const status = await this.notes.postDone(t.id, t.version, this.#sendOnDone.has(t), this.#sendOnDone.get(t));
    if (status < 200 || status >= 300) {
      this.#donePending.add(t);
      this.#unsaved.add(t);
      throw new Error('Feedback saved; processing could not start. Retrying…');
    }
    if (t === this.take && t.closed && !t.saveErr) this.doneSaved = true;
  }

  // ---------------------------------------------------------------- timeline: playhead, where you talked and drew
  #tlT(clientX: number): number {
    const r = this.el.tl.getBoundingClientRect();
    return tlT(clientX, r.left, r.width, this.mediaDuration());
  }

  attachTimeline = (el: HTMLElement) => {
    const down = (e: PointerEvent) => {
      if ((e.target as Element).closest('button')) return;
      this.scrubbing = true;
      el.setPointerCapture(e.pointerId);
      this.scrubber.start(this.#clampT(this.#tlT(e.clientX)));
    };
    const move = (e: PointerEvent) => {
      const r = this.el.tl.getBoundingClientRect();
      this.tip.show = true;
      const time = this.#tlT(e.clientX);
      const key = (e.target as Element).closest<HTMLElement>('[data-comment]')?.dataset.comment;
      this.hoverRows = this.timelineComments
        .filter(
          (row) =>
            time >= row.t0 - (this.mediaDuration() * 9) / r.width &&
            time <= row.t1 + (this.mediaDuration() * 9) / r.width,
        )
        .sort((a, b) => Number(b.key === key) - Number(a.key === key) || Math.abs(a.t0 - time) - Math.abs(b.t0 - time))
        .slice(0, 3);
      const half = Math.min(160, r.width / 2);
      this.tip.left = clamp(e.clientX - r.left, half, r.width - half);
      this.tip.text = fmt(time);
      if (this.scrubbing) this.scrubber.to(this.#clampT(this.#tlT(e.clientX)));
    };
    const up = () => {
      if (!this.scrubbing) return;
      this.scrubbing = false;
      this.scrubber.end();
    };
    const leave = () => {
      this.tip.show = false;
      this.hoverRows = [];
    };
    el.addEventListener('pointerdown', down);
    el.addEventListener('pointermove', move);
    el.addEventListener('pointerup', up);
    el.addEventListener('pointercancel', up);
    el.addEventListener('pointerleave', leave);
    return () => {
      el.removeEventListener('pointerdown', down);
      el.removeEventListener('pointermove', move);
      el.removeEventListener('pointerup', up);
      el.removeEventListener('pointercancel', up);
      el.removeEventListener('pointerleave', leave);
    };
  };

  // ---------------------------------------------------------------- versions: C flips, the picker switches
  /** The matching moment, same play state; the last frame stays up while the other file loads. The moment comes
   * through the time map between the two versions (spec/bridge.md); without one it's the same time. */
  async showVersion(label: string): Promise<boolean> {
    const video = this.video,
      v = byLabel(this.versions, label);
    if (!v || this.switching) return false;
    if (v.kind) {
      // An extension's version (spec/extensions.md, "Version kinds"): nothing to play, its slot shows it.
      if (!video.paused) video.pause();
      this.#landNote = null;
      this.#posterOnly = null;
      this.offline = false;
      this.freezeShown = false;
      this.#source.clear();
      this.paused = true;
      this.shown = label;
      void this.notes.load(label);
      this.#afterStream();
      return true;
    }
    this.switching = true;
    this.#landNote = null;
    // On a stream's poster (nothing rendered yet) the moment and the play state are the ones it waits with.
    const wait = this.#posterOnly;
    const from = wait ? wait.t : video.currentTime;
    this.frozenT = from;
    const segs = this.kindShown ? null : await this.maps.get(this.shown, label); // from a kind's version: no map
    const wasPlaying = wait ? wait.play : !video.paused,
      land = landing(v, from, segs);
    if ('refuse' in land) {
      this.switching = false;
      this.toast.say(land.refuse, 2600);
      return false;
    }
    const t = land.t;
    this.#landNote = land.note ?? null;
    this.frozenT = from;
    this.#posterOnly = null;
    this.offline = false; // the other version may play; if it's offline too, its error says so
    this.#setPoster(v);
    if (incomplete(v) && (renderedEnd(v) ?? 0) <= 0) {
      // A stream with nothing rendered yet (spec/bridge.md, "Streaming"): its poster at once, and watching it starts
      // its render; the video loads once the first segment is out (#loadWaiting).
      if (wasPlaying) video.pause();
      this.#source.clear();
      this.#posterOnly = { label, t, play: wasPlaying };
      this.paused = !wasPlaying;
      this.shown = label;
      void this.notes.load(label);
      this.#prefetchMaps();
      this.switching = false;
      this.#afterStream();
      return true;
    }
    const fz = this.el.freeze;
    if (video.videoWidth) {
      fz.width = video.videoWidth;
      fz.height = video.videoHeight;
      fz.getContext('2d')?.drawImage(video, 0, 0);
      this.freezeShown = true;
    }
    if (wasPlaying) video.pause();
    try {
      await this.#source.load(v);
      await this.#landAt(t, incomplete(v));
      this.shown = label;
      this.#watched.add(label);
      void this.notes.load(label); // the sidebar follows the version on screen
      this.#prefetchMaps();
      if (wasPlaying) await video.play().catch(() => {});
    } catch {
      if (v.relay) {
        // It plays from the editing computer, which may be offline: it's on screen with its poster, saying so.
        this.#posterOnly = { label, t, play: false };
        this.shown = label;
        this.#watched.add(label);
        void this.notes.load(label);
        void this.#checkOffline(v);
        return true;
      }
      this.toast.say(`Could not load ${verName(label)}`, 3000);
      return false;
    } finally {
      this.freezeShown = false;
      this.switching = false;
      this.#afterStream();
    }
    return true;
  }

  /** Seeks the freshly loaded video to `t`. A stream still filling in may take a while to have that frame, so then
   * the switch doesn't wait for it for more than 5 s. */
  async #landAt(t: number, patient: boolean): Promise<void> {
    const video = this.video;
    const seeked = new Promise((res) => video.addEventListener('seeked', res, { once: true }));
    video.currentTime = Math.min(t, (video.duration || t) - 0.001);
    await (patient ? Promise.race([seeked, new Promise((res) => setTimeout(res, 5000))]) : seeked);
  }

  /** The video's poster: a streamed version's first frame, shown until its video has a frame. */
  #setPoster(v: Pick<Version, 'poster' | 'label'> | undefined): void {
    if (v?.poster) this.video.poster = v.poster;
    else if (this.homeCtx) this.video.poster = v ? `api/poster?v=${encodeURIComponent(v.label)}` : 'api/poster';
    else this.video.removeAttribute('poster');
  }

  /** The version on its poster has a segment now: load it, land where it waits, and play if it should. */
  async #loadWaiting(): Promise<void> {
    const wait = this.#posterOnly,
      v = wait && byLabel(this.versions, wait.label);
    if (!wait || !v || this.shown !== wait.label || this.#loadingWait || this.switching || this.offline) return;
    this.#loadingWait = true;
    const video = this.video;
    try {
      await this.#source.load(v);
      if (this.#posterOnly !== wait) return; // the page moved on meanwhile
      await this.#landAt(wait.t, true);
      if (this.#posterOnly !== wait) return;
      this.#posterOnly = null;
      if (wait.play) await video.play().catch(() => {});
      this.paused = video.paused;
    } catch {
      /* superseded by another version, or it won't load: the video's error handler says so */
    } finally {
      this.#loadingWait = false;
    }
  }

  /** Fetches the time maps from the version on screen to the one C goes to next and its neighbors (the picker's
   * usual picks), so they're here in time. */
  #prefetchMaps(): void {
    if (this.kindShown) return;
    for (const label of [
      neighbor(this.cuts, this.shown, -1)?.label,
      neighbor(this.cuts, this.shown, 1)?.label,
      flipTarget(this.cuts, this.base, this.flipped)?.label,
    ]) {
      if (label) this.maps.peek(this.shown, label);
    }
  }

  /** C: flip between the base and its partner (versions.ts, `partner`), at the matching moment. The take stays
   * open. While flipped, following the latest waits; C back lands on the base, or, when following the latest (or the
   * base was a draft the family stopped listing), on the newest version or draft that came meanwhile. */
  flip = async (): Promise<void> => {
    if (this.switching) return;
    const newer = this.flipped ? catchUp(this.cuts, this.base, this.followLatest, this.video.currentTime) : null;
    if (newer) {
      await this.setBase(newer.label);
      return;
    }
    const target = flipTarget(this.cuts, this.base, this.flipped);
    if (!target) {
      this.toast.say('Only one version');
      return;
    }
    if (!(await this.showVersion(target.label))) return;
    this.flipped = !this.flipped;
    this.logEv('version', { shown: this.shown, base: this.base, flip: this.flipped });
    const note = this.#landNote;
    this.toast.say(note ?? flipToast(this.cuts, this.base, target, this.flipped), note ? 3200 : 1200);
  };

  /** Make `label` the base: stops any recording; the next R starts a fresh take on that version. */
  async setBase(label: string): Promise<void> {
    if (this.switching || (label === this.base && !this.flipped)) return;
    const v = byLabel(this.versions, label);
    if (!v) return;
    const was = verName(this.shown);
    // A quick check before a recording is stopped for nothing, with the map if it's here already (it's fetched ahead,
    // see #prefetchMaps): showVersion, which starts the switch at once, has the last word.
    const land =
      v.kind || this.kindShown ? { t: 0 } : landing(v, this.video.currentTime, this.maps.peek(this.shown, label));
    if ('refuse' in land) {
      this.toast.say(land.refuse, 2600);
      return;
    }
    const t = this.take;
    if (this.seg) await this.stopRec();
    if (!(await this.showVersion(label))) return;
    if (t.id && !t.closed) {
      t.closed = true;
      void this.save(t);
      this.#unsaved.add(t);
      this.#donePending.add(t); // a finished take, like Done: transcribe it once it is saved
    }
    this.base = label;
    this.flipped = false;
    this.#rememberShown();
    this.newTake();
    if (v.kind) return; // an extension's version: no drawings to load, nothing to say
    void this.loadHistory(label);
    const note = this.#landNote;
    // The live version's next revision (the agent's next edit) comes in quietly: its comment moving to Done is the news.
    if (note || verName(label) !== was) this.toast.say(note ?? 'Video updated', note ? 3200 : 1400);
  }

  pickVersion = (label: string): void => {
    this.verOpen = false;
    // A live version plays its newest revision that has the moment (spec/automation.md, "The live version").
    const v = byLabel(this.versions, label);
    if (v && !v.kind) label = revisionOf(this.cuts, verName(label), this.video?.currentTime ?? 0)?.label ?? label;
    this.followLatest = label === this.versions.at(-1)?.label || verName(label) === verName(this.cuts.at(-1)?.label);
    void this.setBase(label);
  };

  /** The selected source survives reload without changing the URL shape for an untouched review. */
  #rememberShown(): void {
    if (typeof location === 'undefined' || !this.shown) return;
    const value = encodeURIComponent(this.shown);
    const search = /(?:[?&])v=/.test(location.search)
      ? location.search.replace(/([?&])v=[^&]*/, `$1v=${value}`)
      : `${location.search}${location.search ? '&' : '?'}v=${value}`;
    history.replaceState(history.state, '', `${location.pathname}${search}${location.hash}`);
  }

  /** A Done comment's "in v5": v5 (its newest revision while it's live) at that comment's moment, mapped from the
   * comment's version (spec/notes.md, "What a version shows"). */
  showIn = async (row: Row, version: string): Promise<void> => {
    const v = revisionOf(this.cuts, version, row.t0);
    if (!v) return;
    this.jumpToComment(row);
    if (v.label === this.shown) return;
    this.verOpen = false;
    this.followLatest = verName(v.label) === verName(this.cuts.at(-1)?.label);
    await this.setBase(v.label);
  };

  jumpToComment = (row: Row): void => {
    this.video.pause();
    const strokes = [...this.hist.strokes, ...this.take.strokes].filter(
      (st) =>
        (st.take === row.take || (row.take === this.take.id && !st.take)) &&
        !st.undone &&
        st.video_t >= row.t0 - 2.5 &&
        st.video_t <= row.t1 + 2.5,
    );
    const closest = strokes.sort((a, b) => Math.abs(a.video_t - row.t0) - Math.abs(b.video_t - row.t0))[0];
    this.jumpTo(row.drawings.length && closest ? closest.video_t : row.t0);
  };

  /** A comment card is navigation; its controls, links, editor and a text selection keep their own jobs. */
  jumpFromComment = (row: Row, event: MouseEvent): void => {
    const target = event.target as Element | null;
    if (target?.closest('button, a, input, textarea, select, [contenteditable="true"], [data-part~="menu"]')) return;
    if (getSelection()?.toString()) return;
    this.jumpToComment(row);
  };

  openViewTarget = (): void => {
    const target = this.viewTarget;
    if (target) this.pickVersion(target);
  };

  get visibleHistoryStrokes(): HistoryStroke[] {
    const rows = this.sidebar.rows;
    return this.hist.strokes.filter(
      (st) =>
        !this.rows.some((row) => row.take === st.take) ||
        rows.some((row) => row.take === st.take && st.video_t >= row.t0 - 2.5 && st.video_t <= row.t1 + 2.5),
    );
  }

  async loadHistory(label: string): Promise<void> {
    const request = ++this.#historyRequest;
    try {
      // The version's own takes only (another version's drawings never show on this one), and its drafts' takes.
      const answers = await Promise.all(
        historyLabels(this.rows, label).map(async (v, i) => {
          const res = await fetch(`api/history?v=${encodeURIComponent(v)}`);
          if (!res.ok && i === 0) throw new Error('history');
          return res.ok ? ((await res.json()) as { history?: HistoryEntry[] }).history : [];
        }),
      );
      const all = answers.flat().filter((h): h is HistoryEntry => !!h);
      const d = { history: all.filter((h, i) => all.findIndex((o) => o.id === h.id) === i) };
      if (request !== this.#historyRequest || label !== this.shown) return;
      this.hist.strokes = [];
      this.hist.images = [];
      this.hist.bins = new Float32Array(this.nBins());
      this.historyVoice = [];
      this.historyTakeIds = [];
      this.transcribedTakes = (d.history ?? []).filter((h) => h.transcribed).map((h) => h.id);
      this.transcribedClips = (d.history ?? []).flatMap((h) =>
        (h.voice_tracks ?? []).filter((track) => track.transcribed).map((track) => `${h.id}/${track.seg}`),
      );
      this.addHistory(d.history?.filter((h) => h.id !== this.take.id));
    } catch {
      /* retain the previous history while offline */
    }
  }

  addHistory(list: HistoryEntry[] | undefined): void {
    const hist = this.hist;
    const voices: VoiceTrack[] = [];
    const ids: string[] = [];
    for (const h of list || []) {
      ids.push(h.id);
      const pairs = h.voice?.bins ?? [];
      const count = pairs.reduce((n, [i]) => Math.max(n, i + 1), this.nBins());
      const bins = new Float32Array(count);
      for (const [i, v] of pairs) bins[i] += v;
      if (h.voice_tracks?.length) {
        for (const track of h.voice_tracks) {
          const trackBins = new Float32Array(count);
          for (const [i, v] of track.voice.bins ?? []) if (i < count) trackBins[i] = v;
          voices.push({ take: h.id, seg: track.seg, bins: trackBins, transcribed: track.transcribed });
        }
      } else voices.push({ take: h.id, bins, transcribed: h.transcribed });
      for (const st of h.strokes || []) hist.strokes.push({ ...st, take: h.id, sourceVersion: h.source_version });
      for (const im of h.images || []) if (im && im.url && Array.isArray(im.box)) hist.images.push(im);
      for (const [i, v] of (h.voice && h.voice.bins) || []) {
        if (i >= hist.bins.length) {
          const a = new Float32Array(i + 64);
          a.set(hist.bins);
          hist.bins = a;
        }
        hist.bins[i] += v;
      }
    }
    this.historyVoice = [...this.historyVoice, ...voices];
    this.historyTakeIds = [...this.historyTakeIds, ...ids];
  }

  // ---------------------------------------------------------------- chrome
  /**
   * Pick the layout now and whenever the window or the pointer changes (a phone turning). The choice goes on <html> as
   * data-layout too, for the phone styles and for app.css, which keeps a phone's prerendered page hidden until then.
   */
  watchLayout(): void {
    const coarse = matchMedia('(pointer: coarse)');
    const pick = () => {
      this.layoutMode = layoutMode({ width: innerWidth, height: innerHeight, coarse: coarse.matches });
      document.documentElement.dataset.layout = this.layoutMode;
      document.documentElement.toggleAttribute('data-phone', this.touch);
      this.viewport.update();
    };
    pick();
    this.viewport.start();
    addEventListener('resize', pick);
    coarse.addEventListener('change', pick);
    // Safari's pinch gestures: the phone layout never zooms (the pen and the controls are sized for a finger).
    const noZoom = (e: Event) => {
      if (this.touch) e.preventDefault();
    };
    document.addEventListener('gesturestart', noZoom);
    document.addEventListener('gesturechange', noZoom);
  }

  setColor = (c: PenColor): void => {
    this.penColor = c;
    this.eraser = false;
  };

  /** Send (or save): stop, save and close the take, then POST /done (with `send`, the notes go to the agent). Resolves
   * to whether the take was finished now. */
  done = async (send = false, agent = ''): Promise<boolean> => {
    if (this.finishing) return false;
    const t = this.take;
    if (!t.id || t.closed) {
      const open = this.versionRows.filter((row) => row.mark !== 'answered').length;
      this.toast.say(
        this.notes.job
          ? 'Your feedback is saved. Transcription is still processing.'
          : this.versionRows.length
            ? `Your feedback is already saved. ${open} comments still need edits.`
            : 'Press Record to leave feedback. It saves automatically.',
        5000,
      );
      return false;
    }
    this.finishing = true;
    if (send) this.#sendOnDone.set(t, agent);
    try {
      this.video.pause();
      if (this.seg || this.starting) await this.stopRec();
      t.closed = true;
      await this.save(t);
      while (t.saving) await t.saving;
      if (t.saveErr) {
        this.#unsaved.add(t);
        this.#donePending.add(t);
        this.toast.say(MSG_SAVE_FAILED, 5000);
        return false;
      }
      this.#doneCall = this.#postDone(t);
      await this.#doneCall;
      this.doneSaved = true;
      if (!send) this.toast.say('Saved. Your comments stay open until they are edited.', 5000);
      if (this.homeCtx) void this.loadHome();
      return true;
    } catch (err) {
      this.toast.say((err as Error).message, 5000);
      return false;
    } finally {
      this.closeMic();
      this.finishing = false;
    }
  };

  // ---------------------------------------------------------------- the home (spec/home.md)
  /** Reads the home: whether there is one, and the next project to review. */
  async loadHome(): Promise<void> {
    const ctx = this.homeCtx;
    if (!ctx) return;
    try {
      const r = await fetch(`${ctx.home}api/home`);
      if (!r.ok) return;
      const h = normalizeHome(await r.json());
      const me = h.projects.find((c) => c.name === ctx.project);
      this.homeMe = me ?? null;
      const titleOf = (path: string) => (path ? (findNode(h.tree, path)?.title ?? path.split('/').pop()!) : h.title);
      // The next one to review: from the same project first, then any.
      const mine = me ? h.projects.filter((c) => c.project === me.project) : [];
      this.homeNext = nextToReview(mine, ctx.project) ?? nextToReview(h.projects, ctx.project);
      this.homeCrumb = me ? crumb(titleOf(me.project), videoName(me)) : [h.title || 'All videos'];
      let storage: Storage | null = null;
      try {
        storage = localStorage;
      } catch {
        /* none here */
      }
      const place = me ? backPlace(rememberedPlace(storage, ctx.home), me) : '';
      // The top has no hash: the home then shows what it remembers, which is the top (backPlace says so).
      this.homeBack = { href: ctx.home + hashOf(place), title: titleOf(place) || 'All videos' };
      this.homeAvailable = true;
    } catch {
      /* no home here, or the server is gone */
    }
  }

  /** "Next video" and >: a take in progress is closed and saved like Done, then the next project to review opens. */
  goNext = async (): Promise<void> => {
    const ctx = this.homeCtx;
    if (!ctx || this.#leaving || this.starting) return;
    this.#leaving = true;
    let going = false;
    try {
      const t = this.take;
      if (t.id && !t.closed) {
        await this.done();
        if (t.saveErr) return; // its toast says so; the take stays on this page
      }
      await Promise.race([this.#doneCall, new Promise((res) => setTimeout(res, 3000))]);
      await this.loadHome();
      const next = this.homeNext;
      if (!next) {
        if (this.homeAvailable) this.toast.say(NO_MORE, 2600);
        return;
      }
      going = true;
      location.assign(projectUrl(ctx.home, next.name));
    } finally {
      if (!going) this.#leaving = false;
    }
  };

  run(act: Action): void {
    const video = this.video;
    switch (act) {
      case 'save':
        void this.save();
        break;
      case 'undo':
        this.undo();
        break;
      case 'togglePlay':
        this.togglePlay();
        break;
      case 'slower':
        this.slower();
        break;
      case 'faster':
        this.faster();
        break;
      case 'stop':
        this.stop();
        break;
      case 'toggleRec':
        this.toggleRec();
        break;
      case 'back':
        this.seekTo(video.currentTime - SEEK);
        break;
      case 'forward':
        this.seekTo(video.currentTime + SEEK);
        break;
      case 'framePrev':
        this.frameStep(-1);
        break;
      case 'frameNext':
        this.frameStep(1);
        break;
      case 'escape':
        this.drawing = null;
        this.verOpen = false;
        this.keysOpen = false;
        this.pickerOpen = false;
        this.penOpen = false;
        if (this.draft) this.cancelDraft();
        break;
      case 'help':
        this.keysOpen = !this.keysOpen;
        this.dismissHint();
        break;
      case 'flip':
        void this.flip();
        break;
      case 'color1':
      case 'color2':
      case 'color3':
      case 'color4':
        this.setColor(COLOR_KEYS[Number(act.slice(5)) - 1]);
        break;
      case 'eraser':
        this.toggleEraser();
        break;
      case 'fullscreen':
        if (document.fullscreenElement) void document.exitFullscreen();
        else document.documentElement.requestFullscreen().catch(() => {});
        break;
      case 'notes':
        this.toggleSidebar();
        break;
      case 'next':
        void this.goNext();
        break;
    }
  }

  /** window keydown, capture phase: the player's keys work whatever has focus, an extension's slot too, except in a
   * text field. On an extension's version (no video) only R, Esc and the page's own keys are core's: Esc in its text
   * leaves the text (then R talks), and the video's keys go to the page as plain keys. */
  onKeydown = (e: KeyboardEvent): void => {
    const target = e.target as Element | null;
    if (target?.closest?.('[data-part~="feedback"]')) return;
    if (target?.closest?.(TYPING) && !((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's')) {
      if (e.key === 'Escape' && target.closest?.('[data-part~="keys-sheet"]')) {
        this.run('escape');
        return;
      }
      if (e.key === 'Escape' && this.kindShown && target.closest(TEXT)) (target as HTMLElement).blur?.();
      return;
    }
    if (target?.closest?.('.comment-marker, .drawing-marker') && e.key === ' ') return;
    // ←/→ on the comments column's handle resize it (Sidebar.svelte), they don't seek.
    if (target?.closest?.('[data-part~="sidebar-grip"]') && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) return;
    // Enter with a pending comment (lines drawn, say): its box takes the words; Enter there saves it.
    if (
      e.key === 'Enter' &&
      this.draft &&
      !e.repeat &&
      !(e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) &&
      !target?.closest?.('button, a, [role=button]')
    ) {
      e.preventDefault();
      this.focusComment();
      return;
    }
    const r = keyAction(e, this.shortcuts);
    if (!r) return;
    if (this.kindShown) {
      const key = { key: e.key, ctrlKey: e.ctrlKey, metaKey: e.metaKey, altKey: e.altKey, repeat: false };
      const act = r.act ?? keyAction(key)?.act;
      if (!act || !KIND_KEYS.has(act)) return; // no video here: a plain key of the page
    }
    e.preventDefault();
    // Escape also reaches an open menu or popover (Bits UI closes them on it), wherever the focus is.
    if (r.stop && e.key !== 'Escape') e.stopPropagation();
    if (r.act) this.run(r.act);
  };

  /** Clicking a button must not focus it, so Space and the other keys keep working. */
  onDocMousedown = (e: MouseEvent): void => {
    const target = e.target as Element | null;
    if (target?.closest?.('[data-part~="feedback"]')) return;
    if (target?.closest?.('[data-select-trigger], [data-select-content]')) return;
    // Player buttons deliberately keep focus off themselves. Release a previously focused menu
    // so clicking Record or a timecode also restores the player's keyboard shortcuts.
    const focused = document.activeElement;
    if (focused instanceof HTMLElement && focused.matches('[data-select-trigger]')) focused.blur();
    if (target?.closest?.('button')) e.preventDefault();
  };

  // ---------------------------------------------------------------- frame loop and timers
  #ticks: { key: unknown[]; hist: HistoryStroke[]; take: HistoryStroke[] } | null = null;
  /** The pen ticks no comment stands for yet (lib/timeline.ts, pendingStrokes), recomputed only when they change. */
  #pendingTicks(): { histStrokes: HistoryStroke[]; takeStrokes: HistoryStroke[] } {
    const hist = this.visibleHistoryStrokes,
      key = [
        this.rows,
        this.take,
        this.take.strokes.length,
        hist.length,
        this.hist.strokes,
        this.commentFilter,
        this.commentSearch,
      ];
    const c = this.#ticks;
    if (!c || c.key.some((k, i) => k !== key[i])) {
      this.#ticks = {
        key,
        hist: pendingStrokes(hist, this.rows, this.take.id),
        take: pendingStrokes(this.take.strokes, this.rows, this.take.id),
      };
    }
    return { histStrokes: this.#ticks!.hist, takeStrokes: this.#ticks!.take };
  }

  #drawTimeline(vt: number): void {
    const tl = this.el.tl;
    if (!tl) return; // between two layouts
    const dpr = devicePixelRatio || 1,
      w = tl.clientWidth,
      h = tl.clientHeight;
    if (tl.width !== Math.round(w * dpr) || tl.height !== Math.round(h * dpr)) {
      tl.width = Math.round(w * dpr);
      tl.height = Math.round(h * dpr);
    }
    const g = tl.getContext('2d') as CanvasRenderingContext2D;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    const md = this.mediaDuration(),
      rendered = renderedEnd(this.shownVersion());
    drawTimeline(g, w, h, {
      // an incomplete stream's length is known before its video is (its poster shows until the first segment)
      d: this.video.duration || rendered !== null ? md : 0,
      xd: md || 1,
      rendered,
      vt,
      recording: !!this.seg,
      takeBins: pendingVoiceBins(
        this.take.segments.map((seg) => ({
          take: this.take.id ?? '',
          seg: seg.id,
          bins: seg.voiceBins ?? new Float32Array(0),
          active: seg === this.seg,
          transcribed:
            this.transcribedTakes.includes(this.take.id ?? '') ||
            this.transcribedClips.includes(`${this.take.id}/${seg.id}`),
        })),
        this.rows,
      ),
      histBins: this.pendingHistoryVoice,
      ...this.#pendingTicks(),
      knob: this.touch ? 8 : undefined,
      colors: palette(),
    });
  }

  #drawWave(): void {
    const wave = this.el.wave;
    if (!wave) return; // between two layouts
    const dpr = devicePixelRatio || 1,
      w = wave.clientWidth,
      h = wave.clientHeight;
    if (wave.width !== Math.round(w * dpr)) {
      wave.width = Math.round(w * dpr);
      wave.height = Math.round(h * dpr);
    }
    const g = wave.getContext('2d') as CanvasRenderingContext2D;
    g.setTransform(dpr, 0, 0, dpr, 0, 0);
    g.clearRect(0, 0, w, h);
    drawWave(g, w, h, this.seg ? this.bars : null, palette());
  }

  loop = (): void => {
    const t = now(),
      dt = Math.min(0.1, t - this.lastFrame),
      video = this.video,
      vt = this.switching ? this.frozenT : video.currentTime;
    this.lastFrame = t;
    if (!this.switching) {
      this.listen(dt, vt);
      if (!video.seeking) this.prevVT = vt;
    }
    const ink = this.el.ink;
    renderInk(
      this.#ictx,
      this.#lctx,
      ink.width,
      ink.height,
      [
        ...this.visibleHistoryStrokes,
        ...this.typedStrokes,
        ...this.take.strokes.filter(this.#onShown),
        ...this.draftStrokes.filter(this.#onShown),
      ],
      vt,
      this.seg ? 1 : 0.85,
      this.drawing,
    );
    this.#drawTimeline(vt);
    this.#drawWave();
    this.#updateCards(vt);
    this.#updateAsks(vt);
    const rows = this.versionRows,
      act = this.kindShown ? this.activeRow : activeKey(rows, vt);
    if (act !== this.activeRow) this.activeRow = act;
    this.#tellExtensions(vt);
    if (!this.notes.everShown && this.notes.available && (this.rows.length || this.notes.job))
      this.notes.everShown = true;
    if (this.touch)
      this.flipBlocked = flipRefusal(this.cuts, this.base, this.flipped, vt, (label, t) =>
        mapTime(this.maps.peek(this.shown, label), t),
      );
    this.curText = fmt(vt);
    this.curShort = fmt(vt, false);
    if (this.seg) {
      this.recElapsed = fmt(t - this.#recordingStart, false);
      // Record's ring follows your voice: the mic is working.
      const level = this.bars[this.bars.length - 1]?.[0] ?? 0;
      this.el.rec?.style.setProperty('--level', Math.min(1, Math.max(0, level)).toFixed(2));
    }
    requestAnimationFrame(this.loop);
  };

  /** Start the page: called once the markup is mounted and `el` is filled in. */
  async start(): Promise<void> {
    const video = this.video;
    this.#source = new VideoSource(video, { onFatal: () => this.onVideoError() });
    this.#storeReady = openChunkStore().then((s) => (this.#store = s));
    this.#imgReady = openImageStore().then((s) => (this.#imgStore = s));
    this.notes.restore();
    const narrow = matchMedia(PHONE_QUERY);
    this.narrow = narrow.matches;
    narrow.addEventListener('change', () => (this.narrow = narrow.matches));
    this.newTake();
    this.paused = video.paused;
    setInterval(() => {
      const t = this.take;
      if (t.id && !t.closed && !video.paused && !video.seeking) this.logEv('tick'); // keeps the clocks locked
    }, TICK_MS);
    setInterval(() => {
      const t = this.take;
      if (t.id && !t.closed && (t.dirty || this.seg)) void this.save(t);
      for (const c of this.#unsaved) {
        if (c.saving) continue;
        if (c.saveErr) void this.save(c);
        else {
          this.#unsaved.delete(c);
          if (this.#donePending.delete(c)) void this.#postDone(c).catch(() => {});
        }
      }
    }, AUTOSAVE_MS);
    setInterval(() => {
      fetch('api/ping').catch(() => {});
    }, PING_MS);
    new ResizeObserver(this.layout).observe(this.el.stage);
    // In a home, the first frame the home showed is the poster while the video loads (spec/home.md).
    this.homeCtx = homeContext(location.pathname);
    if (this.homeCtx) video.poster = 'api/poster';
    void this.extLoader.list().then((list) => {
      this.exts = list;
      this.extsLoaded = true;
    });
    // The chat's thread and its document's changes (spec/chat.md).
    this.notes.on('chat', this.chat.onEvent);
    this.notes.on('ext.files', this.chat.onFiles);

    try {
      this.info = (await (await fetch('api/info')).json()) as Info;
    } catch {
      this.toast.say('Review server not reachable', 8000);
    }
    document.title = reviewTitle(
      pageName({ label: this.info.label ?? '', name: this.info.name }, this.info.versions || []),
      brandOf(),
    );
    this.role = this.info.role ?? null;
    this.mayDownload = this.info.downloads === true;
    // Shown once per browser, then it's behind ?. A viewer's "View only" always shows.
    if (!this.take.id) this.hintGone = this.canRecord && hintSeen();
    this.versions = this.info.versions || [];
    // Streams already there when the page opens show their chip; only ones pushed later are announced.
    for (const v of newlyStreamed(this.versions, this.#seenStreams)) this.#seenStreams.add(v.label);
    // The newest cut that plays, as always; a video with only an extension's version opens on it; a deep link
    // (`?v=<label>&t=<seconds>`, ctx.navigate) opens where it says.
    const link = deepLink();
    this.base = this.shown = openingVersion(
      this.versions.map((v) => ({
        label: v.label,
        kind: v.kind,
        playable: !incomplete(v) || (renderedEnd(v) ?? 0) > 0,
      })),
      this.info.label,
      link.v,
    );
    // Linked to an older cut (or v0 next to cuts): the page stays there.
    if (link.v === this.base && this.cuts.length && this.base !== this.cuts.at(-1)?.label) this.followLatest = false;
    const curV = byLabel(this.versions, this.base);
    if (this.base && !curV?.kind) this.#watched.add(this.base);
    this.take.version = this.base;
    if (this.base === this.info.label) this.addHistory(this.info.history);
    else if (this.base && !curV?.kind) void this.loadHistory(this.base);
    const cur = curV ?? (this.base ? { label: this.base, name: this.info.name } : null);
    if (curV?.kind) {
      video.removeAttribute('poster'); // an extension's version: nothing to play, its slot shows it
    } else {
      this.#setPoster(curV);
      if (curV && incomplete(curV) && (renderedEnd(curV) ?? 0) <= 0) {
        this.#posterOnly = { label: curV.label, t: 0, play: false }; // opened on a stream with nothing rendered yet
      } else
        this.#source
          .load(cur)
          .then(() => {
            if (link.t && this.shown === cur?.label) this.seekTo(link.t);
          })
          .catch(() => {}); // a failure shows the fatal message (video error event)
    }
    // Watch the stream on screen while the page is visible; say so at once when it's hidden or closed.
    document.addEventListener('visibilitychange', () => this.watcher.visibility(document.visibilityState !== 'hidden'));
    addEventListener('pageshow', (e) => {
      if (e.persisted) this.watcher.visibility(true); // back from the back-forward cache
    });
    this.#afterStream();
    this.layout();
    requestAnimationFrame(this.loop);
    if (this.canRecord) {
      // A viewer's link can't upload: whatever an earlier page left in the buffers waits for one that can.
      void this.recover().catch(() => {});
      void this.recoverImages().catch(() => {});
    }
    void this.notes.load(this.shown); // no sidebar at all if the server has no notes routes
    this.#prefetchMaps();
    void this.loadHome();
  }

  /** Resend images that earlier takes left in the image buffer, like recover() does for audio. */
  async recoverImages(): Promise<void> {
    const res = await recoverImages(
      await this.#imgReady,
      this.versions,
      async (url, body, mime) => {
        const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': mime }, body });
        await r.body?.cancel().catch(() => {});
        return r.status;
      },
      (take) => this.#ownTakes.has(take),
    );
    if (res.recovered || res.kept || res.dropped)
      console.info(`retake: image buffer: ${res.recovered} recovered, ${res.dropped} refused, ${res.kept} kept`);
    if (res.retry) setTimeout(() => void this.recoverImages().catch(() => {}), AUTOSAVE_MS);
  }
}
