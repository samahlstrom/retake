// One document of v0 (idea.md, outline.md, script.md, refs/*.md) as the editor holds it: what you typed, what's on
// disk, and saving with If-Match (spec/ignition.md, "Saving" and "Outside edits").
import type { Files } from '../types';
import { fillTalkOut, insertBlock, talkOutBlock, TRANSCRIBING, lineOf, type Splice } from '../lib/doc';
import { isChanged, isMissing, isOffline, sayError, shaOf } from '../lib/errors';
import { decide, type Change } from '../lib/merge';

/** Saving waits this long after the last keystroke. */
export const SAVE_DELAY = 600;

/** A change that didn't come from the textarea, for it to apply with the caret in the right place. */
export type External = { kind: 'reload'; old: string } | { kind: 'splice'; splice: Splice; caret?: number };

export class Doc {
  readonly path: string;
  readonly #files: Files;
  /** The text as typed. */
  text = $state('');
  /** The text on disk as far as we know: what we last read or wrote; null when only its sha is known (Keep mine). */
  saved = $state<string | null>('');
  /** The sha256 on disk: what If-Match sends. "" while the file doesn't exist (its first save makes it). */
  sha = $state('');
  exists = $state(false);
  loaded = $state(false);
  /** Reading it failed: the text can't be edited (it would overwrite what we never saw). */
  loadError = $state('');
  saving = $state(false);
  /** The last save failed (not a conflict): one line. */
  error = $state('');
  offline = $state(false);
  /** It changed on disk while you had unsaved typing: its sha now ("" when it was deleted). */
  conflict = $state<{ sha: string } | null>(null);
  /** The newest change made from outside the textarea. */
  external = $state.raw<External | null>(null);
  readonly ready: Promise<void>;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #later: Change | null = null;
  #again = false;

  constructor(files: Files, path: string) {
    this.#files = files;
    this.path = path;
    this.ready = this.#load();
  }

  get dirty(): boolean {
    return this.text !== this.saved;
  }

  /** The quiet word in the header: Saved, Saving…, Edited, Not saved, Offline (or nothing). */
  get word(): string {
    if (!this.loaded) return '';
    if (this.loadError) return this.offline ? 'Offline' : '';
    if (this.conflict || this.error) return this.offline ? 'Offline' : 'Not saved';
    if (this.saving) return 'Saving…';
    if (this.dirty) return 'Edited';
    return this.exists ? 'Saved' : '';
  }

  async #load(): Promise<void> {
    try {
      const f = await this.#files.read(this.path);
      const t = await f.text();
      this.sha = f.sha256;
      this.saved = t;
      this.text = t;
      this.exists = true;
    } catch (e) {
      if (!isMissing(e)) {
        this.loadError = sayError(e);
        this.offline = isOffline(e);
      }
    }
    this.loaded = true;
  }

  /** Reads it again after a failed first read. */
  async retry(): Promise<void> {
    if (!this.loadError) return;
    this.loadError = '';
    this.offline = false;
    this.loaded = false;
    await this.#load();
    if (!this.loadError) this.external = { kind: 'reload', old: '' };
  }

  /** Typing: saves 600 ms after the last keystroke. */
  input(text: string): void {
    if (text === this.text) return;
    this.text = text;
    this.#schedule();
  }

  #schedule(ms = SAVE_DELAY): void {
    if (this.#timer) clearTimeout(this.#timer);
    this.#timer = setTimeout(() => {
      this.#timer = null;
      void this.save();
    }, ms);
  }

  /** Saves now (on blur, before switching documents, before leaving). */
  flush(): Promise<void> {
    return this.save();
  }

  get pending(): boolean {
    return this.#timer !== null || this.saving;
  }

  async save(): Promise<void> {
    if (this.#timer) {
      clearTimeout(this.#timer);
      this.#timer = null;
    }
    if (this.saving) {
      this.#again = true;
      return;
    }
    if (!this.loaded || this.loadError || this.conflict || !this.dirty) return;
    const body = this.text;
    this.saving = true;
    try {
      const info = await this.#files.write(this.path, body, { ifMatch: this.sha || '*' });
      this.sha = info.sha256;
      this.saved = body;
      this.exists = true;
      this.error = '';
      this.offline = false;
    } catch (e) {
      if (isChanged(e)) this.conflict = { sha: shaOf(e) };
      else {
        this.error = sayError(e);
        this.offline = isOffline(e);
      }
    } finally {
      this.saving = false;
    }
    const later = this.#later;
    this.#later = null;
    if (later) this.outside(later);
    if (this.#again) {
      this.#again = false;
      await this.save();
    }
  }

  /** The watch says the file changed on disk. */
  outside(c: Change): void {
    switch (decide(c, { sha: this.sha, dirty: this.dirty, saving: this.saving })) {
      case 'later':
        this.#later = c;
        return;
      case 'ignore':
        return;
      case 'conflict':
        this.conflict = { sha: c.deleted ? '' : c.sha256 };
        return;
      case 'reload':
        void this.reload(false);
    }
  }

  /** Shows what's on disk. `force` (the Reload button) drops unsaved typing; otherwise typing that arrives while it
   * reads turns it into a conflict instead. */
  async reload(force = true): Promise<void> {
    const before = this.text;
    let text = '',
      sha = '',
      exists = false;
    try {
      const f = await this.#files.read(this.path);
      text = await f.text();
      sha = f.sha256;
      exists = true;
    } catch (e) {
      if (!isMissing(e)) {
        this.error = sayError(e);
        this.offline = isOffline(e);
        return;
      }
    }
    if (!force && this.text !== before) {
      this.conflict = { sha };
      return;
    }
    if (this.#timer) {
      clearTimeout(this.#timer);
      this.#timer = null;
    }
    const old = this.text;
    this.sha = sha;
    this.saved = text;
    this.exists = exists;
    this.conflict = null;
    this.error = '';
    this.offline = false;
    if (text !== old) {
      this.text = text;
      this.external = { kind: 'reload', old };
    }
  }

  /** "Keep mine": writes your text over the outside edit. */
  async keepMine(): Promise<void> {
    if (!this.conflict) return;
    this.sha = this.conflict.sha;
    this.saved = null;
    this.conflict = null;
    await this.save();
  }

  #splice(next: string, splice: Splice, caret?: number): void {
    this.text = next;
    this.external = { kind: 'splice', splice, caret };
  }

  /** Puts a talk-out in at `pos` with its placeholder and saves. Returns its marker's line (from 0). */
  async insertTalkOut(pos: number, rel: string, body = TRANSCRIBING): Promise<number> {
    await this.ready;
    const r = insertBlock(this.text, Math.min(pos, this.text.length), talkOutBlock(rel, body));
    this.#splice(r.text, r.splice, r.caret);
    await this.save();
    return lineOf(r.text, r.caret) - body.trim().split('\n').length;
  }

  /** Puts a talk-out's words in place of its placeholder (when both are still there) and saves. */
  async fillTalkOut(rel: string, body: string): Promise<boolean> {
    await this.ready;
    const r = fillTalkOut(this.text, rel, body);
    if (!r) return false;
    this.#splice(r.text, r.splice);
    await this.save();
    return true;
  }

  dispose(): void {
    if (this.#timer) {
      clearTimeout(this.#timer);
      this.#timer = null;
      void this.save();
    }
  }
}
