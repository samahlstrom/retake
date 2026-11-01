<script lang="ts">
  // The bar's one action. The owner sends: "Send 12 to agent" finishes this take and hands every open comment to the
  // agent (lib/status.ts, sendButton). A reviewer finishes: "Finish review". With no computer to send to, it saves the
  // take and opens the way to connect one, instead of failing.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();
  const b = $derived(review.button);
</script>

<button
  id="done"
  class:primary={b.primary}
  class:saved={b.label === 'Saved' || b.label === 'Sent'}
  aria-busy={review.finishing}
  title={b.primary
    ? 'Finish this take and send your open comments to the agent'
    : 'Stop and save your feedback. Comments stay open until they are edited.'}
  onclick={review.primary}>{b.label}</button
>
