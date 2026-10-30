// The review page's state and behavior: a port of the legacy player's script, function for function. Values the
// markup shows are runes ($state); the hot per-frame data (take, strokes, voice bins, waveform) stays plain, and the
// canvases are drawn every animation frame, as before.
import { memoryChunkStore, openChunkStore, recoverPending, type ChunkStore } from './audio-buffer';
import {
  AUTOSAVE_MS,
  HINT_DRAW,
  HINT_DRAW_TOUCH,
  MSG_SAVE_FAILED,
  MSG_SAVED,
  MSG_SAVED_TOUCH,
  PING_MS,
  SEEK,
  TICK_MS,
  type PenColor,
  COLOR_KEYS,
} from './constants';
import { keyAction, type Action } from './keys';
import { layoutMode, type LayoutMode } from './layout';
import { closeMic, MIC_BLOCKED, MIC_BLOCKED_TOUCH, openMic, pickMime, recorderMime, type Mic } from './mic';
import { addPoint, fitFrame, isClick, renderInk } from './pen';
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
import { drawTimeline, tlT } from './timeline';
import { Toast } from './toast.svelte';
import type {
  HistoryEntry,
  HistoryStroke,
  Info,
  Point,
  Segment,
  Stroke,
  Take,
  TakeData,
  TakeEvent,
  Version,
} from './types';
import { byLabel, coverMessage, covers, flipRefusal, flipTarget, flipToast, stepTarget } from './versions';
import { addVoice, drawWave, levelDb, newAcc, newVad, pushBars, vadStep, type WaveBar } from './voice';
import { clamp, fmt, isoLocal, now, r3, stamp } from './util';

export interface Elements {
  stage: HTMLElement;
  video: HTMLVideoElement;
  freeze: HTMLCanvasElement;
  ink: HTMLCanvasElement;
  tl: HTMLCanvasElement;
  wave: HTMLCanvasElement;
}

/** The test hook (`window.__review`): the same shape as the legacy page's, see spec/api.md. */
export interface ReviewHook {
  readonly take: Take;
  readonly seg: Segment | null;
  readonly view: { base: string | null; shown: string | null; flipped: boolean; switching: boolean };
  readonly info: Info;
  readonly hist: { strokes: HistoryStroke[]; bins: Float32Array };
  readonly vad: { floor: number; hang: number; active: boolean };
  readonly bars: WaveBar[];
  readonly mic: Mic | null;
  serialize(t: Take): TakeData;
  save(t?: Take): Promise<void> | undefined;
}

const FATAL = (name: string) =>
  `This file won't play in the browser. Make an H.264 copy first: ffmpeg -i "${name}" -c:v libx264 -crf 18 -c:a aac review.mp4`;

export class Review {
  // ---------------------------------------------------------------- what the markup shows
  paused = $state(true);
  curText = $state('0:00.0');
  durText = $state('0:00');
  /** The recording segment in progress. */
  seg = $state.raw<Segment | null>(null);
  starting = $state(false);
  recElapsed = $state('0:00');
  penColor = $state<PenColor>('red');
  versions = $state.raw<Version[]>([]);
  base = $state<string | null>(null);
  shown = $state<string | null>(null);
  flipped = $state(false);
  verOpen = $state(false);
  hintGone = $state(false);
  doneSaved = $state(false);
  fatal = $state('');
  freezeShown = $state(false);
  tip = $state<{ show: boolean; left: number | null; text: string }>({ show: false, left: null, text: '' });
  readonly toast = new Toast();
  /** Desktop (the bar), or the phone layout in portrait or landscape. The prerendered page is desktop. */
  layoutMode = $state<LayoutMode>('desktop');
  /** The video's aspect ratio (CSS), for the phone layout's stage. */
  aspect = $state('16 / 9');
  /** What C would say if it were refused now, else null: the phone layout's flip button shows it disabled. */
  flipBlocked = $state<string | null>(null);

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
  hist: { strokes: HistoryStroke[]; bins: Float32Array } = { strokes: [], bins: new Float32Array(1) };
  inkW = 1;
  inkH = 1;
  scrubbing = false;

