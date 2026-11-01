<script lang="ts">
  // The phone layout's controls (the desktop has the bar instead). Same parts, sized for a thumb:
  //  - portrait, under the video: the timeline; play, time, version and the agent's dot; the pen colors and undo
  //    (#pbar). At the bottom, in the thumb zone: the hint (once), Record and Send (#precord). The free space between is
  //    the grid's `side` row (phone.css), where the comments open.
  //  - landscape, around the video (which fills the height): the pen colors and undo at the left edge, Send and a big
  //    round Record at the right edge, and a slim bar at the bottom with play, time, the timeline, version and agent.
  // A viewer's share link has no pen, undo, Record or Send; the hint says "View only".
  import type { Review } from '../lib/review.svelte';
  import VersionPicker from './VersionPicker.svelte';
  import AgentStatus from './AgentStatus.svelte';
  import SendButton from './SendButton.svelte';
  import PlayButton from './PlayButton.svelte';
  import Timeline from './Timeline.svelte';
  import PenColors from './PenColors.svelte';
  import UndoButton from './UndoButton.svelte';
  import PhoneRecord from './PhoneRecord.svelte';
  import Hint from './Hint.svelte';

  let { review }: { review: Review } = $props();
</script>

{#snippet time()}
  <div id="time" data-part="time">
    <span id="cur">{review.curText}</span> <span class="dim">/ <span id="dur">{review.durText}</span></span>
  </div>
{/snippet}

{#if review.layoutMode === 'portrait'}
  <div id="pbar" data-part="bar" class="portrait" class:recording={!!review.seg}>
    <Timeline {review} />
    <div class="prow">
      <PlayButton paused={review.paused} onclick={review.togglePlay} />
      {@render time()}
      <VersionPicker {review} />
      <span class="grow"></span>
      {#if review.canRecord && review.agent.available}<AgentStatus {review} />{/if}
    </div>
    {#if review.canRecord}
      <div class="prow">
        <PenColors
          color={review.penColor}
          onpick={review.setColor}
          eraser={review.eraser}
          onerase={review.toggleEraser}
        />
        <span class="grow"></span>
        <UndoButton onclick={review.undo} />
      </div>
    {/if}
  </div>
  <div id="precord" data-part="bar">
    <Hint gone={review.hintGone} touch inline viewOnly={!review.canRecord} />
    {#if review.canRecord}
      <div class="prow">
        <PhoneRecord {review} />
        <SendButton {review} />
      </div>
    {/if}
  </div>
{:else}
  <div id="pleft" data-part="bar">
    {#if review.canRecord}
      <PenColors
        color={review.penColor}
        onpick={review.setColor}
        eraser={review.eraser}
        onerase={review.toggleEraser}
      />
      <UndoButton onclick={review.undo} compact />
    {/if}
  </div>
  <div id="pright" data-part="bar" class:recording={!!review.seg}>
    {#if review.canRecord}
      <SendButton {review} />
      <span class="grow"></span>
      <PhoneRecord {review} round />
    {/if}
  </div>
  <div id="pbar" data-part="bar" class="landscape" class:recording={!!review.seg}>
    <PlayButton paused={review.paused} onclick={review.togglePlay} />
    {@render time()}
    <Timeline {review} />
    <VersionPicker {review} />
    {#if review.canRecord && review.agent.available}<AgentStatus {review} compact />{/if}
  </div>
{/if}
