// The chat with the agent on a kind's version (spec/chat.md): the thread, kept current by the page's `chat` events;
// sending a message (with images saved in the video's v0/images/ first, through the kind's extension's files), a
// voice message (saved in v0/voice/chat/, transcribed, its words sent), a tap on a question's choice, Stop, and Send ("make the video").
import type { Files, FileInfo } from '../../ext/retake-ext';
import {
  applyEvent,
  EMPTY,
  imageName,
  normalizeView,
  pickDoc,
  stampOf,
  type ChatMessage,
  type ChatView,
} from './thread';

/** The kind's extension, for its files and transcription. */
export interface ChatFiles {
  id: string;
  files: () => Files;
  transcribe: (path: string) => Promise<{ text: string }>;
}

export interface ChatDeps {
  fetch?: typeof fetch;
  /** The kind's extension on screen, or null when the version on screen has no chat. */
  ext: () => ChatFiles | null;
  /** The video's folder name, for its document `v0/<folder>_v0.md`. */
  folder: () => string;
  toast: (text: string) => void;
}

/** The largest image the composer saves (the files route's limit). */
export const MAX_IMAGE = 25 << 20;

/** A voice message on its way: saved, then transcribed, then sent. */
export interface PendingVoice {
  id: number;
  state: 'saving' | 'transcribing' | 'failed';
  text: string;
}

export class Chat {
  view = $state.raw<ChatView>(EMPTY);
  /** GET api/chat answered at least once. */
  loaded = $state(false);
  /** The page's own message is on its way. */
  posting = $state(false);
  /** The kind's document and its sha256 now, for Send ("nothing changed since the last Send"). */
  doc = $state.raw<{ path: string; sha256: string } | null>(null);
  voices = $state.raw<PendingVoice[]>([]);
  #req = 0;
  #voiceN = 0;

  constructor(private readonly deps: ChatDeps) {}

