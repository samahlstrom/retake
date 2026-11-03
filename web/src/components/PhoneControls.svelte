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
  // A kind with the agent chat (spec/chat.md) has the chat's pill in place of Record: tapping it opens the chat with
  // the keyboard up, its mic records a voice message.
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
  import { raiseKeyboard } from '../lib/viewport.svelte';

  let { review }: { review: Review } = $props();

  // Portrait: Record and Send are fixed to the bottom (phone.css), with a spacer of their height in the grid. Their
  // lift raises them over whatever the browser lays over the page's bottom edge (the visual viewport ends above the
  // layout one); the page's viewport store (lib/viewport.svelte.ts) holds it while a field has the focus and while the
  // keyboard goes down, so the bar never jumps. While typing it fades out (spec/notes.md, "Typing on a phone").
  let barHeight = $state(0);
  const lift = $derived(review.viewport.lift);
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
        {#if review.chatShown}
          <!-- The chat (spec/chat.md): the pill opens it with the keyboard up; its mic sends a voice message. -->
          <div class="chat-pill" class:recording={!!review.seg} data-part="chat-pill">
            <!-- iOS raises the keyboard only for a field focused in the tap itself: the stand-in takes the focus at
                 once, then the chat's composer takes it over and the keyboard stays (spec/notes.md). -->
            <button
              class="ask"
              onclick={() => {
                raiseKeyboard('chat');
                review.openChat();
              }}>{review.seg ? `Recording ${review.recElapsed}` : 'Ask the agent…'}</button
            >
            <button
              class="mic"
              aria-label={review.seg ? 'Stop and send the voice message' : 'Record a voice message'}
              onclick={review.toggleRec}
              ><svg viewBox="0 0 24 24" aria-hidden="true"
                >{#if review.seg}<rect x="7" y="7" width="10" height="10" rx="2" class="fill" />{:else}<rect
                    x="8.6"
                    y="3"
                    width="6.8"
                    height="11.4"
                    rx="3.4"
                  /><path d="M5.4 11.2a6.6 6.6 0 0 0 13.2 0M12 17.8V21" />{/if}</svg
              ></button
            >
          </div>
        {:else if review.recordHere}<PhoneRecord {review} />{/if}
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

<style>
  @layer retake {
    .chat-pill {
      flex: 1;
      min-width: 0;
      display: flex;
      align-items: center;
      height: 44px;
      border: 1px solid color-mix(in srgb, var(--overlay) 12%, transparent);
      border-radius: 22px;
      background: color-mix(in srgb, var(--overlay) 4%, transparent);
    }
    .chat-pill.recording {
      border-color: color-mix(in srgb, var(--record) 70%, transparent);
    }
    .chat-pill .ask {
      flex: 1;
      min-width: 0;
      height: 100%;
      padding: 0 4px 0 16px;
      border: 0;
      background: none;
      color: var(--faint);
      font: inherit;
      font-size: 16px;
      text-align: left;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .chat-pill.recording .ask {
      color: var(--record);
      font-variant-numeric: tabular-nums;
    }
    .chat-pill .mic {
      flex: none;
      display: grid;
      place-items: center;
      width: 44px;
      height: 44px;
      padding: 0;
      border: 0;
      background: none;
      color: var(--muted);
    }
    .chat-pill.recording .mic {
      color: var(--record);
    }
    .chat-pill svg {
      width: 20px;
      height: 20px;
      fill: none;
      stroke: currentColor;
      stroke-width: 1.8;
      stroke-linecap: round;
    }
    .chat-pill svg .fill {
      fill: currentColor;
      stroke: none;
    }
  }
</style>
