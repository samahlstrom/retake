<script lang="ts">
  // The phone layout's controls (the desktop has the bar instead). The same parts, sized for a thumb (40 px and up):
  //  - portrait, under the video: the timeline; play, time, the version, then the pen and undo (#pbar), compact (a
  //    40 px row just under the track) so the comments get the height. At the bottom,
  //    in the thumb zone: Record and Send (#precord), over them only where Send can't reach an agent one quiet line, and
  //    a viewer's "View only". The space between is the grid's `side` row (phone.css): the comments are always there,
  //    with the comment box at their bottom, so it is never empty.
  //  - landscape, around the video (which fills the height): the pen and undo at the left edge, Send and a big round
  //    Record at the right edge, and a slim bar at the bottom with play, time, the timeline and the version.
  // A viewer's share link has no pen, undo, Record or Send; the hint says "View only". On an extension's kind of
  // version (v0) there is no video: in portrait the slot fills the page down to Record and Send (the version is in the
  // comments drawer's header); in landscape no timeline, play, time, pen or undo. Record only when the kind records.
  import type { Review } from '../lib/review.svelte';
  import VersionPicker from './VersionPicker.svelte';
  import SendButton from './SendButton.svelte';
  import SendNotice from './SendNotice.svelte';
  import PlayButton from './PlayButton.svelte';
  import Timeline from './Timeline.svelte';
  import PenButton from './PenButton.svelte';
  import UndoButton from './UndoButton.svelte';
  import PhoneRecord from './PhoneRecord.svelte';
  import Hint from './Hint.svelte';

  let { review }: { review: Review } = $props();

  // Portrait: Record and Send are fixed to the bottom (phone.css), with a spacer of their height in the grid. `lift`
  // raises them over whatever the browser lays over the page's bottom edge (the visual viewport ends above the layout
  // one); while a text field has the focus that's the on-screen keyboard, and the bar stays put behind it.
  let barHeight = $state(0);
  let lift = $state(0);
  const EDITABLE = 'input, textarea, select, [contenteditable]:not([contenteditable=false])';
  $effect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const measure = () => {
      if ((document.activeElement as Element | null)?.closest?.(EDITABLE)) return;
      lift = Math.max(0, Math.round(innerHeight - (vv.offsetTop + vv.height)));
    };
    measure();
    vv.addEventListener('resize', measure);
    vv.addEventListener('scroll', measure);
    document.addEventListener('focusout', measure);
    return () => {
      vv.removeEventListener('resize', measure);
      vv.removeEventListener('scroll', measure);
      document.removeEventListener('focusout', measure);
    };
  });
</script>

{#snippet space()}
  <div id="pspace" aria-hidden="true" style:height="{barHeight + lift}px"></div>
{/snippet}

{#snippet time()}
  <div id="time" data-part="time">
    <span id="cur">{review.curText}</span> <span class="dim">/ <span id="dur">{review.durText}</span></span>
  </div>
{/snippet}

{#if review.layoutMode === 'portrait' && review.kindShown}
  <div
    id="precord"
    data-part="bar"
    class="kind"
    class:recording={!!review.seg}
    style:bottom="{lift}px"
    bind:offsetHeight={barHeight}
  >
    {#if review.canRecord}
      <SendNotice {review} />
      <div class="prow">
        {#if review.recordHere}<PhoneRecord {review} />{/if}
        <SendButton {review} />
      </div>
    {/if}
  </div>
  {@render space()}
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
      {#if review.canRecord && !review.kindShown}
        <PenButton {review} side="bottom" />
        <UndoButton onclick={review.undo} compact />
      {/if}
    </div>
  </div>
  <div id="precord" data-part="bar" style:bottom="{lift}px" bind:offsetHeight={barHeight}>
    <!-- The comments above say how to comment (their empty line, the box's placeholder), so the hint is only for a
         viewer ("View only") or where there are no comments to show. -->
    {#if !review.kindShown && (!review.canRecord || !review.sidebar.panel)}<Hint
        gone={review.hintGone}
        touch
        inline
        viewOnly={!review.canRecord}
      />{/if}
    {#if review.canRecord}
      <SendNotice {review} />
      <div class="prow">
        {#if review.recordHere}<PhoneRecord {review} />{/if}
        <SendButton {review} />
      </div>
    {/if}
  </div>
  {@render space()}
{:else}
  <div id="pleft" data-part="bar">
    {#if review.canRecord && !review.kindShown}
      <PenButton {review} side="right" />
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
    {#if review.canRecord}<SendNotice {review} />{/if}
  </div>
{/if}
