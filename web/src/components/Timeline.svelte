<script lang="ts">
  // The timeline canvas (drawn by the frame loop) and the time tip that follows the pointer. Click or drag to seek.
  // The tip stays a plain element: it tracks the cursor with no delay, which a Bits UI tooltip anchored to a trigger
  // doesn't do.
  import type { Review } from '../lib/review.svelte';
  import { COLORS } from '../lib/constants';

  let { review }: { review: Review } = $props();
  const markers = $derived([
    ...review.timelineComments.map((row) => ({ row, drawing: false })),
    ...review.timelineDrawings.map((row) => ({ row, drawing: true })),
  ]);
</script>

<div id="timeline" {@attach review.attachTimeline}>
  <canvas id="tl" bind:this={review.el.tl}></canvas>
  {#each markers as marker (`${marker.drawing ? 'drawing' : 'comment'}:${marker.row.key}`)}
    {@const row = marker.row}
    <button
      class={marker.drawing ? 'drawing-marker' : 'comment-marker'}
      style:--drawing-color={COLORS[row.drawings[0] as keyof typeof COLORS] ?? COLORS.red}
      data-comment={row.key}
      class:resolved={row.mark === 'answered'}
      style:left={`calc(8px + (100% - 16px) * ${Math.min(1, Math.max(0, row.t0 / (review.timelineDuration || 1)))})`}
      aria-label={`${marker.drawing ? 'Drawing · ' : ''}${row.time}: ${row.by}: ${row.text || 'Drawing'}`}
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
      onclick={() => review.jumpTo(row.t0)}
    ></button>
  {/each}
  <div
    id="tip"
    style:display={review.tip.show ? 'block' : null}
    style:left={review.tip.left == null ? null : `${review.tip.left}px`}
  >
    <span class="tip-time">{review.tip.text}</span>
    {#each review.hoverRows as row (row.key)}<div class="tip-comment">
        <strong>{row.by}</strong><span>{row.time}{row.mark === 'answered' ? ' · Resolved' : ''}</span>
        <p>{row.text || 'Drawing on this frame'}</p>
      </div>{/each}
  </div>
</div>
