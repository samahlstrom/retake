<script lang="ts">
  // The one-line hint. Desktop: the keys, under the video. Phone layout: how to record and compare by touch, over the
  // video in landscape, or `inline` in the free space above Record in portrait. It fades out at the first record and
  // doesn't come back.
  let {
    gone,
    touch = false,
    inline = false,
    compare = true,
  }: { gone: boolean; touch?: boolean; inline?: boolean; compare?: boolean } = $props();

  const sep = ' · '; // an expression: Svelte would trim it as leading whitespace of the {#if} block
</script>

{#if !touch}
  <div id="hint" class="float" class:gone>
    <b>R</b> record · <b>Space</b> play · <b>←/→</b> 5 s · <b>1–4</b> pen color · <b>Z</b> undo · <b>C</b> compare version
  </div>
{:else}
  <div id="hint" class={inline ? 'touch inline' : 'float touch'} class:gone>
    Tap <b class="recdot">●</b> to record · draw while recording{#if compare}{sep}<b>⇄</b> compare{/if}
  </div>
{/if}
