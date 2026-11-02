<script lang="ts">
  // The pen: one button, its pen in the colour you draw with (the eraser while that's on). It opens the four colours
  // and the eraser; keys 1–4 and E work without it. The pen draws at any moment: recording, its lines go with what
  // you say; not recording, with a typed comment (spec/notes.md, "Typed comments on a video").
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { COLORS } from '../lib/constants';
  import PenColors from './PenColors.svelte';

  let { review, side = 'top' }: { review: Review; side?: 'top' | 'right' | 'bottom' } = $props();

  const label = $derived(review.eraser ? 'Eraser' : `Pen, ${review.penColor}`);
  const keep = (e: Event) => e.preventDefault();
</script>

<Popover.Root bind:open={() => review.penOpen, (v) => (review.penOpen = v)}>
  <Popover.Trigger
    id="pen"
    data-part="pen-button"
    class={review.eraser ? 'erasing' : ''}
    data-color={review.eraser ? 'eraser' : review.penColor}
    aria-label="{label}: colours and eraser"
    title="{label} (1–4, E)"
  >
    {#if review.eraser}
      <svg viewBox="0 0 24 24" aria-hidden="true"
        ><path
          d="M14.2 4.6 19.4 9.8 10.6 18.6H6.2L4.4 16.8a1.4 1.4 0 0 1 0-2z"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linejoin="round"
        /><path
          d="M8.6 10.2 13.8 15.4M10.6 18.6H20"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
        /></svg
      >
    {:else}
      <!-- The pen's body is the colour (painted with currentColor), its outline the text colour. -->
      <svg viewBox="0 0 24 24" aria-hidden="true" class="tip" style:color={COLORS[review.penColor]}
        ><path d="M15.6 4.4a2 2 0 0 1 2.8 0l1.2 1.2a2 2 0 0 1 0 2.8L9 19l-4.6 1.2L5.6 15.6z" fill="currentColor" /><path
          class="edge"
          d="M15.6 4.4a2 2 0 0 1 2.8 0l1.2 1.2a2 2 0 0 1 0 2.8L9 19l-4.6 1.2L5.6 15.6zM13.8 6.2l4 4"
          fill="none"
          stroke-width="1.3"
          stroke-linejoin="round"
        /></svg
      >
    {/if}
  </Popover.Trigger>
  <Popover.Portal>
    <Popover.Content
      class="pen-pop"
      {side}
      align="center"
      sideOffset={10}
      collisionPadding={10}
      onOpenAutoFocus={keep}
      onCloseAutoFocus={keep}
    >
      <PenColors
        color={review.penColor}
        onpick={(c) => {
          review.setColor(c);
          review.penOpen = false;
        }}
        eraser={review.eraser}
        onerase={() => {
          review.toggleEraser();
          review.penOpen = false;
        }}
      />
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
