<script lang="ts">
  // The phone layout's R: record your voice, in the thumb zone. Idle, a microphone with the red record dot; recording,
  // a red ring that follows your voice, the elapsed time, the segment id and the live waveform, and a stop square.
  // Portrait: a wide pill with all of it inside. Landscape (`round`): a round button at the right edge, the time and
  // the waveform just above it.
  import type { Review } from '../lib/review.svelte';
  import MicGlyph from './MicGlyph.svelte';

  let { review, round = false }: { review: Review; round?: boolean } = $props();

  const seg = $derived(review.seg);
  const recClass = $derived(seg ? 'on' : review.starting ? 'wait' : '');
  const label = $derived(seg ? `REC ${review.recElapsed}` : review.starting ? 'Mic…' : 'Record');
  const name = $derived(seg ? `Stop recording ${seg.id}` : `Record voice (${review.shortcutLabel('toggleRec')})`);
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
    aria-label={name}
    aria-pressed={!!seg}
    onclick={review.toggleRec}
    bind:this={review.el.rec}
    >{#if seg}<span class="ring" aria-hidden="true"></span><span class="stop" aria-hidden="true"></span>{:else}<MicGlyph
        size={30}
      />{/if}</button
  >
{:else}
  <button
    id="rec"
    data-part="record"
    class="pill {recClass}"
    aria-label={name}
    aria-pressed={!!seg}
    onclick={review.toggleRec}
    bind:this={review.el.rec}
    >{#if seg}<span class="ring" aria-hidden="true"></span>{:else}<MicGlyph size={24} />{/if}<span id="recLabel"
      >{label}</span
    ><span class="k" id="recKey">{seg ? seg.id : ''}</span><canvas id="wave" bind:this={review.el.wave}
    ></canvas>{#if seg}<span class="stop" aria-hidden="true"></span>{/if}</button
  >
{/if}
