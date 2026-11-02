<script lang="ts">
  // The bar under the video, left to right: play, time, timeline, version, pen colors, then Record, the one action
  // (Send N to agent) and the agent's status; ? for the keys. A red top edge while recording. A
  // viewer's share link has no pen, no Record and no action. On an extension's kind of version (v0) there is no video:
  // no play, time, timeline or pens; the version, Record (when the kind records), Send and the agent stay put.
  import type { Review } from '../lib/review.svelte';
  import VersionPicker from './VersionPicker.svelte';
  import PlayButton from './PlayButton.svelte';
  import Timeline from './Timeline.svelte';
  import PenColors from './PenColors.svelte';
  import RecordButton from './RecordButton.svelte';
  import SendButton from './SendButton.svelte';
  import AgentStatus from './AgentStatus.svelte';
  import KeysSheet from './KeysSheet.svelte';

  let { review }: { review: Review } = $props();
</script>

<div id="bar" data-part="bar" class:recording={!!review.seg}>
  {#if review.kindShown}
    <span class="grow"></span>
  {:else}
    <PlayButton paused={review.paused} onclick={review.togglePlay} />
    <div id="time" data-part="time">
      <span id="cur">{review.curText}</span> <span class="dim">/ <span id="dur">{review.durText}</span></span>
    </div>
    <Timeline {review} />
  {/if}
  <VersionPicker {review} />
  {#if review.canRecord}
    {#if !review.kindShown}
      <PenColors
        color={review.penColor}
        onpick={review.setColor}
        eraser={review.eraser}
        onerase={review.toggleEraser}
      />
    {/if}
    <div id="voicebox">
      {#if review.recordHere}<RecordButton {review} />{/if}
      <SendButton {review} />
      {#if review.agent.available}<AgentStatus {review} />{/if}
    </div>
  {/if}
  <KeysSheet {review} />
</div>

<style>
  @layer retake {
    .grow {
      flex: 1;
    }
  }
</style>
