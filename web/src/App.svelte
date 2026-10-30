<script lang="ts">
  import Stage from './components/Stage.svelte';
  import Bar from './components/Bar.svelte';
  import type { Review } from './lib/review.svelte';

  let { review }: { review: Review } = $props();
</script>

<svelte:window onkeydowncapture={review.onKeydown} onpagehide={review.flushOnExit} />
<svelte:document onmousedown={review.onDocMousedown} />

<Stage {review} />
<Bar {review} />
