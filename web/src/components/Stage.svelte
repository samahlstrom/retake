<script lang="ts">
  // The video, the frozen last frame while another version loads, the image cards, the pen layer, the hint, the toast
  // and the fatal message.
  import type { Review } from '../lib/review.svelte';
  import Hint from './Hint.svelte';
  import ImageLayer from './ImageLayer.svelte';

  let { review }: { review: Review } = $props();
</script>

<main id="stage" bind:this={review.el.stage}>
  <video
    id="video"
    preload="auto"
    playsinline
    bind:this={review.el.video}
    onplay={review.onPlay}
    onpause={review.onPause}
    onended={review.onEnded}
    onseeking={review.onSeeking}
    onloadedmetadata={review.onLoadedMetadata}
    onerror={review.onVideoError}
  ></video>
  <canvas id="freeze" bind:this={review.el.freeze} style:display={review.freezeShown ? 'block' : null}></canvas>
  <ImageLayer {review} />
  <canvas id="ink" class:pen={!!review.seg} {@attach review.attachInk}></canvas>
  <Hint gone={review.hintGone} />
  <div id="toast" class="float" class:show={review.toast.show}>{review.toast.msg}</div>
  <div id="fatal" style:display={review.fatal ? 'flex' : null}>{review.fatal}</div>
</main>
