<script lang="ts">
  // Images on the video (milestone 2): a faint framed card at each image's box, over the frame and under the pen, so
  // strokes go over images. Like strokes, a card shows within ±1.5 s of its moment. While recording, this take's cards
  // have a corner grip (above the pen layer) that resizes them. Drops and pastes anywhere on the page land here; the
  // page refuses them unless recording, with the same hint as the pen.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();

  let resizing: { id: number; pointer: number } | null = null;

  const grab = (e: PointerEvent, id: number) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    resizing = { id, pointer: e.pointerId };
  };
  const move = (e: PointerEvent) => {
    if (resizing && e.pointerId === resizing.pointer) review.resizeImage(resizing.id, review.normPoint(e));
  };
  const release = (e: PointerEvent) => {
    if (resizing && e.pointerId === resizing.pointer) resizing = null;
  };
</script>

<svelte:document
  ondragover={review.onDragOver}
  ondrop={review.onDrop}
  onpaste={review.onPaste}
  onpointermove={review.onPointerMove}
/>

<div
  id="images"
  style:left="{review.frame.left}px"
  style:top="{review.frame.top}px"
  style:width="{review.frame.width}px"
  style:height="{review.frame.height}px"
>
  {#each review.cards as c (c.key)}
    <div
      class="card"
      data-id={c.id}
      style:left="{c.box[0] * 100}%"
      style:top="{c.box[1] * 100}%"
      style:width="{c.box[2] * 100}%"
      style:height="{c.box[3] * 100}%"
    >
      <img src={c.url} alt="" draggable="false" style:opacity={c.alpha} />
      {#if c.id != null}
        {@const id = c.id}
        <button
          class="grip"
          tabindex="-1"
          title="Drag to resize"
          aria-label="Resize image"
          onpointerdown={(e) => grab(e, id)}
          onpointermove={move}
          onpointerup={release}
          onpointercancel={release}
        ></button>
      {/if}
    </div>
  {/each}
</div>

<style>
  @layer retake {
    /* No z-index or opacity on the layer and the card: they must not form stacking contexts, so the grip's z-index lifts
       it over the pen canvas while the image stays under it. */
    #images {
      position: absolute;
      pointer-events: none;
    }
    .card {
      position: absolute;
    }
    img {
      display: block;
      width: 100%;
      height: 100%;
      object-fit: contain;
      border-radius: var(--radius-sm);
      border: 1px solid color-mix(in srgb, var(--on-scrim-strong) 30%, transparent);
      background: color-mix(in srgb, var(--scrim) 55%, transparent);
      box-shadow: 0 10px 28px color-mix(in srgb, var(--shadow) 50%, transparent);
    }
    .grip {
      position: absolute;
      right: -9px;
      bottom: -9px;
      width: 22px;
      height: 22px;
      padding: 0;
      border: 0;
      background: none;
      z-index: 1;
      pointer-events: auto;
      touch-action: none;
      cursor: nwse-resize;
    }
    .grip::after {
      content: '';
      position: absolute;
      right: 7px;
      bottom: 7px;
      width: 8px;
      height: 8px;
      border-right: 2px solid color-mix(in srgb, var(--on-scrim-strong) 85%, transparent);
      border-bottom: 2px solid color-mix(in srgb, var(--on-scrim-strong) 85%, transparent);
      border-bottom-right-radius: 3px;
    }
  }
</style>
