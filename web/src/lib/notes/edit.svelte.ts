// Editing a comment's words (spec/notes.md, "Editing a comment"): one comment at a time, in its own card on a
// computer, or in the comment box (compose mode) on a phone. Save posts api/notes/<id>/edit; the field stays until the
// list has the new words, so the card never shows the old ones in between. Cancel (Esc) changes nothing.
import type { EditingTask } from '../automation.svelte';
import { activeTask, type CommentStatus } from '../status';
import type { Row } from './view';

export const editUrl = (id: number): string => `api/notes/${encodeURIComponent(String(id))}/edit`;

/**
 * Whether a card offers Edit now. The server says whether this page may edit it (`editable`: open, its author or an
 * approver, and the agent wasn't on it when the list loaded); what the page has learnt since must agree: still Open, or
 * a quiet Working (sent, the agent not on it yet), and no mark of the agent's on it (spec/automation.md, "Marking
 * progress"). Not a question, not Done.
 */
export function canEdit(
  row: Pick<Row, 'id' | 'kind' | 'mark' | 'editable'>,
  status: Pick<CommentStatus, 'state' | 'now'>,
  task?: Pick<EditingTask, 'state' | 'working' | 'marked' | 'done'> | null,
): boolean {
  if (row.kind !== 'note' || !row.editable || row.mark !== 'open' || status.now) return false;
  if (status.state !== 'open' && status.state !== 'working') return false;
  const t = activeTask(task as EditingTask | null | undefined);
  if (t && (t.state === 'running' || t.state === 'stopping')) {
    if (t.working?.note === row.id || t.marked?.some((m) => m.note === row.id) || t.done?.includes(row.id))
      return false;
  }
  return true;
}

/** What a key does in the field: Enter (or Ctrl/⌘+Enter) saves, Esc cancels, Shift+Enter is a new line. */
export function editKey(e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'isComposing'>): 'save' | 'cancel' | null {
  if (e.isComposing) return null;
  if (e.key === 'Escape') return 'cancel';
  if (e.key === 'Enter' && !e.shiftKey) return 'save';
  return null;
}

export interface NoteEditDeps {
  fetch?: typeof fetch;
  /** After a save the server took: the list refetches, so the card has the new words before the field goes. */
  onSaved?: () => Promise<unknown> | unknown;
  /** A save the server refused, or a network failure: the reason, in a few words. */
  onError?: (message: string) => void;
}

export class NoteEdit {
  /** The comment being edited, or null. */
  id = $state<number | null>(null);
  /** Its words as typed so far. */
  text = $state('');
  /** In the comment box (a phone's compose mode), not in its card. */
  inBox = $state(false);
  /** Its moment, "0:18", for the box's line. */
  time = $state('');
  /** A save is on its way. */
  busy = $state(false);
  /** Its words when editing started: saving them unchanged is Cancel. */
  #original = '';

  constructor(private readonly deps: NoteEditDeps = {}) {}

  /** Editing `row`'s words starts (another comment's edit, unsaved, is dropped). */
  start(row: Pick<Row, 'id' | 'text' | 'time'>, inBox = false): void {
    if (this.busy) return;
    this.id = row.id;
    this.text = row.text;
    this.#original = row.text;
    this.time = row.time;
    this.inBox = inBox;
  }

  /** Whether `id` is the comment being edited (in its card, or anywhere with `anywhere`). */
  editing(id: number, anywhere = false): boolean {
    return this.id === id && (anywhere || !this.inBox);
  }

  /** The words differ from what the comment says. */
  get changed(): boolean {
    return this.id != null && this.text.trim() !== this.#original.trim();
  }

  /** Esc or Cancel: nothing changes. */
  cancel = (): void => {
    if (this.busy) return;
    this.id = null;
    this.text = '';
    this.#original = '';
    this.inBox = false;
  };

  /** Enter or Save: the new words go to the server; unchanged words are Cancel. Resolves to whether the edit ended
   * (saved or unchanged); a refusal keeps the field, with what was typed. */
  save = async (): Promise<boolean> => {
    const id = this.id;
    if (id == null || this.busy) return false;
    if (!this.changed) {
      this.cancel();
      return true;
    }
    this.busy = true;
    try {
      const r = await (this.deps.fetch ?? fetch)(editUrl(id), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: this.text.trim() }),
      });
      if (!r.ok) {
        const body = (await r.json().catch(() => null)) as { error?: unknown } | null;
        const why = typeof body?.error === 'string' && body.error ? body.error : '';
        this.deps.onError?.(
          why ? why.charAt(0).toUpperCase() + why.slice(1) : 'Could not save the comment. Try again.',
        );
        return false;
      }
      await r.body?.cancel().catch(() => {});
      await this.deps.onSaved?.();
    } catch {
      this.deps.onError?.('Could not save the comment. Try again.');
      return false;
    } finally {
      this.busy = false;
    }
    if (this.id === id) this.cancel();
    return true;
  };
}
