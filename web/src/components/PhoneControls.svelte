<script lang="ts">
  // The phone layout's controls (the desktop has the bar instead). Same parts, sized for a thumb:
  //  - portrait, under the video: the timeline; play, time, version and flip; the pen colors and undo (#pbar). At the
  //    bottom, in the thumb zone: the hint, Record and Done (#precord). The free space between is the grid's `side`
  //    row (phone.css).
  //  - landscape, around the video (which fills the height): the pen colors and undo at the left edge, Done and a big
  //    round Record at the right edge, and a slim bar at the bottom with play, time, the timeline, version and flip.
  import type { Review } from '../lib/review.svelte';
  import PlayButton from './PlayButton.svelte';
  import Timeline from './Timeline.svelte';
  import VersionPicker from './VersionPicker.svelte';
  import PenColors from './PenColors.svelte';
  import FlipButton from './FlipButton.svelte';
  import UndoButton from './UndoButton.svelte';
  import PhoneRecord from './PhoneRecord.svelte';
  import Hint from './Hint.svelte';

  let { review }: { review: Review } = $props();
</script>

{#snippet time()}
  <div id="time">
    <span id="cur">{review.curText}</span> <span class="dim">/ <span id="dur">{review.durText}</span></span>
  </div>
{/snippet}
{#snippet done()}
  <button id="done" class:saved={review.doneSaved} onclick={review.done}>{review.doneSaved ? 'Saved' : 'Done'}</button>
{/snippet}

{#if review.layoutMode === 'portrait'}
  <div id="pbar" class="portrait" class:recording={!!review.seg}>
    <Timeline {review} />
    <div class="prow">
      <PlayButton paused={review.paused} onclick={review.togglePlay} />
      {@render time()}
      <span class="grow"></span>
      <VersionPicker {review} />
      <FlipButton {review} />
    </div>
    <div class="prow">
      <PenColors color={review.penColor} onpick={review.setColor} />
      <span class="grow"></span>
      <UndoButton onclick={review.undo} />
    </div>
  </div>
  <div id="precord">
    <Hint gone={review.hintGone} touch inline compare={review.versions.length > 1} />
    <div class="prow">
      <PhoneRecord {review} />
      {@render done()}
    </div>
  </div>
{:else}
  <div id="pleft">
    <PenColors color={review.penColor} onpick={review.setColor} />
    <UndoButton onclick={review.undo} compact />
  </div>
  <div id="pright" class:recording={!!review.seg}>
    {@render done()}
    <span class="grow"></span>
    <PhoneRecord {review} round />
  </div>
  <div id="pbar" class="landscape" class:recording={!!review.seg}>
    <PlayButton paused={review.paused} onclick={review.togglePlay} />
    {@render time()}
    <Timeline {review} />
    <VersionPicker {review} />
    <FlipButton {review} />
  </div>
{/if}
