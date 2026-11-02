<script lang="ts">
  // One quiet line where Send can't reach an agent (spec/automation.md, "No agent menu in the bar"): the local
  // worker's reason, or on a hosted project that no editing computer is online, with Settings for the owner (where a
  // computer connects). Nothing at all while Send works.
  import type { Review } from '../lib/review.svelte';
  import { sendNotice } from '../lib/status';

  let { review }: { review: Review } = $props();
  const notice = $derived(sendNotice(review.agent, review.owner));
</script>

{#if notice}
  <p class="send-notice" data-part="send-notice agent-status" role="status">
    <span class="dot" aria-hidden="true"></span><span class="text">{notice.text}</span
    >{#if notice.settings && review.settingsHref}<a href={review.settingsHref}>Settings</a>{/if}
  </p>
{/if}
