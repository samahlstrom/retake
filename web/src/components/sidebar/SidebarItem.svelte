<script lang="ts">
  import type { Review } from '../../lib/review.svelte';
  import type { Row } from '../../lib/notes/view';
  import TaskControls from './TaskControls.svelte';
  let { review, row, active }: { review: Review; row: Row; active: boolean } = $props();
  const answering = $derived(row.answer != null && review.seg?.answer_to === row.answer);
</script>

<li class="item" class:active data-key={row.key} data-mark={row.mark}>
  <div class="comment-author">
    <span class="avatar" class:editor={row.kind === 'question'}>{row.by.slice(0, 1).toUpperCase()}</span>
    <strong>{row.by}</strong>
    {#if row.kind === 'note' && review.canApprove}
      <button
        class="delete-comment"
        aria-label="Delete comment"
        title="Delete comment"
        disabled={review.notes.deleting}
        onclick={() => review.notes.deleteComment(row.id)}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"
          ><path
            d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 10v7m4-7v7"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          /></svg
        >
      </button>
    {/if}
    {#if row.version && row.version !== review.shown}<span class="origin-version">{row.version}</span>{/if}
    {#if row.mark === 'answered'}<span class="resolved" title="Edit completed"
        >✓ <span>Resolved{row.answeredIn ? ` in ${row.answeredIn}` : ''}</span></span
      >{/if}
  </div>
  <div class="comment-body">
    <button class="time jump" title="Go to {row.time}" onclick={() => review.jumpToComment(row)}>{row.time}</button>
    <p class="text">
      {row.text || (row.drawings.length || row.images.length ? 'Drawing on this frame' : 'Empty comment')}
    </p>
    {#if row.images.length}<div class="thumbs">
        {#each row.images as im, i (i)}<button
            title="View annotation at {row.time}"
            onclick={() => review.jumpToComment(row)}
            ><img src={im.url} alt="Attached annotation" draggable="false" /></button
          >{/each}
      </div>{/if}
    {#if row.change}<div class="editor-reply">
        <strong>Completed edit</strong>
        <p class="change">{row.change}</p>
        {#if row.see}<button class="act" onclick={() => review.setBase(row.see!)}>View {row.see}</button>{/if}
      </div>{/if}
    {#if row.question}<div class="editor-reply">
        <strong>Editor</strong>
        <p class="asked">{row.question}</p>
      </div>{/if}
    {#if row.reopen || row.answer != null}<div class="acts">
        {#if row.reopen}<button class="act reopen" onclick={() => review.reopen(row)}>Reopen</button>{/if}
        {#if row.answer != null}{@const thread = row.answer}<button
            class="answer"
            class:on={answering}
            onclick={() => review.answer(thread, row.t0)}
            ><span class="dot"></span>{answering ? 'Stop reply' : 'Record reply'}</button
          >{/if}
      </div>{/if}
    {#if row.kind === 'note'}<TaskControls {review} {row} />{/if}
  </div>
</li>
