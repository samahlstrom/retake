<script lang="ts">
  // The bar under the video, left to right: play, time, timeline, version, the pen, Record (your voice), Send and ?
  // for the keys. Calm: no boxes around icons, Send the only filled control, and every part a fixed width, so nothing
  // moves when recording starts (the bar's top edge turns red, Record shows REC and the time, the pen stays a click
  // away). A viewer's share link has no pen, no Record and no Send. On an extension's kind of version (v0) there is no
  // video: no play, time, timeline or pen; the version, Record (when the kind records) and Send stay put. Where Send
  // can't reach an agent, one quiet line says so over the bar's right end.
  import type { Review } from '../lib/review.svelte';
  import VersionPicker from './VersionPicker.svelte';
  import PlayButton from './PlayButton.svelte';
  import Timeline from './Timeline.svelte';
  import PenButton from './PenButton.svelte';
  import RecordButton from './RecordButton.svelte';
  import SendButton from './SendButton.svelte';
  import SendNotice from './SendNotice.svelte';
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
    {#if !review.kindShown}<PenButton {review} />{/if}
    {#if review.recordHere}<RecordButton {review} />{/if}
    <SendButton {review} />
    <SendNotice {review} />
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
