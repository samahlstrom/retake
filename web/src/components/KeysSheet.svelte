<script lang="ts">
  // ?: every key of the player, over the video. The one-line hint shows once per browser; after that the keys live
  // here, behind the ? in the bar (or the ? key).
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { KEY_SHEET } from '../lib/keys';

  let { review }: { review: Review } = $props();
</script>

<Popover.Root bind:open={() => review.keysOpen, (v) => (review.keysOpen = v)}>
  <Popover.Trigger id="keys" aria-label="Keyboard shortcuts" title="Keys (?)">?</Popover.Trigger>
  <Popover.Portal>
    <Popover.Content
      class="pop keys-pop"
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
      <p class="note">The pen draws only while you record.</p>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
