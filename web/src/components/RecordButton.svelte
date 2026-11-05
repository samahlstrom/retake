<script lang="ts">
  // R, in the bar: record your voice. Idle, a microphone with the red record dot and "Record"; recording, a red ring
  // that follows your voice (the mic works), REC and the time, and a stop square. One fixed width, so nothing in the
  // bar moves when it starts or stops. Recording is optional: the pen and the comment box work without it.
  import type { Review } from '../lib/review.svelte';
  import MicGlyph from './MicGlyph.svelte';

  let { review }: { review: Review } = $props();

  const seg = $derived(review.seg);
  const recClass = $derived(seg ? 'on' : review.starting ? 'wait' : '');
  const name = $derived(
    seg
      ? `Stop recording (${review.shortcutLabel('toggleRec')})`
      : `Record voice (${review.shortcutLabel('toggleRec')})`,
  );
</script>

<button
  id="rec"
  data-part="record"
  class={recClass}
  aria-label={name}
  aria-pressed={!!seg}
  title={name}
  onclick={review.toggleRec}
  bind:this={review.el.rec}
>
  {#if seg}
    <span class="ring" aria-hidden="true"></span><span id="recLabel">REC {review.recElapsed}</span><span
      class="stop"
      aria-hidden="true"
    ></span>
  {:else}
    <MicGlyph /><span id="recLabel">{review.starting ? 'Mic…' : 'Record'}</span>
  {/if}
</button>
