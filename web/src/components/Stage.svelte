<script lang="ts">
  // The video, the frozen last frame while another version loads, the image cards, the pen layer, the hint, the toast
  // and the fatal message; in a home (spec/home.md), "All videos" and "Next video". In the phone layout the stage takes the video's shape (--ar), and in portrait the hint moves
  // out of it.
  import type { Review } from '../lib/review.svelte';
  import Hint from './Hint.svelte';
  import HomeNav from './HomeNav.svelte';
  import ImageLayer from './ImageLayer.svelte';
  import AgentLayer from './AgentLayer.svelte';

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
  <AgentLayer {review} />
  <canvas id="ink" class:pen={!!review.seg} class:erasing={review.eraser && review.canRecord} {@attach review.attachInk}
  ></canvas>
  {#if review.layoutMode !== 'portrait'}
    <Hint gone={review.hintGone} touch={review.touch} viewOnly={!review.canRecord} />
  {/if}
  <div id="toast" class="float" class:show={review.toast.show}>{review.toast.msg}</div>
  <div id="fatal" style:display={review.fatal ? 'flex' : null}>{review.fatal}</div>
  {#if review.offline}<div id="offline" role="status">The editing computer is offline</div>{/if}
  <HomeNav {review} />
</main>

<style>
  /* Over the poster of a version whose computer is offline: a caption, not a broken player. */
  #offline {
    position: absolute;
    left: 50%;
    top: 50%;
    transform: translate(-50%, -50%);
    padding: 8px 14px;
    border-radius: 999px;
    background: rgb(0 0 0 / 0.66);
    color: #e8e8ec;
    font-size: 14px;
    white-space: nowrap;
    pointer-events: none;
  }
</style>
