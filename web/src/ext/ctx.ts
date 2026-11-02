// `ctx`, the page API an extension's mount gets (spec/extensions.md, "The page API"; its types are retake-ext.d.ts).
// Everything goes through the HTTP routes as the person viewing, so their role applies. The page that hosts a slot
// (the review page, the home) gives a Host: where things are, its toast, its events, recording and the player.
// Every subscription made through a ctx ends when its slot unmounts, whether or not the extension unsubscribes.
import type * as T from './retake-ext';
import type { ExtInfo } from './loader';
import { draftOf } from '../lib/versions';

/** What the hosting page gives a slot's ctx. */
export interface Host {
  /** The home, or a video's review page. */
  where: 'home' | 'video';
  /** The home's root: '' on the home itself (relative URLs), its path from a video's page ('/' or '/x/'). */
  readonly homeRoot: string;
  /** The video this page reviews. */
  readonly video?: T.VideoRef;
  /** The version on screen. */
  readonly version?: { label: string; kind?: string };
  readonly role: T.Role;
  /** This page load's id (the watch lease). */
  readonly viewer: string;
  /** The page slot's route (the hash after #ext/<id>/), and when it changes. */
  route?(): string;
  onRoute?(cb: (route: string) => void): () => void;
  toast(text: string): void;
  /** The page's own event stream (the review page's api/events): `notes`, `job`, `version`, `automation`, `ext.files`. */
  events?(name: string, cb: (data: unknown) => void): () => void;
  onRecorded?(cb: (a: T.Recorded) => void): () => void;
  recording?(): boolean;
  onRecording?(cb: (on: boolean) => void): () => void;
  /** The version slot says where a typed comment points (ctx.onAnchor). */
  onAnchor?(fn: () => T.Anchor | null): () => void;
  /** A comment's place was clicked: `{anchor}` for the version slot (ctx.on('reveal')). */
  onReveal?(cb: (data: { anchor: T.Anchor }) => void): () => void;
  /** sidebar-tab and review-panel on a video version. */
  player?(): T.Player | undefined;
  /** Shows another version of this page's video, and/or seeks. */
  show?(version: string | undefined, t: number | undefined): void;
  selectComment?(id: number): void;
  /** Leaves for a URL; sets the home's hash. */
  go?(url: string): void;
  setHash?(hash: string): void;
  fetch?: typeof fetch;
  /** An event stream of another video (files(video).watch from elsewhere). */
  eventSource?(url: string): EventSourceLike | null;
  setInterval?(fn: () => void, ms: number): unknown;
  clearInterval?(id: unknown): void;
  sleep?(ms: number): Promise<void>;
}

export interface EventSourceLike {
  addEventListener(type: string, cb: (e: MessageEvent) => void): void;
  close(): void;
}

/** How often the page renews a watch lease (the server keeps one 30 s). */
export const WATCH_RENEW_MS = 10_000;
/** How often transcribe() asks how its job is doing. */
export const JOB_POLL_MS = 1000;
/** transcribe() gives up after this many polls in a row that failed to answer. */
const JOB_POLL_FAILURES = 30;

/** What a call rejects with (retake-ext.d.ts, ExtError). */
export class ExtError extends Error implements T.ExtError {
  status: number;
  changed?: boolean;
  sha256?: string;
  hint?: string;
  constructor(msg: string, status: number, extra: { changed?: boolean; sha256?: string; hint?: string } = {}) {
    super(msg);
    this.name = 'ExtError';
    this.status = status;
    Object.assign(this, extra);
  }
}

const enc = encodeURIComponent;
const encPath = (p: string): string => p.split('/').map(enc).join('/');
const str = (v: unknown): string => (typeof v === 'string' ? v : '');

/** The version label that a video's notes belong to: a draft is its version's. */
const versionOf = (label: string): string => draftOf(label) ?? label;

/** `<path without its extension>.words.json`: where a transcription's words land. */
export const wordsPath = (path: string): string => path.replace(/\.[^./]*$/, '') + '.words.json';

/** A transcript's plain text: the words joined, spaces folded. */
export const wordsText = (words: readonly T.Word[]): string =>
  words
    .map((w) => w.w)
    .join(' ')
    .replace(/\s+/g, ' ')
    .replace(/\s+([,.!?;:])/g, '$1')
    .trim();

