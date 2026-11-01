<script lang="ts">
  // The one-line hint. Desktop: the keys, under the video. Phone layout: how to record and compare by touch, over the
  // video in landscape, or `inline` in the free space above Record in portrait. It fades out at the first record and
  // doesn't come back. A viewer's share link (`viewOnly`) can't record: the hint says so, and stays.
  let {
    gone,
    touch = false,
    inline = false,
    viewOnly = false,
  }: { gone: boolean; touch?: boolean; inline?: boolean; viewOnly?: boolean } = $props();
</script>

{#if !touch}
  <div id="hint" class="float" class:gone>
    {#if viewOnly}
      <b>View only</b> · <b>Space</b> play · <b>←/→</b> 5 s
    {:else}
      <b>R</b> record · <b>Space</b> play · <b>←/→</b> 5 s · <b>1–4</b> pen color · <b>E</b> eraser · <b>Z</b> undo
    {/if}
  </div>
{:else}
  <div id="hint" class={inline ? 'touch inline' : 'float touch'} class:gone>
    {#if viewOnly}
      <b>View only</b>
    {:else}
      Tap <b class="recdot">●</b> to record · draw while recording
    {/if}
  </div>
{/if}
