<script lang="ts">
  // The phone layout's R: one big button in the thumb zone. While recording it shows the elapsed time, the segment id
  // and the live waveform. Portrait: a wide pill with all of it inside. Landscape (`round`): a round button at the
  // right edge, the time and the waveform just above it.
  import type { Review } from '../lib/review.svelte';

  let { review, round = false }: { review: Review; round?: boolean } = $props();

  const seg = $derived(review.seg);
  const recClass = $derived(seg ? 'on' : review.starting ? 'wait' : '');
  const label = $derived(seg ? `REC ${review.recElapsed}` : review.starting ? 'Mic…' : 'Record');
</script>

{#if round}
  <div id="recInfo" class={recClass}>
    <span id="recLabel">{label}</span><span class="k" id="recKey">{seg ? seg.id : ''}</span>
  </div>
  <canvas id="wave" class={recClass} bind:this={review.el.wave}></canvas>
  <button
    id="rec"
    data-part="record"
    class="round {recClass}"
    aria-label={seg ? `Stop recording ${seg.id}` : 'Record'}
    onclick={review.toggleRec}><span class="dot"></span></button
  >
{:else}
  <button
    id="rec"
    data-part="record"
    class="pill {recClass}"
    aria-label={seg ? `Stop recording ${seg.id}` : 'Record'}
    onclick={review.toggleRec}
    ><span class="dot"></span><span id="recLabel">{label}</span><span class="k" id="recKey">{seg ? seg.id : ''}</span
    ><canvas id="wave" bind:this={review.el.wave}></canvas></button
  >
{/if}
