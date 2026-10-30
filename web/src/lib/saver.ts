// Saving is automatic: audio is appended by byte offset, then the take's JSON files are rewritten. A failed save
// toasts once and retries on the next autosave; the audio stays in the local buffer until the server confirms it.
import { audioUrl, type ChunkStore } from './audio-buffer';
import { KEEPALIVE_MAX, MSG_SAVE_FAILED } from './constants';
import type { Segment, Take, TakeData } from './types';

export const dataUrl = (take: string, version: string | null): string =>
  `/api/session/${encodeURIComponent(take)}/data?v=${encodeURIComponent(String(version))}`;

export interface SaverDeps {
  serialize: (t: Take) => TakeData;
  toast: (msg: string, ms?: number) => void;
  store: () => ChunkStore;
  fetch?: typeof fetch;
}

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
