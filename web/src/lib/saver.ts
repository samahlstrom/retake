// Saving is automatic: audio is appended by byte offset, new images are stored, then the take's JSON files are
// rewritten. A failed save toasts once and retries on the next autosave; audio and images stay in the local buffer
// until the server confirms them.
import { audioUrl, type ChunkStore } from './audio-buffer';
import { KEEPALIVE_MAX, MSG_SAVE_FAILED } from './constants';
import { imageRefused, imageRouteMissing, imageUrl, memoryImageStore, type ImageStore } from './image-buffer';
import { MSG_IMAGE_REFUSED } from './images';
import type { Segment, Take, TakeData, TakeImage } from './types';

export const dataUrl = (take: string, version: string | null): string =>
  `/api/session/${encodeURIComponent(take)}/data?v=${encodeURIComponent(String(version))}`;

export interface SaverDeps {
  serialize: (t: Take) => TakeData;
  toast: (msg: string, ms?: number) => void;
  store: () => ChunkStore;
  /** The image buffer (memory when left out). */
  images?: () => ImageStore;
  fetch?: typeof fetch;
}

const fallbackImages = memoryImageStore();

const segUrl = (t: Take, s: Segment, offset: number) => audioUrl(t.id ?? '', t.version, s.file, offset);

export class Saver {
  constructor(private readonly deps: SaverDeps) {}

  #fetch(url: string, init: RequestInit): Promise<Response> {
    return (this.deps.fetch ?? fetch)(url, init);
  }

  /** Upload what is new, then the JSON. Concurrent calls coalesce into one more save after the running one. */
  save(t: Take | null): Promise<void> | undefined {
    if (!t || !t.id) return;
    if (t.saving) {
      t.saveAgain = true;
      return t.saving;
    }
    t.saving = (async () => {
      try {
        for (const s of t.segments) {
          if (!s.chunks.length) continue;
          await s.persisted; // every chunk so far is in the local buffer before any of it is sent
          const end = s.stored,
            blob = new Blob(s.chunks, { type: s.mime });
          for (let tries = 0; end > s.uploaded && tries < 3; tries++) {
            const r = await this.#fetch(segUrl(t, s, s.uploaded), {
              method: 'POST',
              body: blob.slice(s.uploaded, end),
            });
            const j = (await r.json()) as { size: number; error?: string };
            if (r.status === 409) {
              s.uploaded = Math.min(j.size, s.uploaded);
              continue;
            }
            if (!r.ok) throw new Error(j.error || `audio ${r.status}`);
            s.uploaded = j.size;
            await this.deps.store().confirm(t.id ?? '', String(t.version), s.file, j.size);
          }
        }
        for (const img of t.images) if (img.upload === 'pending') await this.#sendImage(t, img);
        const r = await this.#fetch(dataUrl(t.id ?? '', t.version), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.deps.serialize(t)),
        });
        if (!r.ok) throw new Error(`data ${r.status}`);
        t.dirty = false;
        t.lastSave = Date.now();
        if (t.saveErr) this.deps.toast('Saving again');
        t.saveErr = null;
      } catch (err) {
        if (!t.saveErr) this.deps.toast(MSG_SAVE_FAILED, 6000);
        t.saveErr = String((err as Error)?.message || err);
      } finally {
        t.saving = null;
        if (t.saveAgain) {
          t.saveAgain = false;
          void this.save(t);
        }
      }
    })();
    return t.saving;
  }

  /**
   * Store one image of take `t` (after it is in the local buffer). One undone before it was sent is never sent. An
   * older server without the image route keeps the take saving: the image stays buffered for a server that has it.
   * Any other failure throws, so the JSON (which lists the image) waits for the retry.
   */
  async #sendImage(t: Take, img: TakeImage): Promise<void> {
    const take = t.id ?? '',
      version = String(t.version),
      buffer = (this.deps.images ?? (() => fallbackImages))();
    if (img.undone) {
      img.upload = 'dropped';
      await img.stored;
      await buffer.confirm(take, version, img.file);
      return;
    }
    await img.stored;
    const r = await this.#fetch(imageUrl(take, t.version, img.file), {
      method: 'POST',
      headers: { 'Content-Type': img.mime },
      body: img.blob,
    });
    if (imageRouteMissing(r.status)) {
      img.upload = 'refused';
      if (!t.images.some((x) => x !== img && x.upload === 'refused')) this.deps.toast(MSG_IMAGE_REFUSED, 4000);
      return;
    }
    if (imageRefused(r.status)) {
      img.upload = 'refused';
      await buffer.confirm(take, version, img.file);
      const j = (await r.json().catch(() => ({}))) as { error?: string };
      this.deps.toast(`Image ${img.file} refused: ${j.error ?? r.status}`, 4000);
      return;
    }
    if (!r.ok) throw new Error(`image ${r.status}`);
    img.upload = 'done';
    await buffer.confirm(take, version, img.file);
  }

  /** Page closing: best-effort keepalive upload of the last few seconds (small requests only). */
  flush(t: Take | null): void {
    if (!t || !t.id || t.closed) return;
    for (const s of t.segments) {
      const blob = new Blob(s.chunks, { type: s.mime });
      if (blob.size > s.uploaded && blob.size - s.uploaded < KEEPALIVE_MAX) {
        void this.#fetch(segUrl(t, s, s.uploaded), {
          method: 'POST',
          body: blob.slice(s.uploaded),
          keepalive: true,
        }).catch(() => {});
      }
    }
    const body = JSON.stringify(this.deps.serialize(t));
    if (body.length < KEEPALIVE_MAX)
      void this.#fetch(dataUrl(t.id, t.version), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body,
        keepalive: true,
      }).catch(() => {});
  }
}
