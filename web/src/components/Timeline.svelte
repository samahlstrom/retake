<script lang="ts">
  // The timeline canvas (drawn by the frame loop) and the time tip that follows the pointer. Click or drag to seek.
  // The tip stays a plain element: it tracks the cursor with no delay, which a Bits UI tooltip anchored to a trigger
  // doesn't do.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();
</script>

<div id="timeline" {@attach review.attachTimeline}>
  <canvas id="tl" bind:this={review.el.tl}></canvas>
  <div
    id="tip"
    style:display={review.tip.show ? 'block' : null}
    style:left={review.tip.left == null ? null : `${review.tip.left}px`}
  >
    {review.tip.text}
  </div>
</div>
