<script lang="ts">
  // ?: every key of the player, over the video. The one-line hint shows once per browser; after that the keys live
  // here, behind the ? in the bar (or the ? key).
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { KEY_SHEET } from '../lib/keys';

  let { review }: { review: Review } = $props();
</script>

<Popover.Root bind:open={() => review.keysOpen, (v) => (review.keysOpen = v)}>
  <Popover.Trigger id="keys" data-part="keys" aria-label="Keyboard shortcuts" title="Keys (?)"
    ><svg viewBox="0 0 24 24" aria-hidden="true"
      ><circle cx="12" cy="12" r="8.6" fill="none" stroke="currentColor" stroke-width="1.6" /><path
        d="M9.7 9.6a2.4 2.4 0 0 1 4.7.6c0 1.7-2.4 2-2.4 3.6M12 16.6v.01"
        fill="none"
        stroke="currentColor"
        stroke-width="1.7"
        stroke-linecap="round"
      /></svg
    ></Popover.Trigger
  >
  <Popover.Portal>
    <Popover.Content
      class="pop keys-pop"
      data-part="keys-sheet"
      side="top"
      align="end"
      sideOffset={12}
      collisionPadding={12}
      onOpenAutoFocus={(e) => e.preventDefault()}
      onCloseAutoFocus={(e) => e.preventDefault()}
    >
      <strong>Keys</strong>
      <dl>
        {#each KEY_SHEET as k (k.what)}
          <dt>
            {#each k.keys as key (key)}<kbd>{key}</kbd>{/each}
          </dt>
          <dd>{k.what}</dd>
        {/each}
      </dl>
      <p class="note">Draw, type or record a comment at any moment.</p>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
