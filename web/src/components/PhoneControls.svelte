<script lang="ts">
  // The phone layout's controls (the desktop has the bar instead). Same parts, sized for a thumb:
  //  - portrait, under the video: the timeline; play, time, version and the agent's dot; the pen colors and undo
  //    (#pbar). At the bottom, in the thumb zone: the hint (once), Record and Send (#precord). The free space between is
  //    the grid's `side` row (phone.css), where the comments open.
  //  - landscape, around the video (which fills the height): the pen colors and undo at the left edge, Send and a big
  //    round Record at the right edge, and a slim bar at the bottom with play, time, the timeline, version and agent.
  // A viewer's share link has no pen, undo, Record or Send; the hint says "View only". On an extension's kind of
  // version (v0) there is no video: in portrait the slot fills the page down to Record and Send (the version and the
  // agent are in the comments drawer's header); in landscape no timeline, play, time, pens or undo. Record only when
  // the kind records.
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

{#if review.layoutMode === 'portrait' && review.kindShown}
  <div id="precord" data-part="bar" class="kind" class:recording={!!review.seg}>
    {#if review.canRecord}
      <div class="prow">
        {#if review.recordHere}<PhoneRecord {review} />{/if}
        <SendButton {review} />
      </div>
    {/if}
  </div>
{:else if review.layoutMode === 'portrait'}
  <div id="pbar" data-part="bar" class="portrait" class:recording={!!review.seg}>
    {#if !review.kindShown}<Timeline {review} />{/if}
    <div class="prow">
      {#if !review.kindShown}
        <PlayButton paused={review.paused} onclick={review.togglePlay} />
        {@render time()}
      {/if}
      <VersionPicker {review} />
      <span class="grow"></span>
      {#if review.canRecord && review.agent.available}<AgentStatus {review} />{/if}
    </div>
    {#if review.canRecord && !review.kindShown}
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
    {#if !review.kindShown}<Hint gone={review.hintGone} touch inline viewOnly={!review.canRecord} />{/if}
    {#if review.canRecord}
      <div class="prow">
        {#if review.recordHere}<PhoneRecord {review} />{/if}
        <SendButton {review} />
      </div>
    {/if}
  </div>
{:else}
  <div id="pleft" data-part="bar">
    {#if review.canRecord && !review.kindShown}
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
      {#if review.recordHere}<PhoneRecord {review} round />{/if}
    {/if}
  </div>
  <div id="pbar" data-part="bar" class="landscape" class:recording={!!review.seg}>
    {#if review.kindShown}
      <span class="grow"></span>
    {:else}
      <PlayButton paused={review.paused} onclick={review.togglePlay} />
      {@render time()}
      <Timeline {review} />
    {/if}
    <VersionPicker {review} />
    {#if review.canRecord && review.agent.available}<AgentStatus {review} compact />{/if}
  </div>
{/if}
