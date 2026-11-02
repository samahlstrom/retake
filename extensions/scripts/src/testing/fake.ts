// A stand-in for the page's ctx.files() in unit tests: files in memory, If-Match like the server's, and a watch that
// fires when a test edits a file "outside".
import type { Edit, FileInfo, Files, ReadFile } from '../types';

/** A sha256 stand-in: the same text always gets the same value. */
export function fakeSha(text: string): string {
  let h = 5381;
  for (let i = 0; i < text.length; i++) h = ((h << 5) + h + text.charCodeAt(i)) | 0;
  return `s${(h >>> 0).toString(16)}-${text.length}`;
}

export class HttpError extends Error {
  status: number;
  changed?: boolean;
  sha256?: string;
  hint?: string;
  constructor(status: number, message: string, extra: Partial<HttpError> = {}) {
    super(message);
    this.status = status;
    Object.assign(this, extra);
  }
}

interface Stored {
  text: string;
  blob: Blob | null;
  sha: string;
  modified: number;
}

const globRe = (glob: string): RegExp =>
  new RegExp(
    `^${glob
      .split('**')
      .map((p) =>
        p
          .split('*')
          .map((q) => q.replace(/[.+?^${}()|[\]\\]/g, '\\$&'))
          .join('[^/]*'),
      )
      .join('.*')}$`,
  );

export class FakeFiles implements Files {
  store = new Map<string, Stored>();
  writes: { path: string; ifMatch?: string; text: string }[] = [];
  watchers = new Set<(c: FileInfo[]) => void>();
  edits: Edit[] = [];
  /** When set, every call fails with it (503 offline, …). */
  failWith: Error | null = null;
  #gate: Promise<void> | null = null;

  constructor(files: Record<string, string> = {}) {
    for (const [p, t] of Object.entries(files)) this.put(p, t);
  }

  put(path: string, text: string, blob: Blob | null = null): FileInfo {
    const s = { text, blob, sha: fakeSha(blob ? `blob:${blob.size}:${text}` : text), modified: Date.now() };
    this.store.set(path, s);
    return this.info(path, s);
  }

  info(path: string, s: Stored): FileInfo {
    return { path, size: s.blob ? s.blob.size : s.text.length, modified: s.modified, sha256: s.sha };
  }

  text(path: string): string | undefined {
    return this.store.get(path)?.text;
  }

  /** Holds writes until the returned function is called. */
  hold(): () => void {
    let release!: () => void;
    this.#gate = new Promise((r) => (release = r));
    return () => {
      this.#gate = null;
      release();
    };
  }

  /** Someone edits a file outside Retake: it changes, and the watch says so. */
  outside(path: string, text: string | null): FileInfo {
    let info: FileInfo;
    if (text === null) {
      const s = this.store.get(path);
      this.store.delete(path);
      info = { path, size: 0, modified: Date.now(), sha256: s?.sha ?? '', deleted: true };
    } else info = this.put(path, text);
    for (const w of this.watchers) w([info]);
    return info;
  }

  async list(glob?: string): Promise<FileInfo[]> {
    if (this.failWith) throw this.failWith;
    const re = glob ? globRe(glob) : null;
    return [...this.store]
      .filter(([p]) => !re || re.test(p))
      .map(([p, s]) => this.info(p, s))
      .sort((a, b) => a.path.localeCompare(b.path));
  }

  async read(path: string): Promise<ReadFile> {
    if (this.failWith) throw this.failWith;
    const s = this.store.get(path);
    if (!s) throw new HttpError(404, 'not found');
    return { sha256: s.sha, text: async () => s.text, blob: async () => s.blob ?? new Blob([s.text]) };
  }

  async write(path: string, data: string | Blob, o: { ifMatch?: string } = {}): Promise<FileInfo> {
    if (this.#gate) await this.#gate;
    if (this.failWith) throw this.failWith;
    const cur = this.store.get(path);
    const m = o.ifMatch;
    if (m === '*' ? !!cur : m ? cur?.sha !== m : false) {
      throw new HttpError(409, 'changed', { changed: true, sha256: cur?.sha ?? '' });
    }
    const text = typeof data === 'string' ? data : '';
    this.writes.push({ path, ifMatch: m, text });
    return this.put(path, text, typeof data === 'string' ? null : data);
  }

  async remove(path: string): Promise<void> {
    this.store.delete(path);
  }

  url(path: string): string {
    return `api/ext/scripts/files/${path}`;
  }

  watch(cb: (c: FileInfo[]) => void): () => void {
    this.watchers.add(cb);
    return () => this.watchers.delete(cb);
  }

  async history(path?: string): Promise<Edit[]> {
    return this.edits.filter((e) => !path || e.path === path);
  }

  async undo(id: string, path: string): Promise<FileInfo> {
    const e = this.edits.find((x) => x.id === id && x.path === path);
    if (!e) throw new HttpError(404, 'not found');
    const cur = this.store.get(path);
    if ((cur?.sha ?? '') !== e.after) throw new HttpError(409, 'changed', { changed: true, sha256: cur?.sha ?? '' });
    this.edits = this.edits.filter((x) => x !== e);
    return this.put(path, `restored ${e.before}`);
  }
}

/** Lets pending promises and timers run. */
export const settle = async (): Promise<void> => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};
