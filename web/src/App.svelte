<script lang="ts">
  import Stage from './components/Stage.svelte';
  import Bar from './components/Bar.svelte';
  import Sidebar from './components/sidebar/Sidebar.svelte';
  import PhoneControls from './components/PhoneControls.svelte';
  import type { Review } from './lib/review.svelte';

  let { review }: { review: Review } = $props();
</script>

<svelte:window onkeydowncapture={review.onKeydown} onpagehide={review.flushOnExit} />
<svelte:document onmousedown={review.onDocMousedown} />

<Stage {review} />
{#if review.layoutMode === 'desktop'}
  <Bar {review} />
{:else}
  <PhoneControls {review} />
{/if}
<Sidebar {review} />
