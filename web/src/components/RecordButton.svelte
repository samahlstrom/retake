<script lang="ts">
  // R, in the bar: one fixed-width button. Idle it says Record and its key; recording, it shows the elapsed time, the
  // live waveform (your mic is working) and the segment. Its width never changes, so nothing in the bar moves.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();

  const seg = $derived(review.seg);
  const recClass = $derived(seg ? 'on' : review.starting ? 'wait' : '');
  const label = $derived(seg ? `REC ${review.recElapsed}` : review.starting ? 'Mic…' : 'Record');
  const recTitle = $derived(
    seg ? `Recording segment ${seg.id}. Press R to stop.` : 'Press R to record your voice and pen',
  );
</script>

<button id="rec" aria-label="Record" class={recClass} title={recTitle} onclick={review.toggleRec}
  ><span class="dot"></span><span id="recLabel">{label}</span><canvas
    id="wave"
    title="Your voice (live while recording)"
    bind:this={review.el.wave}
  ></canvas><span class="k" id="recKey">{seg ? seg.id : 'R'}</span></button
>
