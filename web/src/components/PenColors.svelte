<script lang="ts">
  // The four pen colors (keys 1–4) and the eraser (E). The ring marks the current tool.
  import { COLORS, COLOR_KEYS, type PenColor } from '../lib/constants';
  import type { ShortcutAction } from '../lib/keys';

  let {
    color,
    onpick,
    eraser = false,
    onerase,
    keyOf,
  }: {
    color: PenColor;
    onpick: (c: PenColor) => void;
    eraser?: boolean;
    onerase?: () => void;
    keyOf: (action: ShortcutAction) => string;
  } = $props();
</script>

<div id="colors" data-part="pens">
  {#each COLOR_KEYS as c, i (c)}
    <button
      class="sw"
      data-part="pen"
      class:on={c === color && !eraser}
      data-color={c}
      title="{c} ({keyOf(`color${i + 1}` as ShortcutAction)})"
      aria-label="{c} pen"
      style:background={COLORS[c]}
      onclick={() => onpick(c)}
    ></button>
  {/each}
  {#if onerase}
    <button
      id="eraser"
      data-part="eraser"
      class="sw er"
      class:on={eraser}
      title="eraser ({keyOf('eraser')})"
      aria-label="eraser"
      aria-pressed={eraser}
      onclick={onerase}
    >
      <svg viewBox="0 0 16 16" aria-hidden="true"
        ><path
          d="M9.6 2.4 13.6 6.4 7.2 12.8H3.6L2.4 11.6a1 1 0 0 1 0-1.4z"
          fill="none"
          stroke="currentColor"
          stroke-width="1.4"
          stroke-linejoin="round"
        /><path d="M5.6 6.4 9.6 10.4" stroke="currentColor" stroke-width="1.4" /></svg
      >
    </button>
  {/if}
</div>
