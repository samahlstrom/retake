<script lang="ts">
  // The live waveform (scrolls while recording, a flat grey line when idle), REC and Done.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();

  const seg = $derived(review.seg);
  const recClass = $derived(seg ? 'on' : review.starting ? 'wait' : '');
  const label = $derived(seg ? `REC ${review.recElapsed}` : review.starting ? 'Mic…' : 'Record');
  const recTitle = $derived(
    seg ? `Recording segment ${seg.id}. Press R to stop.` : 'Press R to record your voice and pen',
  );
</script>

<div id="voicebox">
  <canvas id="wave" title="Your voice (live while recording)" bind:this={review.el.wave}></canvas>
  <button id="rec" aria-label="Record" class={recClass} title={recTitle} onclick={review.toggleRec}
    ><span class="dot"></span><span id="recLabel">{label}</span><span class="k" id="recKey">{seg ? seg.id : 'R'}</span
    ></button
  >
  <button id="done" class:saved={review.doneSaved} onclick={review.done}>{review.doneSaved ? 'Saved' : 'Done'}</button>
</div>
