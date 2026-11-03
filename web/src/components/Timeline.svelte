<script lang="ts">
  // The timeline canvas (drawn by the frame loop) and one marker per comment, colored by its status, plus a "?" for
  // each question the agent asked from inside its task, at the question's own moment. Where you talked
  // and drew shows on the canvas only until it becomes a comment; then the comment's marker stands for it. The tip
  // follows the pointer. Click or drag to seek.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();
</script>

<div id="timeline" data-part="timeline" {@attach review.attachTimeline}>
  <canvas id="tl" bind:this={review.el.tl}></canvas>
  {#each review.timelineComments as row (row.key)}
    {@const status = review.statusOf(row)}
    {@const state = status.state}
    <button
      class="comment-marker {state}"
      class:now={status.doing != null}
      data-part="marker"
      data-state={state}
      data-comment={row.key}
      class:resolved={state === 'done'}
      style:left={`calc(8px + (100% - 16px) * ${Math.min(1, Math.max(0, row.t0 / (review.timelineDuration || 1)))})`}
      aria-label={`${row.time}: ${row.text || 'Drawing'}`}
      onfocus={(e) => {
        review.hoverRows = [row];
        review.tip.show = true;
        review.tip.text = row.time;
        const parent = e.currentTarget.parentElement;
        review.tip.left = Math.max(150, Math.min((parent?.clientWidth ?? 300) - 150, e.currentTarget.offsetLeft));
      }}
      onblur={() => {
        review.tip.show = false;
      }}
      onclick={() => review.jumpToComment(row)}
    ></button>
  {/each}
  {#each review.timelineAsks as row (`ask:${row.ask!.id}`)}
    {@const ask = row.ask!}
    <button
      class="ask-marker"
      data-part="ask-marker"
      data-ask={ask.id}
      class:on={review.selectedAsk === ask.id}
      style:left={`calc(8px + (100% - 16px) * ${Math.min(1, Math.max(0, ask.t / (review.timelineDuration || 1)))})`}
      aria-label={`The agent's question at ${ask.time}: ${ask.text}`}
      title={`${ask.time} · ${ask.text}`}
      onclick={() => review.selectAsk(row)}><span aria-hidden="true">?</span></button
    >
  {/each}
  <div
    id="tip"
    data-part="tip"
    style:display={review.tip.show ? 'block' : null}
    style:left={review.tip.left == null ? null : `${review.tip.left}px`}
  >
    <span class="tip-time">{review.tip.text}</span>
    {#each review.hoverRows as row (row.key)}<div class="tip-comment">
        <span class="tip-state {review.statusOf(row).state}">{review.manyAuthors ? `${row.by} · ` : ''}{row.time}</span>
        <p>{row.text || 'Drawing'}</p>
      </div>{/each}
  </div>
</div>
