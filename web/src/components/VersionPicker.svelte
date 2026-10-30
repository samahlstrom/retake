<script lang="ts">
  // The version label; click it for the compact list (newest first, the base marked with ·). Hidden for a single
  // version. Keys ([ ] C) are handled globally; focus never moves into the list.
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { versionTitle } from '../lib/versions';
  import { fmt } from '../lib/util';

  let { review }: { review: Review } = $props();

  const title = $derived(versionTitle(review.versions, review.base, review.shown, review.flipped));
  const newestFirst = $derived([...review.versions].reverse());
  const keepFocus = (e: Event) => e.preventDefault();
</script>

<div id="ver" hidden={review.versions.length < 2}>
  <Popover.Root bind:open={() => review.verOpen, (v) => (review.verOpen = v)}>
    <Popover.Trigger id="verBtn" class={review.flipped ? 'flipped' : ''} {title}>{review.shown || ''}</Popover.Trigger>
    <Popover.Content
      id="verList"
      side="top"
      sideOffset={12}
      align="center"
      trapFocus={false}
      onOpenAutoFocus={keepFocus}
      onCloseAutoFocus={keepFocus}
    >
      {#each newestFirst as v (v.label)}
        <button
          data-ver={v.label}
          class={v.label === review.base ? 'on' : ''}
          onclick={() => review.pickVersion(v.label)}
          ><span>{v.label}{v.label === review.base ? ' ·' : ''}</span><span class="d"
            >{fmt(v.duration || 0, false)}</span
          ></button
        >
      {/each}
    </Popover.Content>
  </Popover.Root>
</div>
