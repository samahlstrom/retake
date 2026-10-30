<script lang="ts">
  // The one-line key hint under the video. It fades out at the first R and doesn't come back.
  let { gone }: { gone: boolean } = $props();
</script>

<div id="hint" class="float" class:gone>
  <b>R</b> record · <b>Space</b> play · <b>←/→</b> 5 s · <b>1–4</b> pen color · <b>Z</b> undo · <b>C</b> compare version
</div>
