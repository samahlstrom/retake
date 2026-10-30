<script lang="ts">
  // The bar under the video, left to right: play, time, timeline, version, pen colors, then the voice box (live
  // waveform, REC, Done). A red top edge while recording. A viewer's share link has no pen and no voice box.
  import type { Review } from '../lib/review.svelte';
  import PlayButton from './PlayButton.svelte';
  import Timeline from './Timeline.svelte';
  import VersionPicker from './VersionPicker.svelte';
  import PenColors from './PenColors.svelte';
  import VoiceBox from './VoiceBox.svelte';

  let { review }: { review: Review } = $props();
</script>

<div id="bar" class:recording={!!review.seg}>
  <PlayButton paused={review.paused} onclick={review.togglePlay} />
  <div id="time">
    <span id="cur">{review.curText}</span> <span class="dim">/ <span id="dur">{review.durText}</span></span>
  </div>
  <Timeline {review} />
  <VersionPicker {review} />
  {#if review.canRecord}
    <PenColors color={review.penColor} onpick={review.setColor} eraser={review.eraser} onerase={review.toggleEraser} />
    <VoiceBox {review} />
  {/if}
</div>
