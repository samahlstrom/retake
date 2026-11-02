<script lang="ts">
  // The one-line hint. Desktop: the keys, under the video. Phone layout: how to comment by touch, over the
  // video in landscape, or `inline` in the free space above Record in portrait. It fades out at the first record and
  // doesn't come back, in this browser (the keys are then behind ?). A viewer's share link (`viewOnly`) can't record:
  // the hint says so, and stays.
  let {
    gone,
    touch = false,
    inline = false,
    viewOnly = false,
  }: { gone: boolean; touch?: boolean; inline?: boolean; viewOnly?: boolean } = $props();
</script>

{#if !touch}
  <div id="hint" data-part="hint" class="float" class:gone>
    {#if viewOnly}
      <b>View only</b> · <b>Space</b> play · <b>←/→</b> 5 s · <b>C</b> compare
    {:else}
      Draw or type to comment · <b>R</b> record your voice · <b>Space</b> play · <b>1–4</b> pen color · <b>Z</b> undo ·
      <b>?</b> all keys
    {/if}
  </div>
{:else}
  <div id="hint" data-part="hint" class={inline ? 'touch inline' : 'float touch'} class:gone>
    {#if viewOnly}
      <b>View only</b>
    {:else}
      Draw on the video or type to comment · <b>Record</b> to talk
    {/if}
  </div>
{/if}
