<script lang="ts">
  // The comment box at the bottom of the comments (spec/notes.md, "Typed comments on a video"). On a video: "Comment
  // at 0:18 · goes into v8…". Focusing it, typing, or drawing on the video while not recording pauses the video and
  // starts a pending comment; the lines drawn meanwhile go with it. Enter saves it (Ctrl/⌘+Enter also sends it), Esc
  // cancels it with its lines. On an extension's version: "Comment for the agent…", anchored where the version's
  // slot says (spec/extensions.md, "Typing one"). One quiet line under it says what Enter does, only while it's in use.
  // On a phone, while it has the focus on a video, it is compose mode (spec/notes.md, "Typing on a phone"): the box sits
  // on the keyboard (moved by a transform, --compose-dy) and the video shrinks from its top only when it wouldn't fit
  // above it (--compose-scale); phone.css hides the rest. On a phone it is also where a comment's words are edited
  // (spec/notes.md, "Editing a comment"): the comment's words in the box, "Editing 0:18" with Cancel and Save under it;
  // a new comment being written waits meanwhile, its words back after.
  import type { Review } from '../../lib/review.svelte';
  import { composeKey } from '../../lib/notes/typed';
  import { editKey } from '../../lib/notes/edit.svelte';
  import { intoVersion } from '../../lib/notes/view';
  import { composeLayout } from '../../lib/viewport';

  let { review }: { review: Review } = $props();

  const mac = typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.platform);
  const kind = $derived(!!review.kindShown);
  const draft = $derived(review.draft);
  const lines = $derived(draft?.lines ?? 0);
  const into = $derived(kind ? null : intoVersion(draft?.version || review.shown));
  const placeholder = $derived(
    kind ? 'Comment for the agent…' : `Comment at ${review.draftTime}${into ? ` · goes into ${into}` : ''}…`,
  );
  const typing = $derived(!!review.commentDraft.trim());
  /** A comment's words being edited here (a phone), not a new comment. */
  const edit = $derived(review.edit);
  const editing = $derived(edit.id != null && edit.inBox);
  /** The line under the box: while a comment is pending (or typed, on a kind's version), or edited here. */
  const shown = $derived(editing || (kind ? typing : !!draft));
  const drawings = $derived(lines === 1 ? '1 drawing' : lines ? `${lines} drawings` : '');

  let box = $state<HTMLTextAreaElement | null>(null);
  let form = $state<HTMLFormElement | null>(null);
  $effect(() => {
    review.el.comment = box;
  });

  // Compose mode: where the box goes and how small the video gets, again whenever the keyboard, the box's height or the
  // video's frame changes.
  let boxSize = $state(0);
  $effect(() => {
    const el = form;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(() => (boxSize = el.offsetHeight));
    ro.observe(el);
    return () => ro.disconnect();
  });
  $effect(() => {
    const root = document.documentElement;
    const vp = review.viewport;
    const side = form?.parentElement,
      stage = review.el.stage;
    if (!review.composing || !form || !side || !stage) {
      root.style.removeProperty('--compose-dy');
      root.style.removeProperty('--compose-scale');
      return;
    }
    void boxSize;
    const f = review.frame;
    const stageTop = stage.getBoundingClientRect().top; // scaled from its top: the top never moves
    const { dy, scale } = composeLayout({
      kbTop: vp.up ? vp.top + vp.height : null,
      boxBottom: side.getBoundingClientRect().bottom,
      boxHeight: form.offsetHeight,
      stageTop,
      pictureBottom: stageTop + (f.height ? f.top + f.height : stage.offsetHeight),
    });
    root.style.setProperty('--compose-dy', `${dy}px`);
    root.style.setProperty('--compose-scale', String(scale));
  });
  /** The box grows with what's typed, up to a few lines. */
  $effect(() => {
    void review.commentDraft;
    void edit.text;
    void editing;
    if (!box) return;
    box.style.height = 'auto';
    box.style.height = `${Math.min(box.scrollHeight, 160)}px`;
  });

  /** Save or Cancel of an edit here: the keyboard goes down with it (compose mode ends). */
  async function endEdit(save: boolean) {
    if (save && !(await edit.save())) return;
    if (!save) edit.cancel();
    box?.blur();
  }
  function key(e: KeyboardEvent) {
    if (editing) {
      const k = editKey(e);
      if (!k) return;
      e.preventDefault();
      e.stopPropagation();
      void endEdit(k === 'save');
      return;
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      if (kind) {
        review.commentDraft = '';
        box?.blur();
      } else review.cancelDraft();
      return;
    }
    const k = composeKey(e);
    if (!k) return;
    e.preventDefault();
    review.addComment(k === 'send');
  }
  function blur() {
    // Left empty, with no lines: nothing is pending. An edit stays, with its Cancel and Save.
    if (!editing && !kind && draft && !review.draftHasContent) review.draft = null;
  }
  function started() {
    if (!editing) review.startDraft();
  }
</script>

<form
  bind:this={form}
  class="compose"
  data-part="comment-line"
  data-typing-as="comment"
  class:pending={shown}
  class:editing
  onsubmit={(e) => {
    e.preventDefault();
    if (editing) void endEdit(true);
    else review.addComment(false);
  }}
>
  <textarea
    bind:this={box}
    bind:value={
      () => (editing ? edit.text : review.commentDraft),
      (v) => {
        if (editing) edit.text = v;
        else review.commentDraft = v;
      }
    }
    rows="1"
    placeholder={editing ? 'Add words…' : placeholder}
    aria-label={editing
      ? `Edit the comment at ${edit.time}`
      : kind
        ? 'Comment for the agent'
        : placeholder.replace('…', '')}
    readonly={review.commentBusy || edit.busy}
    onkeydown={key}
    onfocus={started}
    oninput={started}
    onblur={blur}></textarea>
  <div class="under" class:shown>
    {#if editing}
      <span class="what">Editing {edit.time}</span>
      <span class="grow"></span>
      <button type="button" class="link" onclick={() => endEdit(false)} disabled={edit.busy}>Cancel</button>
      <button type="submit" class="link strong" disabled={edit.busy}>Save</button>
    {:else if kind}
      {#if !review.touch}<span>Enter adds · {mac ? '⌘' : 'Ctrl'}+Enter sends</span>{/if}
    {:else if draft}
      <span class="what">{review.draftTime}{drawings ? ` · ${drawings}` : ''}</span>
      {#if review.touch}
        <span class="grow"></span>
        <button type="button" class="link" onclick={review.cancelDraft}>Cancel</button>
        <button type="submit" class="link strong" disabled={!review.draftHasContent || review.commentBusy}>Save</button>
      {:else}
        <span>Enter saves · Esc cancels</span>
      {/if}
    {/if}
  </div>
</form>
