<script lang="ts">
  // The video, the frozen last frame while another version loads, the image cards, the pen layer, the hint, the toast
  // and the fatal message; in a home (spec/home.md), "All videos" and "Next video". In the phone layout the stage takes the video's shape (--ar), and in portrait the hint moves
  // out of it.
  import type { Review } from '../lib/review.svelte';
  import Hint from './Hint.svelte';
  import HomeNav from './HomeNav.svelte';
  import ImageLayer from './ImageLayer.svelte';

  let { review }: { review: Review } = $props();
</script>

<main id="stage" bind:this={review.el.stage} style:--ar={review.touch ? review.aspect : undefined}>
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
    ondurationchange={review.onDurationChange}
    onerror={review.onVideoError}
  ></video>
  <canvas id="freeze" bind:this={review.el.freeze} style:display={review.freezeShown ? 'block' : null}></canvas>
  <ImageLayer {review} />
  <canvas id="ink" class:pen={!!review.seg} class:erasing={review.eraser && review.canRecord} {@attach review.attachInk}
  ></canvas>
  {#if review.layoutMode !== 'portrait'}
    <Hint gone={review.hintGone} touch={review.touch} viewOnly={!review.canRecord} />
  {/if}
  <div id="toast" class="float" class:show={review.toast.show}>{review.toast.msg}</div>
  <div id="fatal" style:display={review.fatal ? 'flex' : null}>{review.fatal}</div>
  <HomeNav {review} />
</main>
