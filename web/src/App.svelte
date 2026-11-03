<script lang="ts">
  import { onMount } from 'svelte';
  import Stage from './components/Stage.svelte';
  import Bar from './components/Bar.svelte';
  import Sidebar from './components/sidebar/Sidebar.svelte';
  import PhoneControls from './components/PhoneControls.svelte';
  import type { Review } from './lib/review.svelte';

  let { review }: { review: Review } = $props();
  onMount(() => {
    void review.automation.refresh();
    const timer = setInterval(() => {
      review.clock = Date.now() / 1000;
      void review.automation.refresh();
    }, 5000);
    return () => clearInterval(timer);
  });
  // Compose mode (spec/notes.md, "Typing on a phone"): phone.css shows only the video and the comment box.
  $effect(() => {
    document.documentElement.toggleAttribute('data-compose', review.composing);
  });
  // A kind's version with the chat (spec/chat.md): its thread and its document, for the column and for Send.
  $effect(() => {
    if (!review.chatShown) return;
    void review.chat.refresh();
    void review.chat.loadDoc();
  });
</script>

<svelte:window onkeydowncapture={review.onKeydown} onpagehide={review.onPageHide} />
<svelte:document onmousedown={review.onDocMousedown} />

<Stage {review} />
{#if review.layoutMode === 'desktop'}
  <Bar {review} />
{:else}
  <PhoneControls {review} />
{/if}
<Sidebar {review} />
