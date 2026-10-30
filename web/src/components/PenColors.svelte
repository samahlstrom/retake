<script lang="ts">
  // The four pen colors (keys 1–4). The ring marks the current one.
  import { COLORS, COLOR_KEYS, type PenColor } from '../lib/constants';

  let { color, onpick }: { color: PenColor; onpick: (c: PenColor) => void } = $props();
</script>

<div id="colors">
  {#each COLOR_KEYS as c, i (c)}
    <button
      class="sw"
      class:on={c === color}
      data-color={c}
      title="{c} ({i + 1})"
      aria-label="{c} pen"
      style:background={COLORS[c]}
      onclick={() => onpick(c)}
    ></button>
  {/each}
</div>
