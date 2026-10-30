// The local audio buffer. Every recorded chunk is written here (IndexedDB) before it is uploaded, and deleted only
// after the server confirms its byte range. When the page closes or crashes mid-take, the next page load resends
// what the server is missing. If IndexedDB is unavailable (private mode, quota, a broken profile) the buffer falls
// back to memory, so recording never breaks; only the cross-reload recovery is lost then.

export interface ChunkKey {
  take: string;
  version: string;
  file: string;
  /** Byte offset of this chunk in the segment file. */
  offset: number;
}

export interface ChunkRecord extends ChunkKey {
  data: Blob;
  size: number;
  mime: string;
  /** The version file's path, so a chunk is only ever resent to the same video (ports get reused). */
  path: string;
  saved: number;
}

export interface ChunkStore {
  /** 'indexeddb' while IndexedDB works, 'memory' after it failed. */
  readonly kind: 'indexeddb' | 'memory';
  /** Buffer a chunk. Never rejects: if IndexedDB fails, the chunk is kept in memory. */
  put(rec: ChunkRecord): Promise<void>;
  /** The server holds the first `size` bytes of this file: drop every chunk that ends at or before it. */
  confirm(take: string, version: string, file: string, size: number): Promise<void>;
  /** Every buffered chunk, sorted by take, version, file and offset. */
  list(): Promise<ChunkRecord[]>;
}

export const DB_NAME = 'retake-audio';
const STORE = 'chunks';
const keyOf = (k: ChunkKey): string => JSON.stringify([k.take, k.version, k.file, k.offset]);
const order = (a: ChunkKey, b: ChunkKey): number =>
  a.take < b.take
    ? -1
    : a.take > b.take
      ? 1
      : a.version < b.version
        ? -1
        : a.version > b.version
          ? 1
          : a.file < b.file
            ? -1
            : a.file > b.file
              ? 1
              : a.offset - b.offset;

const done = (req: IDBRequest | IDBTransaction): Promise<void> =>
  new Promise((res, rej) => {
    if ('oncomplete' in req) {
      req.oncomplete = () => res();
      req.onerror = req.onabort = () => rej(req.error ?? new Error('transaction failed'));
    } else {
      req.onsuccess = () => res();
      req.onerror = () => rej(req.error ?? new Error('request failed'));
    }
  });

function openDb(idb: IDBFactory): Promise<IDBDatabase> {
  return new Promise((res, rej) => {
    const req = idb.open(DB_NAME, 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore(STORE, { keyPath: ['take', 'version', 'file', 'offset'] });
    };
    req.onsuccess = () => res(req.result);
    req.onerror = () => rej(req.error ?? new Error('indexedDB.open failed'));
    req.onblocked = () => rej(new Error('indexedDB.open blocked'));
  });
}

class BufferedChunks implements ChunkStore {
  #db: IDBDatabase | null;
  readonly #mem = new Map<string, ChunkRecord>();

  constructor(db: IDBDatabase | null) {
    this.#db = db;
  }

  get kind(): 'indexeddb' | 'memory' {
    return this.#db ? 'indexeddb' : 'memory';
  }

  #fail(err: unknown): void {
    if (!this.#db) return;
    console.warn('retake: IndexedDB failed, buffering audio in memory', err);
    try {
      this.#db.close();
    } catch {
      /* already closed */
    }
    this.#db = null;
  }

  async put(rec: ChunkRecord): Promise<void> {
    if (this.#db) {
      try {
        const tx = this.#db.transaction(STORE, 'readwrite');
        tx.objectStore(STORE).put(rec);
        tx.commit?.(); // commit now, not when the event loop gets round to it: the page may be closing
        await done(tx);
        return;
      } catch (err) {
        this.#fail(err);
      }
    }
    this.#mem.set(keyOf(rec), rec);
  }

