<script lang="ts">
  // The comment box at the bottom of the comments (spec/notes.md, "Typed comments on a video"). On a video: "Comment
  // at 0:18 · goes into v8…". Focusing it, typing, or drawing on the video while not recording pauses the video and
  // starts a pending comment; the lines drawn meanwhile go with it. Enter saves it (Ctrl/⌘+Enter also sends it), Esc
  // cancels it with its lines. On an extension's version: "Comment for the agent…", anchored where the version's
  // slot says (spec/extensions.md, "Typing one"). One quiet line under it says what Enter does, only while it's in use.
  import type { Review } from '../../lib/review.svelte';
  import { composeKey } from '../../lib/notes/typed';
  import { intoVersion } from '../../lib/notes/view';

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
  /** The line under the box: while a comment is pending (or typed, on a kind's version). */
  const shown = $derived(kind ? typing : !!draft);
  const drawings = $derived(lines === 1 ? '1 drawing' : lines ? `${lines} drawings` : '');

  let box = $state<HTMLTextAreaElement | null>(null);
  $effect(() => {
    review.el.comment = box;
  });
  /** The box grows with what's typed, up to a few lines. */
  $effect(() => {
    void review.commentDraft;
    if (!box) return;
    box.style.height = 'auto';
    box.style.height = `${Math.min(box.scrollHeight, 160)}px`;
  });

  function key(e: KeyboardEvent) {
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
    // Left empty, with no lines: nothing is pending.
    if (!kind && draft && !review.draftHasContent) review.draft = null;
  }
</script>

<form
  class="compose"
  data-part="comment-line"
  class:pending={shown}
  onsubmit={(e) => {
    e.preventDefault();
    review.addComment(false);
  }}
>
  <textarea
    bind:this={box}
    bind:value={review.commentDraft}
    rows="1"
    {placeholder}
    aria-label={kind ? 'Comment for the agent' : placeholder.replace('…', '')}
    readonly={review.commentBusy}
    onkeydown={key}
    onfocus={() => review.startDraft()}
    oninput={() => review.startDraft()}
    onblur={blur}></textarea>
  <div class="under" class:shown>
    {#if kind}
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
