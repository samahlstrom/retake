<script lang="ts">
  // ?: every key of the player, over the video. The one-line hint shows once per browser; after that the keys live
  // here, behind the ? in the bar (or the ? key).
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { SHORTCUT_ACTIONS, type ShortcutAction } from '../lib/keys';

  let { review }: { review: Review } = $props();
</script>

<Popover.Root bind:open={() => review.keysOpen, (v) => (review.keysOpen = v)}>
  <Popover.Trigger
    id="keys"
    data-part="keys"
    aria-label="Keyboard shortcuts"
    title="Keys ({review.shortcutLabel('help')})"
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
        {#each SHORTCUT_ACTIONS as binding (binding.action)}
          {@const action = binding.action as ShortcutAction}
          <dt>
            <input
              aria-label={binding.label}
              value={review.shortcutLabel(action)}
              maxlength="1"
              autocapitalize="characters"
              oninput={(event) => review.setShortcut(action, event.currentTarget.value)}
            />
          </dt>
          <dd>{binding.label}</dd>
        {/each}
      </dl>
      {#if review.shortcutError}<p class="key-error" role="alert">{review.shortcutError}</p>{/if}
      <button class="reset-keys" onclick={review.resetShortcuts}>Reset defaults</button>
      <p class="note">Draw, type or record a comment at any moment.</p>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