/** An anchor an extension gave, made safe: a file and a line from 1, the heading and quote when they're text (the
 * quote cut to 200 characters); null when it isn't one. */
export function validAnchor(a: unknown): T.Anchor | null {
  if (!a || typeof a !== 'object') return null;
  const x = a as Record<string, unknown>;
  const file = str(x.file);
  const line = typeof x.line === 'number' && Number.isFinite(x.line) ? Math.max(1, Math.floor(x.line)) : 1;
  if (!file) return null;
  const out: T.Anchor = { file, line };
  if (str(x.heading).trim()) out.heading = str(x.heading).trim();
  if (str(x.quote).trim()) out.quote = str(x.quote).trim().slice(0, 200);
  return out;
}

function validWords(body: unknown): T.Word[] {
  const ws = (body && typeof body === 'object' ? (body as { words?: unknown }).words : null) ?? [];
  if (!Array.isArray(ws)) return [];
  return ws.filter(
    (w): w is T.Word =>
      !!w && typeof w === 'object' && typeof w.w === 'string' && typeof w.s === 'number' && typeof w.e === 'number',
  );
}

/** A ctx and what ends it. */
export interface Made {
  ctx: T.Ctx;
  dispose(): void;
}

/** Builds the ctx of one mount of `slot` of `ext`. */
export function makeCtx(ext: ExtInfo, slot: T.SlotName, host: Host): Made {
  const f: typeof fetch = host.fetch ?? ((...a) => fetch(...a));
  const every = host.setInterval ?? ((fn: () => void, ms: number) => setInterval(fn, ms));
  const stop = host.clearInterval ?? ((id: unknown) => clearInterval(id as ReturnType<typeof setInterval>));
  const sleep = host.sleep ?? ((ms: number) => new Promise<void>((res) => setTimeout(res, ms)));
  const id = enc(ext.id);
  const subs = new Set<() => void>();
  let disposed = false;

  /** Keeps an unsubscribe until the slot unmounts; calling the returned one ends it early. */
  const keep = (off: () => void): (() => void) => {
    if (disposed) {
      off();
      return () => {};
    }
    let done = false;
    const end = () => {
      if (done) return;
      done = true;
      subs.delete(end);
      off();
    };
    subs.add(end);
    return end;
  };

  /** An extension's callback: it may throw, and nothing of core's minds. */
  const safe =
    <A>(cb: (a: A) => void) =>
    (a: A): void => {
      if (disposed) return;
      try {
        cb(a);
      } catch (err) {
        console.error(`retake: the ${ext.name} extension's callback failed:`, err);
      }
    };

  const sameVideo = (video?: string): boolean => !video || (!!host.video && video === host.video.name);
  /** A video's root: this page's own (relative) or another video of the home. */
  const videoRoot = (video?: string): string => {
    if (sameVideo(video) && host.where === 'video') return '';
    if (!video) throw new ExtError('No video here: name one', 400);
    return `${host.homeRoot}p/${enc(video)}/`;
  };

  async function call(url: string, init?: RequestInit): Promise<Response> {
    let r: Response;
    try {
      r = await f(url, init);
    } catch (err) {
      throw new ExtError(err instanceof Error ? err.message : 'Not reachable', 0);
    }
    if (r.ok) return r;
    const body = (await r.json().catch(() => ({}))) as { error?: unknown; sha256?: unknown; hint?: unknown };
    if (r.status === 409 && body.error === 'changed')
      throw new ExtError('changed', 409, { changed: true, sha256: str(body.sha256) });
    throw new ExtError(str(body.error) || `${r.status} ${r.statusText}`.trim(), r.status, {
      hint: str(body.hint) || undefined,
    });
  }
  const json = (body: unknown): RequestInit => ({
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const ifMatch = (sha?: string): Record<string, string> =>
    sha ? { 'If-Match': sha === '*' ? '*' : `"${sha.replace(/^W\//, '').replace(/"/g, '')}"` } : {};

  // ---------------------------------------------------------------- events
  const streams = new Map<string, { es: EventSourceLike; cbs: Map<string, Set<(d: unknown) => void>> }>();
  /** Listen to an event of a video's stream: this page's own, or one opened for another video. */
  function listen(root: string, name: string, cb: (d: unknown) => void): () => void {
    if (root === '') return host.events ? host.events(name, cb) : () => {};
    let s = streams.get(root);
    if (!s) {
      const es =
        host.eventSource?.(`${root}api/events`) ??
        (typeof EventSource === 'undefined' ? null : new EventSource(`${root}api/events`));
      if (!es) return () => {};
      s = { es, cbs: new Map() };
      streams.set(root, s);
    }
    const stream = s;
    let set = stream.cbs.get(name);
    if (!set) {
      const cbs = new Set<(d: unknown) => void>();
      set = cbs;
      stream.cbs.set(name, cbs);
      stream.es.addEventListener(name, (e) => {
        let d: unknown = null;
        try {
          d = typeof e.data === 'string' && e.data ? JSON.parse(e.data) : null;
        } catch {
          /* not JSON */
        }
        for (const c of [...cbs]) c(d);
      });
    }
    set.add(cb);
    return () => {
      stream.cbs.get(name)?.delete(cb);
      if ([...stream.cbs.values()].every((x) => !x.size)) {
        stream.es.close();
        streams.delete(root);
      }
    };
  }

  // ---------------------------------------------------------------- files and the watch lease
  const leases = new Map<string, { cbs: Set<(c: T.FileInfo[]) => void>; timer: unknown; off: () => void }>();
  function watch(root: string, cb: (changed: T.FileInfo[]) => void): () => void {
    let lease = leases.get(root);
    if (!lease) {
      const cbs = new Set<(c: T.FileInfo[]) => void>();
      const renew = () => {
        void f(`${root}api/ext/${id}/watch?viewer=${enc(host.viewer)}`, { method: 'POST' })
          .then((r) => r.body?.cancel())
          .catch(() => {});
      };
      const off = listen(root, 'ext.files', (d) => {
        const data = d as { ext?: unknown; files?: unknown } | null;
        if (!data || data.ext !== ext.id || !Array.isArray(data.files)) return;
        for (const c of [...cbs]) c(data.files as T.FileInfo[]);
      });
      renew();
      lease = { cbs, timer: every(renew, WATCH_RENEW_MS), off };
      leases.set(root, lease);
    }
    const l = lease;
    l.cbs.add(cb);
    return () => {
      l.cbs.delete(cb);
      if (l.cbs.size) return;
      stop(l.timer);
      l.off();
      leases.delete(root);
    };
  }

  function files(video?: string): T.Files {
    const root = videoRoot(video);
    const base = `${root}api/ext/${id}`;
    const fileUrl = (path: string) => `${base}/files/${encPath(path)}`;
    return {
      async list(glob?: string) {
        const r = await call(glob ? `${base}/files?glob=${enc(glob)}` : `${base}/files`);
        const body = (await r.json()) as { files?: T.FileInfo[] };
        return Array.isArray(body.files) ? body.files : [];
      },
      async read(path: string) {
        const r = await call(fileUrl(path));
        const sha256 = (r.headers.get('ETag') ?? '').replace(/^W\//, '').replace(/"/g, '');
        const blob = await r.blob();
        return { sha256, text: () => blob.text(), blob: () => Promise.resolve(blob) };
      },
      async write(path: string, data: string | Blob, o: { ifMatch?: string } = {}) {
        const type = typeof data === 'string' ? 'text/plain; charset=utf-8' : data.type || 'application/octet-stream';
        const r = await call(fileUrl(path), {
          method: 'PUT',
          headers: { 'Content-Type': type, ...ifMatch(o.ifMatch) },
          body: data,
        });
        return (await r.json()) as T.FileInfo;
      },
      async remove(path: string, o: { ifMatch?: string } = {}) {
        const r = await call(fileUrl(path), { method: 'DELETE', headers: ifMatch(o.ifMatch) });
        await r.body?.cancel().catch(() => {});
      },
      url: fileUrl,
      watch: (cb) => keep(watch(root, safe(cb))),
      async history(path?: string) {
        const r = await call(path ? `${base}/history?path=${enc(path)}` : `${base}/history`);
        const body = (await r.json()) as { edits?: T.Edit[] };
        return Array.isArray(body.edits) ? body.edits : [];
      },
      async undo(editId: string, path: string) {
        const r = await call(`${base}/history/undo`, json({ id: editId, path }));
        return (await r.json()) as T.FileInfo;
      },
    };
  }

  // ---------------------------------------------------------------- comments
  /** The label of this extension's version of a video: the one on screen when it's this extension's kind. */
  const kindLabel = (video?: string): string => {
    const v = sameVideo(video) ? host.version : undefined;
    if (v?.kind && ext.kinds.some((k) => k.id === v.kind)) return v.label;
    return ext.kinds[0]?.folder || 'v0';
  };

  function comments(video?: string): T.Comments {
    const root = videoRoot(video);
    const all = async (version: string): Promise<T.Item[]> => {
      const r = await call(`${root}api/notes?v=${enc(version)}&scope=video`);
      const body = (await r.json()) as { items?: unknown };
      return Array.isArray(body.items) ? (body.items as T.Item[]) : [];
    };
    const self: T.Comments = {
      async list(version?: string) {
        const items = await all(version ?? (sameVideo(video) ? host.version?.label : undefined) ?? kindLabel(video));
        if (!version) return items;
        const on = versionOf(version);
        const notes = new Map(items.filter((i) => i.kind === 'note').map((i) => [i.id, i]));
        return items.filter((i) => {
          const own =
            (i.kind === 'question' && typeof i.note === 'number' ? notes.get(i.note)?.version : null) || i.version;
          return !own || versionOf(own) === on;
        });
      },
      async add(c) {
        const body: Record<string, unknown> = { version: kindLabel(video), text: c.text };
        if (c.anchor) body.anchor = c.anchor;
        if (c.wants) body.wants = c.wants;
        const r = await call(`${root}api/notes`, json(body));
        return (await r.json()) as T.Item;
      },
      async send(ids?: number[]) {
        let notes = ids;
        if (!notes) {
          const items = await self.list(kindLabel(video));
          notes = items
            .filter(
              (i) =>
                i.kind === 'note' &&
                i.status === 'open' &&
                !(i.work && i.work.state !== 'completed' && i.work.state !== 'cancelled'),
            )
            .map((i) => i.id);
        }
        if (!notes.length) return;
        const r = await call(`${root}api/automation/tasks`, json({ notes }));
        await r.body?.cancel().catch(() => {});
      },
      async answer(question: number, text: string) {
        const items = await all(kindLabel(video));
        const asked = items.find((i) => i.ask?.id === question)?.ask;
        let r: Response;
        if (asked)
          r = await call(`${root}api/automation/tasks/${enc(asked.task)}/answer`, json({ question, message: text }));
        else {
          const task = items.find((i) => i.id === question && i.kind === 'note' && i.work?.state === 'question')?.work;
          if (!task) throw new ExtError(`No open question ${question}`, 404);
          r = await call(`${root}api/automation/tasks/${enc(task.id)}/answer`, json({ message: text }));
        }
        await r.body?.cancel().catch(() => {});
      },
    };
    return self;
  }

  // ---------------------------------------------------------------- the rest
  async function transcribe(path: string, video?: string): Promise<{ text: string; words: T.Word[] }> {
    const root = videoRoot(video);
    const r = await call(`${root}api/ext/${id}/transcribe`, json({ path }));
    const job = str(((await r.json()) as { job?: unknown }).job);
    if (!job) throw new ExtError('No transcription job', 500);
    let failures = 0,
      words: string | undefined;
    for (;;) {
      await sleep(JOB_POLL_MS);
      if (disposed) throw new ExtError('The slot closed', 0);
      let j: { state?: unknown; error?: unknown; hint?: unknown; words_path?: unknown };
      try {
        j = (await (await call(`${root}api/ext/${id}/jobs/${enc(job)}`)).json()) as typeof j;
        failures = 0;
      } catch (err) {
        if (err instanceof ExtError && err.status === 404) throw new ExtError('The transcription job is gone', 404);
        if (++failures >= JOB_POLL_FAILURES) throw err;
        continue;
      }
      if (j.state === 'done') {
        words = str(j.words_path);
        break;
      }
      if (j.state === 'failed') {
        const hint = str(j.hint);
        throw new ExtError(hint || str(j.error) || 'Transcription failed', 0, { hint: hint || undefined });
      }
    }
    const read = await files(video).read(words || wordsPath(path));
    let body: unknown = null;
    try {
      body = JSON.parse(await read.text());
    } catch {
      /* no words */
    }
    const list = validWords(body);
    return { text: wordsText(list), words: list };
  }

  async function linkPreview(url: string): Promise<T.LinkPreview> {
    if (host.where !== 'video') throw new ExtError('No video here: link previews are read under a video', 400);
    const body = (await (await call(`api/link-preview?url=${enc(String(url))}`)).json()) as Record<string, unknown>;
    return {
      url: str(body.url) || String(url),
      title: str(body.title),
      site: str(body.site),
      image: /^https:\/\//i.test(str(body.image)) ? str(body.image) : '',
      description: str(body.description),
    };
  }

  async function createVideo(v: { project: string; name: string; files: Record<string, string> }) {
    const r = await call(`${host.homeRoot}api/ext/${id}/videos`, json(v));
    const body = (await r.json()) as { name?: unknown; url?: unknown };
    return { name: str(body.name) || v.name, url: str(body.url) };
  }

  function navigate(to: { video?: string; version?: string; t?: number; route?: string }): void {
    const t = typeof to.t === 'number' && isFinite(to.t) && to.t >= 0 ? to.t : undefined;
    const go = host.go ?? ((url: string) => location.assign(url));
    if (to.video && !(sameVideo(to.video) && host.where === 'video')) {
      const q = [to.version ? `v=${enc(to.version)}` : '', t !== undefined ? `t=${t}` : ''].filter(Boolean).join('&');
      go(`${host.homeRoot}p/${enc(to.video)}/${q ? `?${q}` : ''}`);
      return;
    }
    if (to.video || to.version || t !== undefined) {
      host.show?.(to.version, t);
      return;
    }
    if (to.route === undefined) return;
    const hash = `#ext/${ext.id}/${to.route.replace(/^\/+/, '')}`;
    if (host.where === 'home') (host.setHash ?? ((h: string) => (location.hash = h)))(hash);
    else go(`${host.homeRoot}${hash}`);
  }

  const player = slot === 'sidebar-tab' || slot === 'review-panel' ? host.player?.() : undefined;
  const ctx: T.Ctx = {
    api: 1,
    slot,
    ext: { id: ext.id, name: ext.name, version: ext.version, base: ext.base, machine: ext.machine },
    role: host.role,
    get route() {
      return slot === 'page' ? (host.route?.() ?? '') : '';
    },
    onRoute: (cb) => keep(slot === 'page' && host.onRoute ? host.onRoute(safe(cb)) : () => {}),
    ...(host.where === 'video' && host.video ? { video: { ...host.video } } : {}),
    ...(host.where === 'video' && host.version ? { version: { ...host.version } } : {}),
    async home() {
      return (await (await call(`${host.homeRoot}api/home`)).json()) as T.Home;
    },
    files,
    comments,
    createVideo,
    transcribe,
    onRecorded: (cb) => keep(host.onRecorded ? host.onRecorded(safe(cb)) : () => {}),
    recording: () => !!host.recording?.(),
    onRecording: (cb) => keep(host.onRecording ? host.onRecording(safe(cb)) : () => {}),
    onAnchor(fn) {
      if (slot !== 'version' || !host.onAnchor || typeof fn !== 'function') return () => {};
      const ask = (): T.Anchor | null => {
        if (disposed) return null;
        try {
          return validAnchor(fn());
        } catch (err) {
          console.error(`retake: the ${ext.name} extension's anchor failed:`, err);
          return null;
        }
      };
      return keep(host.onAnchor(ask));
    },
    selectComment: (n) => host.selectComment?.(n),
    ...(player
      ? {
          player: {
            time: () => player.time(),
            duration: () => player.duration(),
            seek: (s: number) => player.seek(s),
            onTime: (cb: (t: number) => void) => keep(player.onTime(safe(cb))),
          },
        }
      : {}),
    on(event, cb) {
      if (event === 'reveal') return keep(host.onReveal ? host.onReveal(safe(cb)) : () => {});
      const name = event === 'files' ? 'ext.files' : event;
      const own = safe(cb);
      const filtered =
        event === 'files'
          ? (d: unknown) => {
              if ((d as { ext?: unknown } | null)?.ext === ext.id) own(d);
            }
          : own;
      return keep(host.events ? host.events(name, filtered) : () => {});
    },
    navigate,
    toast: (text) => host.toast(String(text)),
    fetch: (path, init) =>
      f(`${host.where === 'video' ? '' : host.homeRoot}ext/${id}/api/${path.replace(/^\/+/, '')}`, init),
    linkPreview,
  };

  return {
    ctx,
    dispose() {
      if (disposed) return;
      disposed = true;
      for (const end of [...subs]) end();
      for (const s of streams.values()) s.es.close();
      streams.clear();
    },
  };
}
