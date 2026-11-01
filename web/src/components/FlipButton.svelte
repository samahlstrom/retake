<script lang="ts">
  // The phone layout's C: flips between the base and its partner ("v7 ⇄ v6", the one showing is bright). It looks
  // disabled when C would be refused (the partner ends before the playhead), and a tap then shows C's refusal toast.
  import type { Review } from '../lib/review.svelte';
  import { flipPair } from '../lib/versions';

  let { review }: { review: Review } = $props();

  const pair = $derived(flipPair(review.versions, review.base));
</script>

{#if pair}
  <button
    id="flip"
    data-part="flip"
    class:flipped={review.flipped}
    aria-disabled={review.flipBlocked ? 'true' : undefined}
    aria-label="Compare {pair[0]} with {pair[1]} (C)"
    title={review.flipBlocked ?? `Compare ${pair[0]} with ${pair[1]}`}
    onclick={review.flip}
    ><span class:on={review.shown === pair[0]}>{pair[0]}</span><span class="arr">⇄</span><span
      class:on={review.shown === pair[1]}>{pair[1]}</span
    ></button
  >
{/if}
