<script lang="ts">
  // The one-line hint. Desktop: the keys, under the video. Phone layout: how to comment by touch, over the
  // video in landscape, or `inline` in the free space above Record in portrait. It fades out at the first record and
  // doesn't come back, in this browser (the keys are then behind ?). A viewer's share link (`viewOnly`) can't record:
  // the hint says so, and stays.
  import type { Review } from '../lib/review.svelte';

  let {
    review,
    gone,
    touch = false,
    inline = false,
    viewOnly = false,
  }: { review: Review; gone: boolean; touch?: boolean; inline?: boolean; viewOnly?: boolean } = $props();
</script>

{#if !touch}
  <div id="hint" data-part="hint" class="float" class:gone>
    {#if viewOnly}
      <b>View only</b> · <b>Space</b> play · <b>{review.shortcutLabel('back')}/{review.shortcutLabel('forward')}</b> 5 s
      · <b>{review.shortcutLabel('flip')}</b> compare
    {:else}
      Draw or type to comment · <b>{review.shortcutLabel('toggleRec')}</b> record your voice · <b>Space</b> play ·
      <b>{review.shortcutLabel('color1')}–{review.shortcutLabel('color4')}</b>
      pen color · <b>{review.shortcutLabel('undo')}</b> undo ·
      <b>{review.shortcutLabel('help')}</b> all keys
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