  async confirm(take: string, version: string, file: string, size: number): Promise<void> {
    for (const [k, r] of this.#mem)
      if (r.take === take && r.version === version && r.file === file && r.offset + r.size <= size) this.#mem.delete(k);
    if (!this.#db) return;
    try {
      const tx = this.#db.transaction(STORE, 'readwrite');
      const range = IDBKeyRange.bound([take, version, file, 0], [take, version, file, Number.MAX_SAFE_INTEGER]);
      const req = tx.objectStore(STORE).openCursor(range);
      req.onsuccess = () => {
        const c = req.result;
        if (!c) return;
        const r = c.value as ChunkRecord;
        if (r.offset + r.size <= size) c.delete();
        c.continue();
      };
      await done(tx);
    } catch (err) {
      this.#fail(err);
    }
  }

  async list(): Promise<ChunkRecord[]> {
    const out = [...this.#mem.values()];
    if (this.#db) {
      try {
        const tx = this.#db.transaction(STORE, 'readonly');
        const req = tx.objectStore(STORE).getAll();
        await done(tx);
        out.push(...(req.result as ChunkRecord[]));
      } catch (err) {
        this.#fail(err);
      }
    }
    return out.sort(order);
  }
}

/** Open the buffer on IndexedDB, or in memory if that fails. Never rejects. */
export async function openChunkStore(idb: IDBFactory | undefined = globalThis.indexedDB): Promise<ChunkStore> {
  try {
    if (!idb) throw new Error('no IndexedDB');
    return new BufferedChunks(await openDb(idb));
  } catch (err) {
    console.warn('retake: IndexedDB unavailable, buffering audio in memory', err);
    return new BufferedChunks(null);
  }
}

/** A memory-only buffer (what openChunkStore falls back to). */
export const memoryChunkStore = (): ChunkStore => new BufferedChunks(null);

/** The pending chunks of one segment file. */
export interface PendingFile {
  take: string;
  version: string;
  file: string;
  mime: string;
  path: string;
  chunks: ChunkRecord[];
}

/** Group buffered chunks by segment file, each sorted by offset. */
export function groupPending(recs: ChunkRecord[]): PendingFile[] {
  const out = new Map<string, PendingFile>();
  for (const r of [...recs].sort(order)) {
    const k = JSON.stringify([r.take, r.version, r.file]);
    let f = out.get(k);
    if (!f)
      out.set(k, (f = { take: r.take, version: r.version, file: r.file, mime: r.mime, path: r.path, chunks: [] }));
    f.chunks.push(r);
  }
  return [...out.values()];
}

export interface Run {
  start: number;
  end: number;
  blob: Blob;
}

/** The contiguous byte runs the buffer holds for a file, in order: [start, end) and their data. */
export function runs(f: PendingFile): Run[] {
  const out: { start: number; end: number; parts: Blob[] }[] = [];
  for (const c of f.chunks) {
    let r = out[out.length - 1];
    if (!r || c.offset > r.end) out.push((r = { start: c.offset, end: c.offset, parts: [] })); // a hole
    if (c.offset + c.size <= r.end) continue; // already covered
    r.parts.push(c.offset < r.end ? c.data.slice(r.end - c.offset) : c.data);
    r.end = c.offset + c.size;
  }
  return out.map((r) => ({ start: r.start, end: r.end, blob: new Blob(r.parts, { type: f.mime }) }));
}

/** An offset no file reaches: POSTing it is refused with 409 and the file's current size, and writes nothing. */
export const PROBE_OFFSET = Number.MAX_SAFE_INTEGER;

export interface Post {
  (url: string, body: Blob): Promise<{ status: number; json: { size?: number; error?: string } }>;
}

export const audioUrl = (take: string, version: string | null, file: string, offset: number): string =>
  `/api/session/${encodeURIComponent(take)}/audio?v=${encodeURIComponent(String(version))}&name=${encodeURIComponent(file)}&offset=${offset}`;

export interface RecoverResult {
  /** Files whose buffered bytes are now all on the server. */
  recovered: number;
  /** Bytes sent. */
  sent: number;
  /** Files left in the buffer: another video, a hole the server can't fill, or a failure worth retrying. */
  kept: number;
  /** Of the kept files, those that failed in a way worth retrying (server unreachable or erroring). */
  retry: number;
}

/**
 * Resend what earlier takes left in the buffer. Each file first asks the server for its size (a probe at an offset
 * past any end gets 409 with the size), then sends only the missing tail. Chunks are only resent for a version
 * whose file path is the one they were recorded on. `skip` leaves out takes another writer owns (this page's own
 * takes, which its saver uploads), so two writers never race on one file.
 */
export async function recoverPending(
  store: ChunkStore,
  family: readonly { label: string; path: string }[],
  post: Post,
  skip: (take: string) => boolean = () => false,
): Promise<RecoverResult> {
  const res: RecoverResult = { recovered: 0, sent: 0, kept: 0, retry: 0 };
  for (const f of groupPending(await store.list())) {
    if (skip(f.take)) continue;
    const v = family.find((x) => x.label === f.version);
    if (!v || (f.path && v.path !== f.path)) {
      res.kept++; // another video on this port, or a version that is gone: keep it for when it is back
      continue;
    }
    let size: number | undefined;
    const all = runs(f),
      end = all[all.length - 1].end;
    try {
      const probe = await post(audioUrl(f.take, f.version, f.file, PROBE_OFFSET), new Blob([]));
      size = probe.status === 409 ? probe.json.size : undefined;
      for (const run of all) {
        // the server must already reach this run (else there is a hole it can't fill), and not hold all of it yet
        for (let tries = 0; typeof size === 'number' && size >= run.start && size < run.end && tries < 3; tries++) {
          const r = await post(audioUrl(f.take, f.version, f.file, size), run.blob.slice(size - run.start));
          if (r.status === 200) res.sent += run.end - size;
          size = r.status === 200 || r.status === 409 ? r.json.size : undefined;
        }
      }
    } catch {
      size = undefined; // server unreachable
    }
    if (typeof size === 'number' && size > 0) await store.confirm(f.take, f.version, f.file, size);
    if (typeof size === 'number' && size >= end) {
      res.recovered++;
      continue;
    }
    res.kept++;
    // The server stops short of a run, in a gap the buffer can't fill: stuck. Anything else (errors, refusals) may
    // work later.
    const at = size;
    const hole = typeof at === 'number' && !all.some((r) => at >= r.start && at < r.end);
    if (!hole) res.retry++;
  }
  return res;
}
