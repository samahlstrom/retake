<script lang="ts">
  // The video, the frozen last frame while another version loads, the image cards, the pen layer, the hint, the toast
  // and the fatal message; in a home (spec/home.md), "All videos" and "Next video". In the phone layout the stage takes the video's shape (--ar), and in portrait the hint moves
  // out of it.
  import type { Review } from '../lib/review.svelte';
  import Hint from './Hint.svelte';
  import HomeNav from './HomeNav.svelte';
  import ImageLayer from './ImageLayer.svelte';
  import AgentLayer from './AgentLayer.svelte';
  import ExtSlot from '../ext/ExtSlot.svelte';

  let { review }: { review: Review } = $props();
  // An extension's kind of version (spec/extensions.md, "Version kinds"): its `version` slot where the video is, over
  // it (the video element stays, empty), under the toast and the back link.
  const kind = $derived(review.kindShown);
  const slotExt = $derived(kind ? review.versionExt : null);
</script>

<main
  id="stage"
  data-part="stage"
  class:kind={!!kind}
  bind:this={review.el.stage}
  style:--ar={review.touch && !kind ? review.aspect : undefined}
>
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
  {#if slotExt}
    <ExtSlot
      ext={slotExt}
      slot="version"
      host={review.extHost('version')}
      loader={review.extLoader}
      key={review.shown ?? ''}
      class="version-slot"
    />
  {:else if kind}
    <p class="version-slot kind-off" role="status" class:hidden={!review.extsLoaded}>
      {review.nameOf(review.shown)} is an extension’s version, and that extension isn’t on here.
    </p>
  {/if}
  {#if review.layoutMode !== 'portrait' && !kind}
    <Hint gone={review.hintGone} touch={review.touch} viewOnly={!review.canRecord} />
  {/if}
  <div id="toast" data-part="toast" class="float" class:show={review.toast.show}>{review.toast.msg}</div>
  <div id="fatal" style:display={review.fatal ? 'flex' : null}>{review.fatal}</div>
  {#if review.offline}<div id="offline" data-part="offline" role="status">The editing computer is offline</div>{/if}
  <HomeNav {review} />
</main>

<style>
  @layer retake {
    /* An extension's version fills the stage, over the (empty) video. */
    #stage :global(.version-slot) {
      position: absolute;
      inset: 0;
      overflow: auto;
      background: var(--bg);
    }
    .kind-off.hidden {
      visibility: hidden;
    }
    .kind-off {
      display: grid;
      place-items: center;
      margin: 0;
      padding: 40px;
      color: var(--muted);
      text-align: center;
    }
    /* Over the poster of a version whose computer is offline: a caption, not a broken player. */
    #offline {
      position: absolute;
      left: 50%;
      top: 50%;
      transform: translate(-50%, -50%);
      padding: 8px 14px;
      border-radius: var(--radius-pill);
      background: color-mix(in srgb, var(--scrim) 66%, transparent);
      color: var(--on-scrim);
      font-size: 14px;
      white-space: nowrap;
      pointer-events: none;
    }
  }
</style>