  el = {} as Elements;
  #ictx!: CanvasRenderingContext2D;
  #layer!: HTMLCanvasElement;
  #lctx!: CanvasRenderingContext2D;
  #source!: VideoSource;
  /** The audio buffer. Memory until IndexedDB has opened (every chunk waits for `#storeReady`). */
  #store: ChunkStore = memoryChunkStore();
  #storeReady: Promise<ChunkStore> = Promise.resolve(this.#store);
  /** Phone layout, while recording: the screen stays on (iOS locks after 30 s without a touch, and the mic with it). */
  #awake = false;
  #wakeLock: WakeLockSentinel | null = null;
  /** Takes started on this page: the saver owns their uploads, so recovery leaves them alone. */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #ownTakes = new Set<string>();
  /** Closed takes (Done, or a version step) whose last save failed: autosave keeps trying them. */
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  readonly #unsaved = new Set<Take>();
  readonly #saver = new Saver({
    serialize: (t) => this.serialize(t),
    toast: (m, ms) => this.toast.say(m, ms),
    store: () => this.#store,
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
      serialize: (t: Take) => r.serialize(t),
      save: (t?: Take) => r.save(t),
    };
  })();

  // ---------------------------------------------------------------- take, segments, clocks, event log
  nBins(): number {
    return binCount(this.video?.duration || this.info.duration || 1);
  }

  newTake(): void {
    this.take = newTake(this.base, this.nBins());
    this.doneSaved = false;
  }

  ensureTake(): void {
    if (this.take.closed) {
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
      duration: (v.duration || this.info.duration) as number,
      width: v.videoWidth,
      height: v.videoHeight,
      // eslint-disable-next-line svelte/prefer-svelte-reactivity -- a timestamp, not state
      date: new Date(),
    });
  };

  // ---------------------------------------------------------------- saving (automatic)
  save = (t: Take | null = this.take): Promise<void> | undefined => this.#saver.save(t);

  /** Page closing: best-effort keepalive upload of the last few seconds. */
  flushOnExit = (): void => {
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

  startRec = async (): Promise<void> => {
    if (this.seg || this.starting) return;
    this.starting = true;
    this.hintGone = true;
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
      this.#keepAwake(true);
      this.bars = [];
      this.recElapsed = fmt(0, false);
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
    } catch (err) {
      this.toast.say('Could not start recording: ' + ((err as Error)?.message || err), 6000);
    } finally {
      this.starting = false;
    }
  };

  stopRec = (): Promise<unknown> | null => {
    const s = this.seg,
      t = this.take;
    if (!s) return null;
    this.seg = null;
    this.#keepAwake(false);
    s.p1 = now();
    s.v1 = this.video.currentTime;
    this.logEv('rec_stop', { seg: s.id }, t);
    s.stopped = new Promise((res) => {
      s.rec.onstop = res;
    });
    s.rec.stop();
    void s.stopped.then(() => this.save(t));
    return s.stopped;
  };

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
    if (this.seg) this.stopRec();
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
    } else {
      this.vad.active = false;
      this.acc = newAcc();
    }
    if (this.vad.active && video.duration) {
      this.take.bins = grow(this.take.bins, this.nBins());
      addVoice(this.take.bins, dt, vt, this.prevVT, !video.paused, video.seeking);
    }
  }

  // ---------------------------------------------------------------- player
  togglePlay = (): void => {
    const video = this.video;
    if (video.paused || video.ended)
      video.play().catch((err: Error) => this.toast.say('Could not play: ' + err.message));
    else video.pause();
  };

  seekTo(t: number): void {
    const video = this.video,
      d = video.duration || 0;
    video.currentTime = clamp(t, 0, d ? d - 0.001 : Math.max(0, t));
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
    this.paused = this.video.paused;
  };
  onEnded = (): void => {
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
    this.layout();
    this.durText = fmt(this.video.duration, false);
    const { videoWidth: w, videoHeight: h } = this.video;
    if (w && h) this.aspect = `${w} / ${h}`;
    this.take.bins = grow(this.take.bins, this.nBins());
    this.hist.bins = grow(this.hist.bins, this.nBins());
  };
  onVideoError = (): void => {
    this.fatal = FATAL(this.info.name);
  };

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
      if (!s) {
        if (!this.drawHintShown) {
          this.toast.say(this.touch ? HINT_DRAW_TOUCH : HINT_DRAW, 2600);
          this.drawHintShown = true;
        }
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
      const st = this.drawing;
      if (!st) return;
      const co = e.getCoalescedEvents ? e.getCoalescedEvents() : [];
      for (const ce of co.length ? co : [e]) addPoint(st.points, this.#norm(ce), this.inkW, this.inkH);
    };
    const up = () => {
      const st = this.drawing,
        t = this.take;
      if (!st) return;
      this.drawing = null;
      if (isClick(st.points, this.inkW, this.inkH)) return; // a click draws nothing
      st.id = t.nextId++;
      st.p1 = now();
      st.video_t_end = this.video.currentTime;
      insertEv(t, { p: st.p0, video_t: st.video_t, event: 'draw_start', id: st.id, seg: st.seg, color: st.color });
      this.logEv('draw_end', { id: st.id, seg: st.seg });
      t.strokes.push(st);
    };
    const cancel = () => {
      this.drawing = null;
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

  undo = (): void => {
    const t = this.take;
    if (t.closed) return;
    const st = [...t.strokes].reverse().find((x) => !x.undone);
    if (!st) return;
    st.undone = true;
    this.logEv('undo', { id: st.id });
  };

  // ---------------------------------------------------------------- timeline: playhead, where you talked and drew
  #tlT(clientX: number): number {
    const r = this.el.tl.getBoundingClientRect();
    return tlT(clientX, r.left, r.width, this.video.duration || this.info.duration || 0);
  }

  attachTimeline = (el: HTMLElement) => {
    const down = (e: PointerEvent) => {
      this.scrubbing = true;
      el.setPointerCapture(e.pointerId);
      this.seekTo(this.#tlT(e.clientX));
    };
    const move = (e: PointerEvent) => {
      const r = this.el.tl.getBoundingClientRect();
      this.tip.show = true;
      this.tip.left = clamp(e.clientX - r.left, 24, r.width - 24);
      this.tip.text = fmt(this.#tlT(e.clientX));
      if (this.scrubbing) this.seekTo(this.#tlT(e.clientX));
    };
    const up = () => {
      this.scrubbing = false;
    };
    const leave = () => {
      this.tip.show = false;
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

  // ---------------------------------------------------------------- versions: [ ] steps, C flips to the partner
  /** Same time, same play state; the last frame stays up while the other file loads. */
  async showVersion(label: string): Promise<boolean> {
    const video = this.video,
      v = byLabel(this.versions, label),
      t = video.currentTime,
      wasPlaying = !video.paused;
    if (!v || this.switching) return false;
    if (!covers(v, t)) {
      this.toast.say(coverMessage(v), 2600);
      return false;
    }
    this.switching = true;
    this.frozenT = t;
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
      const seeked = new Promise((res) => video.addEventListener('seeked', res, { once: true }));
      video.currentTime = Math.min(t, (video.duration || t) - 0.001);
      await seeked;
      this.shown = label;
      if (wasPlaying) await video.play().catch(() => {});
    } catch {
      this.toast.say(`Could not load ${label}`, 3000);
      return false;
    } finally {
      this.freezeShown = false;
      this.switching = false;
    }
    return true;
  }

  /** C: flip between the base and its partner (the next newer version, else the previous one). */
  flip = async (): Promise<void> => {
    if (this.switching) return;
    const target = flipTarget(this.versions, this.base, this.flipped);
    if (!target) {
      this.toast.say('Only one version');
      return;
    }
    if (!(await this.showVersion(target.label))) return;
    this.flipped = !this.flipped;
    this.logEv('version', { shown: this.shown, base: this.base, flip: this.flipped });
    this.toast.say(flipToast(this.versions, this.base, target, this.flipped), 1200);
  };

  /** Make `label` the base: stops any recording; the next R starts a fresh take on that version. */
  async setBase(label: string): Promise<void> {
    if (this.switching || (label === this.base && !this.flipped)) return;
    const v = byLabel(this.versions, label);
    if (!v) return;
    if (!covers(v, this.video.currentTime)) {
      this.toast.say(coverMessage(v), 2600);
      return;
    }
    const t = this.take;
    if (this.seg) await this.stopRec();
    if (!(await this.showVersion(label))) return;
    if (t.id && !t.closed) {
      t.closed = true;
      void this.save(t);
      this.#unsaved.add(t);
    }
    this.base = label;
    this.flipped = false;
    this.newTake();
    void this.loadHistory(label);
    this.toast.say(`Now on ${label}`, 1400);
  }

  stepBase(dir: number): void {
    const n = stepTarget(this.versions, this.base, dir);
    if (typeof n === 'string') this.toast.say(n);
    else void this.setBase(n.label);
  }

  pickVersion = (label: string): void => {
    this.verOpen = false;
    void this.setBase(label);
  };

  async loadHistory(label: string): Promise<void> {
    this.hist.strokes = [];
    this.hist.bins = new Float32Array(this.nBins());
    try {
      const d = (await (await fetch(`/api/history?v=${encodeURIComponent(label)}`)).json()) as {
        history?: HistoryEntry[];
      };
      this.addHistory(d.history);
    } catch {
      /* server gone */
    }
  }

  addHistory(list: HistoryEntry[] | undefined): void {
    const hist = this.hist;
    for (const h of list || []) {
      for (const st of h.strokes || []) hist.strokes.push(st);
      for (const [i, v] of (h.voice && h.voice.bins) || []) {
        if (i >= hist.bins.length) {
          const a = new Float32Array(i + 64);
          a.set(hist.bins);
          hist.bins = a;
        }
        hist.bins[i] += v;
      }
    }
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
    };
    pick();
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
  };

  done = async (): Promise<void> => {
    const t = this.take;
    if (!t.id || t.closed) {
      this.toast.say('Nothing recorded yet');
      return;
    }
    this.video.pause();
    if (this.seg) await this.stopRec();
    await this.save(t);
    t.closed = true;
    if (t.saveErr) this.#unsaved.add(t);
    this.closeMic();
    this.doneSaved = true;
    this.toast.say(t.saveErr ? MSG_SAVE_FAILED : this.touch ? MSG_SAVED_TOUCH : MSG_SAVED, 4000);
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
        break;
      case 'flip':
        void this.flip();
        break;
      case 'stepPrev':
        this.stepBase(-1);
        break;
      case 'stepNext':
        this.stepBase(1);
        break;
      case 'color1':
      case 'color2':
      case 'color3':
      case 'color4':
        this.setColor(COLOR_KEYS[Number(act.slice(5)) - 1]);
        break;
      case 'fullscreen':
        if (document.fullscreenElement) void document.exitFullscreen();
        else document.documentElement.requestFullscreen().catch(() => {});
        break;
    }
  }

  /** window keydown, capture phase: the player's keys work whatever has focus. */
  onKeydown = (e: KeyboardEvent): void => {
    const r = keyAction(e);
    if (!r) return;
    e.preventDefault();
    if (r.stop) e.stopPropagation();
    if (r.act) this.run(r.act);
  };

  /** Clicking a button must not focus it, so Space and the other keys keep working. */
  onDocMousedown = (e: MouseEvent): void => {
    if ((e.target as Element | null)?.closest?.('button')) e.preventDefault();
  };

  // ---------------------------------------------------------------- frame loop and timers
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
    drawTimeline(g, w, h, {
      d: this.video.duration || 0,
      xd: this.video.duration || this.info.duration || 1,
      vt,
      recording: !!this.seg,
      takeBins: this.take.bins,
      histBins: this.hist.bins,
      histStrokes: this.hist.strokes,
      takeStrokes: this.take.strokes,
      knob: this.touch ? 8 : undefined,
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
    drawWave(g, w, h, this.seg ? this.bars : null);
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
      [...this.hist.strokes, ...this.take.strokes],
      vt,
      this.seg ? 1 : 0.55,
      this.drawing,
    );
    this.#drawTimeline(vt);
    this.#drawWave();
    if (this.touch) this.flipBlocked = flipRefusal(this.versions, this.base, this.flipped, vt);
    this.curText = fmt(vt);
    if (this.seg) this.recElapsed = fmt(t - this.seg.p0, false);
    requestAnimationFrame(this.loop);
  };

  /** Start the page: called once the markup is mounted and `el` is filled in. */
  async start(): Promise<void> {
    const video = this.video;
    this.#source = new VideoSource(video, { onFatal: () => this.onVideoError() });
    this.#storeReady = openChunkStore().then((s) => (this.#store = s));
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
        else this.#unsaved.delete(c);
      }
    }, AUTOSAVE_MS);
    setInterval(() => {
      fetch('/api/ping').catch(() => {});
    }, PING_MS);
    new ResizeObserver(this.layout).observe(this.el.stage);

    try {
      this.info = (await (await fetch('/api/info')).json()) as Info;
    } catch {
      this.toast.say('Review server not reachable', 8000);
    }
    document.title = `Review · ${this.info.name}`;
    this.versions = this.info.versions || [];
    this.base = this.shown = this.info.label || null;
    this.take.version = this.base;
    this.addHistory(this.info.history);
    const cur = byLabel(this.versions, this.base) ?? (this.base ? { label: this.base, name: this.info.name } : null);
    this.#source.load(cur).catch(() => {}); // a failure shows the fatal message (video error event)
    this.layout();
    requestAnimationFrame(this.loop);
    void this.recover().catch(() => {});
  }
}
