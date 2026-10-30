// The local image buffer, the audio buffer's twin (audio-buffer.ts): an image added to a take is written here
// (IndexedDB) before it is uploaded, and deleted once the server has stored it. If the page closes or crashes first,
// the next page load resends it. Without IndexedDB it falls back to memory, so adding images never breaks.
import { done } from './audio-buffer';

export interface ImageRecord {
  take: string;
  version: string;
  /** img_NN.<ext> */
  file: string;
  mime: string;
  data: Blob;
  size: number;
  /** The version file's path, so an image is only ever resent to the same video (ports get reused). */
  path: string;
  saved: number;
}

export interface ImageStore {
  readonly kind: 'indexeddb' | 'memory';
  /** Buffer an image. Never rejects: if IndexedDB fails, it is kept in memory. */
  put(rec: ImageRecord): Promise<void>;
  /** The server has this image (or it was undone before it was sent): drop it. */
  confirm(take: string, version: string, file: string): Promise<void>;
  /** Every buffered image, sorted by take, version and file. */
  list(): Promise<ImageRecord[]>;
}

export const IMAGE_DB = 'retake-images';
const STORE = 'images';
const keyOf = (r: Pick<ImageRecord, 'take' | 'version' | 'file'>): string =>
  JSON.stringify([r.take, r.version, r.file]);

function openDb(idb: IDBFactory): Promise<IDBDatabase> {
  return new Promise((res, rej) => {
    const req = idb.open(IMAGE_DB, 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore(STORE, { keyPath: ['take', 'version', 'file'] });
    };
    req.onsuccess = () => res(req.result);
    req.onerror = () => rej(req.error ?? new Error('indexedDB.open failed'));
    req.onblocked = () => rej(new Error('indexedDB.open blocked'));
  });
}

class BufferedImages implements ImageStore {
  #db: IDBDatabase | null;
  readonly #mem = new Map<string, ImageRecord>();

  constructor(db: IDBDatabase | null) {
    this.#db = db;
  }

  get kind(): 'indexeddb' | 'memory' {
    return this.#db ? 'indexeddb' : 'memory';
  }

  #fail(err: unknown): void {
    if (!this.#db) return;
    console.warn('retake: IndexedDB failed, buffering images in memory', err);
    try {
      this.#db.close();
    } catch {
      /* already closed */
    }
    this.#db = null;
  }

  async put(rec: ImageRecord): Promise<void> {
    if (this.#db) {
      try {
        const tx = this.#db.transaction(STORE, 'readwrite');
        tx.objectStore(STORE).put(rec);
        tx.commit?.();
        await done(tx);
        return;
      } catch (err) {
        this.#fail(err);
      }
    }
    this.#mem.set(keyOf(rec), rec);
  }

  async confirm(take: string, version: string, file: string): Promise<void> {
    this.#mem.delete(keyOf({ take, version, file }));
    if (!this.#db) return;
    try {
      const tx = this.#db.transaction(STORE, 'readwrite');
      tx.objectStore(STORE).delete([take, version, file]);
      await done(tx);
    } catch (err) {
      this.#fail(err);
    }
  }

  async list(): Promise<ImageRecord[]> {
    const out = [...this.#mem.values()];
    if (this.#db) {
      try {
        const tx = this.#db.transaction(STORE, 'readonly');
        const req = tx.objectStore(STORE).getAll();
        await done(tx);
        out.push(...(req.result as ImageRecord[]));
      } catch (err) {
        this.#fail(err);
      }
    }
    return out.sort((a, b) => (keyOf(a) < keyOf(b) ? -1 : keyOf(a) > keyOf(b) ? 1 : 0));
  }
}

/** Open the buffer on IndexedDB, or in memory if that fails. Never rejects. */
export async function openImageStore(idb: IDBFactory | undefined = globalThis.indexedDB): Promise<ImageStore> {
  try {
    if (!idb) throw new Error('no IndexedDB');
    return new BufferedImages(await openDb(idb));
  } catch (err) {
    console.warn('retake: IndexedDB unavailable, buffering images in memory', err);
    return new BufferedImages(null);
  }
}

/** A memory-only buffer (what openImageStore falls back to). */
export const memoryImageStore = (): ImageStore => new BufferedImages(null);

export const imageUrl = (take: string, version: string | null, file: string): string =>
  `/api/session/${encodeURIComponent(take)}/image?v=${encodeURIComponent(String(version))}&name=${encodeURIComponent(file)}`;

/** POSTs one image; resolves with the HTTP status, rejects when the server is unreachable. */
export type PostImage = (url: string, body: Blob, mime: string) => Promise<number>;

/** An older server without the image route answers 404 (or 405): keep the image for a server that has it. */
export const imageRouteMissing = (status: number): boolean => status === 404 || status === 405;

export interface ImageRecoverResult {
  /** Images the server now has. */
  recovered: number;
  /** Images the server refused for good (bad name, too big, wrong type): dropped from the buffer. */
  dropped: number;
  /** Images left in the buffer: another video, an older server, or a failure worth retrying. */
  kept: number;
  /** Of the kept ones, those that failed in a way worth retrying soon (server unreachable or erroring). */
  retry: number;
}

/**
 * Resend images that earlier takes left in the buffer, to the same video only. `skip` leaves out this page's own
 * takes (its saver uploads those).
 */
export async function recoverImages(
  store: ImageStore,
  family: readonly { label: string; path: string }[],
  post: PostImage,
  skip: (take: string) => boolean = () => false,
): Promise<ImageRecoverResult> {
  const res: ImageRecoverResult = { recovered: 0, dropped: 0, kept: 0, retry: 0 };
  for (const r of await store.list()) {
    if (skip(r.take)) continue;
    const v = family.find((x) => x.label === r.version);
    if (!v || (r.path && v.path !== r.path)) {
      res.kept++;
      continue;
    }
    const status = await post(imageUrl(r.take, r.version, r.file), r.data, r.mime).catch(() => 0); // 0: unreachable
    if (status >= 200 && status < 300) {
      await store.confirm(r.take, r.version, r.file);
      res.recovered++;
    } else if (imageRefused(status)) {
      await store.confirm(r.take, r.version, r.file); // it will never be accepted
      res.dropped++;
    } else {
      res.kept++;
      if (status === 0 || status >= 500) res.retry++;
    }
  }
  return res;
}

/** The server will never take this image (bad name, too big, type and extension disagree). */
export const imageRefused = (status: number): boolean => status === 400 || status === 413 || status === 415;
