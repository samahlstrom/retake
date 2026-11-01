<script lang="ts">
  // The bar's one action, the same for everyone who can record: "Send 12 to agent" finishes this take and hands every
  // open comment to the agent (lib/status.ts, sendButton). With no computer online they wait in the queue and start
  // when one connects; local mode that can't edit saves them, and the agent menu next to it says what's missing.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();
  const b = $derived(review.button);
</script>

<button
  id="done"
  data-part="send"
  class="primary"
  class:saved={b.label === 'Saved' || b.label === 'Sent'}
  aria-busy={review.finishing || review.sending}
  title={b.action === 'save'
    ? 'Save this take. The agent menu says what it needs before it can edit.'
    : 'Finish this take and send your open comments to the agent'}
  onclick={review.primary}>{b.label}</button
>