  #fetch(url: string, init?: RequestInit): Promise<Response> {
    return (this.deps.fetch ?? fetch)(url, init);
  }

  /** Reads the thread again. */
  refresh = async (): Promise<void> => {
    const n = ++this.#req;
    try {
      const r = await this.#fetch('api/chat');
      if (!r.ok || n !== this.#req) return;
      this.view = normalizeView(await r.json());
      this.loaded = true;
    } catch {
      /* the next event or reload tries again */
    }
  };

  /** A `chat` event of the page's stream. */
  onEvent = (d: unknown): void => {
    const next = applyEvent(this.view, d);
    if (next) this.view = next;
    else void this.refresh();
  };

  /** An `ext.files` event: the document's sha256 now. */
  onFiles = (d: unknown): void => {
    const data = d as { ext?: unknown; files?: FileInfo[] } | null;
    const ext = this.deps.ext();
    if (!ext || !data || data.ext !== ext.id || !Array.isArray(data.files)) return;
    const doc = this.doc;
    for (const f of data.files) {
      if (doc && f.path === doc.path && !f.deleted) this.doc = { path: f.path, sha256: f.sha256 };
    }
    if (!doc) void this.loadDoc();
  };

  /** Finds the document and its sha256. */
  loadDoc = async (): Promise<void> => {
    const ext = this.deps.ext();
    if (!ext) return;
    try {
      const list = (await ext.files().list('v0/*')).filter((f) => !f.deleted);
      const path = pickDoc(
        list.map((f) => f.path),
        this.deps.folder(),
      );
      const f = list.find((x) => x.path === path);
      this.doc = f ? { path: f.path, sha256: f.sha256 } : null;
    } catch {
      /* offline: Send says the script isn't loaded */
    }
  };

  /** Keeps an image in v0/images/ under a new name; returns its path. */
  saveImage = async (file: File): Promise<string | null> => {
    const ext = this.deps.ext();
    if (!ext) return null;
    if (file.size > MAX_IMAGE) {
      this.deps.toast(`${file.name || 'The image'} is over 25 MB`);
      return null;
    }
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- a timestamp, not state
    const stamp = stampOf(new Date());
    for (let n = 1; n < 10; n++) {
      const path = `v0/images/${imageName(file, stamp, n)}`;
      try {
        await ext.files().write(path, file, { ifMatch: '*' });
        return path;
      } catch (e) {
        if ((e as { changed?: boolean }).changed) continue;
        this.deps.toast(`The image wasn’t saved: ${(e as Error).message || e}`);
        return null;
      }
    }
    return null;
  };

  /** Sends a message. The words, and the paths of images saved already. */
  post = async (
    text: string,
    o: { images?: string[]; voice?: string; answer?: number; agent?: string } = {},
  ): Promise<boolean> => {
    const t = text.trim();
    if (!t) return false;
    this.posting = true;
    try {
      const body: Record<string, unknown> = { text: t };
      if (o.images?.length) body.images = o.images;
      if (o.voice) body.voice = o.voice;
      if (o.answer) body.answer = o.answer;
      if (o.agent) body.agent = o.agent;
      const r = await this.#fetch('api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const b = (await r.json().catch(() => ({}))) as { message?: ChatMessage; error?: string };
      if (!r.ok) {
        this.deps.toast(b.error || 'Not sent. Try again.');
        return false;
      }
      if (b.message) this.onEvent({ why: 'message', message: b.message });
      return true;
    } catch {
      this.deps.toast('Not sent: the review isn’t reachable. Try again.');
      return false;
    } finally {
      this.posting = false;
    }
  };

  /** Stops the agent's reply. */
  stop = async (): Promise<void> => {
    try {
      const r = await this.#fetch('api/chat/stop', { method: 'POST' });
      if (!r.ok && r.status !== 409) this.deps.toast('Couldn’t stop the agent. Try again.');
    } catch {
      this.deps.toast('Couldn’t stop the agent: the review isn’t reachable.');
    }
  };

  /** Send on the chat's version: "I like it, make the video". Returns the version it goes into, or null. */
  send = async (agent = ''): Promise<string | null> => {
    const doc = this.doc;
    if (!doc) return null;
    try {
      const r = await this.#fetch('api/chat/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(agent ? { file: doc.path, agent } : { file: doc.path }),
      });
      const b = (await r.json().catch(() => ({}))) as { target?: string; error?: string };
      if (!r.ok) {
        this.deps.toast(b.error || 'Couldn’t send. Try again.');
        return null;
      }
      void this.refresh();
      return b.target ?? '';
    } catch {
      this.deps.toast('Couldn’t send: the review isn’t reachable.');
      return null;
    }
  };

  /** A voice message: the recording is saved as v0/voice/chat/<stamp>.<ext> (apart from a script's own talk-outs,
   * which it puts into the document itself), transcribed, and its words are sent. */
  voice = async (a: { blob: Blob; type: string }, agent = ''): Promise<void> => {
    const ext = this.deps.ext();
    if (!ext) return;
    const id = ++this.#voiceN;
    const set = (state: PendingVoice['state'], text = '') =>
      (this.voices = [...this.voices.filter((v) => v.id !== id), { id, state, text }]);
    const drop = () => (this.voices = this.voices.filter((v) => v.id !== id));
    set('saving');
    const kind = a.type.includes('mp4') ? 'm4a' : a.type.includes('ogg') ? 'ogg' : 'webm';
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- a timestamp, not state
    const path = `v0/voice/chat/${stampOf(new Date())}.${kind}`;
    try {
      await ext.files().write(path, a.blob, { ifMatch: '*' });
    } catch (e) {
      set('failed', `Your recording wasn’t saved: ${(e as Error).message || e}`);
      return;
    }
    set('transcribing');
    let words: string;
    try {
      words = (await ext.transcribe(path)).text.trim();
    } catch (e) {
      const hint = (e as { hint?: string }).hint;
      set('failed', hint || `Couldn’t transcribe it: ${(e as Error).message || e}`);
      return;
    }
    if (!words) {
      set('failed', 'No words in that recording');
      return;
    }
    drop();
    await this.post(words, { voice: path, ...(agent ? { agent } : {}) });
  };

  /** Dismisses a voice message that failed. */
  dismiss = (id: number): void => {
    this.voices = this.voices.filter((v) => v.id !== id);
  };
}
