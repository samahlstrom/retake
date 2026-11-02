<script lang="ts">
  // The comments, beside the video (desktop) or under the controls (phone). The header (spec/notes.md, "The comments
  // header"): a speech bubble, the version as a menu (every version with where its comments go and what's still to
  // do), search and hide; then To do · Done. Each version shows its own comments only. At the bottom, the comment box
  // ("Comment at 0:18 · goes into v8…"). Progress (recording, transcribing, an undo after a delete) floats over the
  // bottom of the list, so nothing in it moves. N or the › hides the column (remembered); the speech bubble over the
  // video, with the to-do count, brings it back. Extensions add tabs after To do · Done (spec/extensions.md, "UI
  // slots"): a sidebar tab shows its slot in place of the list; a review panel widens the column to half the page. On
  // a computer the column's left edge is a handle that drags it wider or narrower (spec/notes.md, "The column's
  // width"), remembered per viewer.
  import { Collapsible, ScrollArea, Tooltip } from 'bits-ui';
  import { onMount, tick } from 'svelte';
  import type { Review } from '../../lib/review.svelte';
  import SidebarItem from './SidebarItem.svelte';
  import VersionMenu from './VersionMenu.svelte';
  import CommentBox from './CommentBox.svelte';
  import ExtSlot from '../../ext/ExtSlot.svelte';
  import Chat from '../chat/Chat.svelte';
  import { batchLine } from '../../lib/status';
  import {
    browserStorage,
    clampSide,
    loadSideWidth,
    saveSideWidth,
    sideWidthCss,
    SIDE_MAX,
    SIDE_MIN,
    SIDE_STEP,
    SIDE_STEP_BIG,
  } from '../../lib/side-width';
  import './sidebar.css';

  let { review }: { review: Review } = $props();

  const notes = $derived(review.notes);
  const view = $derived(review.sidebar);
  const job = $derived(notes.job);
  const tab = $derived(review.extTab);
  /** A kind's version with the agent chat (spec/chat.md): the column is the chat, no To do · Done. */
  const chatting = $derived(review.chatShown);
  const pill = $derived(
    job && !view.rows.length
      ? job.text
      : review.todoCount === 1
        ? '1 to do'
        : review.todoCount
          ? `${review.todoCount} to do`
          : review.versionRows.length === 1
            ? '1 comment'
            : `${review.versionRows.length} comments`,
  );
  const asking = $derived(review.versionRows.some((row) => review.statusOf(row).state === 'question'));
  const working = $derived(review.versionRows.some((row) => review.statusOf(row).state === 'working'));
  const dot = $derived(
    chatting
      ? review.chat.view.turn
        ? 'w'
        : ''
      : job?.error
        ? 'err'
        : job
          ? 'busy'
          : asking
            ? 'q'
            : working
              ? 'w'
              : '',
  );
  /** A phone: the header and To do · Done share one row (the list needs the height), unless an extension adds
   * tabs. */
  const oneRow = $derived(review.layoutMode !== 'desktop' && !review.extTabs.length && !review.kindDrawer);
  /** "Agent working on 3 comments": the batch the agent works on, as one group. */
  const batch = $derived(batchLine(review.agent));
  const setOpen = (v: boolean) => {
    if (v !== view.panel) review.toggleSidebar();
  };
  /** A phone in portrait: the comments are always there, and the bubble goes to them (To do, from the top). */
  const pinned = $derived(review.commentsPinned);

  // The column's width (desktop): null until dragged (the default, --sidebar-width), read once the page is up. It
  // reaches #side's CSS as --side-w on the root (so the grid's column and the stage beside it reflow).
  let width = $state<number | null>(null);
  /** What the column measures now: the handle's value. */
  let measured = $state(0);
  let dragging = $state(false);
  let grip = $state<HTMLDivElement | null>(null);
  onMount(() => {
    width = loadSideWidth(browserStorage());
  });
  $effect(() => {
    const root = document.documentElement;
    if (!view.phone && width != null) root.style.setProperty('--side-w', sideWidthCss(width));
    else root.style.removeProperty('--side-w');
    return () => root.style.removeProperty('--side-w');
  });
  $effect(() => {
    const side = grip?.parentElement;
    if (!side || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(() => (measured = Math.round(side.getBoundingClientRect().width)));
    ro.observe(side);
    return () => ro.disconnect();
  });
  const resize = (w: number) => (width = clampSide(w, innerWidth));
  // The handle: drag, ←/→ (← widens: the edge moves left), double-click back to the default.
  function grab(e: PointerEvent) {
    if (e.button !== 0) return;
    const el = e.currentTarget as HTMLElement;
    const start = e.clientX,
      from = el.parentElement?.getBoundingClientRect().width ?? measured;
    el.setPointerCapture(e.pointerId);
    dragging = true;
    const move = (m: PointerEvent) => resize(from - (m.clientX - start));
    const up = () => {
      dragging = false;
      el.removeEventListener('pointermove', move);
      el.removeEventListener('pointerup', up);
      el.removeEventListener('pointercancel', up);
      if (width != null) saveSideWidth(browserStorage(), width);
    };
    el.addEventListener('pointermove', move);
    el.addEventListener('pointerup', up);
    el.addEventListener('pointercancel', up);
    e.preventDefault();
  }
  function gripKey(e: KeyboardEvent) {
    const step = e.shiftKey ? SIDE_STEP_BIG : SIDE_STEP;
    const now = clampSide(width ?? measured, innerWidth); // as wide as it is now, a smaller window included
    if (e.key === 'ArrowLeft') resize(now + step);
    else if (e.key === 'ArrowRight') resize(now - step);
    else return;
    e.preventDefault();
    saveSideWidth(browserStorage(), width);
  }
  function resetWidth() {
    width = null;
    saveSideWidth(browserStorage(), null);
  }

  let searching = $state(false);
  let input = $state<HTMLInputElement | null>(null);
  async function openSearch() {
    searching = true;
    await tick();
    input?.focus();
  }
  function closeSearch() {
    searching = false;
    review.commentSearch = '';
  }
</script>

{#snippet tabs()}
  <div class="tabs" data-part="tabs" role="tablist" aria-label="Comments">
    <div class="seg">
      <button
        role="tab"
        aria-selected={!tab && review.commentFilter === 'todo'}
        class:on={!tab && review.commentFilter === 'todo'}
        onclick={() => review.showComments('todo')}>To do <b>{review.todoCount}</b></button
      >
      <button
        role="tab"
        aria-selected={!tab && review.commentFilter === 'done'}
        class:on={!tab && review.commentFilter === 'done'}
        onclick={() => review.showComments('done')}>Done <b>{review.doneCount}</b></button
      >
    </div>
    {#each review.extTabs as t (t.key)}
      <button
        role="tab"
        class="ext"
        data-ext-tab={t.key}
        aria-selected={tab?.key === t.key}
        class:on={tab?.key === t.key}
        onclick={() => (review.sideTab = t.key)}>{t.title}</button
      >
    {/each}
  </div>
{/snippet}

{#if notes.available}
  <Tooltip.Provider delayDuration={500}>
    <Collapsible.Root bind:open={() => view.panel, setOpen}>
      {#snippet child()}
        {#if view.toggle}
          <Tooltip.Root>
            <Tooltip.Trigger id="sideToggle">
              {#snippet child({ props })}
                <!-- A speech bubble and the to-do count (only the bubble at 0), as on Scripts' phone top bar; a status
                     shows as a dot on it. -->
                <Collapsible.Trigger
                  {...props}
                  data-part="sidebar-toggle"
                  class={['compact', view.panel && 'open'].filter(Boolean).join(' ')}
                  aria-label={chatting
                    ? `${view.panel ? 'Hide' : 'Show'} the chat`
                    : pinned
                      ? `Comments: ${pill}`
                      : `${view.panel ? 'Hide' : 'Show'} comments: ${pill}`}
                  ><svg viewBox="0 0 24 24" aria-hidden="true"
                    ><path
                      d="M6 5h12a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-7l-4.5 3.5V17H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Z"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.7"
                      stroke-linejoin="round"
                    /></svg
                  >{#if review.todoCount && !chatting}<span class="n">{review.todoCount}</span>{/if}{#if dot}<span
                      class="dot {dot}"
                    ></span>{/if}</Collapsible.Trigger
                >
              {/snippet}
            </Tooltip.Trigger>
            <Tooltip.Content class="sideTip" side="left" sideOffset={8}>
              {chatting
                ? view.panel
                  ? 'Hide the chat'
                  : 'Chat with the agent'
                : pinned
                  ? 'To do'
                  : view.panel
                    ? 'Hide comments'
                    : 'Show comments'} <b>N</b>
            </Tooltip.Content>
          </Tooltip.Root>
        {/if}
        <Collapsible.Content
          id="side"
          data-part="sidebar"
          aria-label={chatting ? 'Chat' : 'Comments'}
          class={[
            tab?.slot === 'review-panel' && 'wide',
            review.kindShown && 'kind',
            review.kindDrawer && 'drawer',
            chatting && 'chatting',
          ]
            .filter(Boolean)
            .join(' ') || undefined}
        >
          {#if !view.phone}
            <!-- A focusable separator with a value is a widget (WAI-ARIA window splitter); svelte counts it as static. -->
            <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
            <div
              bind:this={grip}
              class="grip"
              class:dragging
              role="separator"
              aria-orientation="vertical"
              aria-label={chatting ? 'Resize the chat' : 'Resize the comments'}
              aria-valuenow={measured || width || undefined}
              aria-valuemin={SIDE_MIN}
              aria-valuemax={SIDE_MAX}
              tabindex="0"
              data-part="sidebar-grip"
              onpointerdown={grab}
              onkeydown={gripKey}
              ondblclick={resetWidth}
            ></div>
          {/if}
          <header>
            {#if searching}
              <input
                bind:this={input}
                class="search"
                type="search"
                placeholder="Search comments"
                aria-label="Search comments"
                bind:value={review.commentSearch}
                onkeydown={(e) => {
                  if (e.key === 'Escape') {
                    e.stopPropagation();
                    closeSearch();
                  }
                }}
                onblur={() => {
                  if (!review.commentSearch.trim()) searching = false;
                }}
              />
            {:else}
              <span class="cicon" aria-hidden="true" title="Comments"
                ><svg viewBox="0 0 24 24"
                  ><path
                    d="M6 5h12a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-7l-4.5 3.5V17H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Z"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.7"
                    stroke-linejoin="round"
                  /></svg
                ></span
              >
              <VersionMenu {review} />
              {#if oneRow && !chatting}{@render tabs()}{/if}
              <span class="h"></span>
            {/if}
            {#if !chatting}<button
                class="icon"
                class:on={searching}
                aria-label={searching ? 'Close search' : 'Search comments'}
                title={searching ? 'Close search' : 'Search'}
                onclick={() => (searching ? closeSearch() : openSearch())}
              >
                {#if searching}
                  <svg viewBox="0 0 20 20" aria-hidden="true"
                    ><path
                      d="M5.5 5.5l9 9m0-9-9 9"
                      stroke="currentColor"
                      stroke-width="1.6"
                      stroke-linecap="round"
                    /></svg
                  >
                {:else}
                  <svg viewBox="0 0 20 20" aria-hidden="true"
                    ><circle cx="8.75" cy="8.75" r="5" fill="none" stroke="currentColor" stroke-width="1.6" /><path
                      d="m12.5 12.5 4 4"
                      stroke="currentColor"
                      stroke-width="1.6"
                      stroke-linecap="round"
                    /></svg
                  >
                {/if}
              </button>{/if}
            {#if review.kindDrawer}
              <Collapsible.Trigger
                class="icon hide"
                title={chatting ? 'Hide the chat' : 'Hide comments'}
                aria-label={chatting ? 'Hide the chat' : 'Hide comments'}
              >
                <svg viewBox="0 0 20 20" aria-hidden="true"
                  ><path
                    d="M5 8l5 5 5-5"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.7"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  /></svg
                >
              </Collapsible.Trigger>
            {:else if !view.phone}
              <Collapsible.Trigger
                class="icon hide"
                title={chatting ? 'Hide the chat (N)' : 'Hide comments (N)'}
                aria-label={chatting ? 'Hide the chat' : 'Hide comments'}
              >
                <svg viewBox="0 0 20 20" aria-hidden="true"
                  ><path
                    d="M8 5l5 5-5 5"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.7"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  /></svg
                >
              </Collapsible.Trigger>
            {/if}
          </header>
          {#if !oneRow && !chatting}{@render tabs()}{/if}
          {#if chatting}
            <Chat {review} />
          {:else if tab}
            <ExtSlot
              ext={tab.ext}
              slot={tab.slot}
              host={review.extHost(tab.slot)}
              loader={review.extLoader}
              key={review.shown ?? ''}
              class="side-slot"
            />
          {:else}
            <div class="list">
              <div class="fill">
                <ScrollArea.Root class="scroll" type="hover">
                  <ScrollArea.Viewport class="vp">
                    {#if view.rows.length}
                      <ol>
                        {#each view.rows as row (row.key)}
                          <SidebarItem {review} {row} active={row.key === view.active} />
                        {/each}
                      </ol>
                    {:else if review.commentSearch.trim()}
                      <p class="empty">No comments match “{review.commentSearch.trim()}”</p>
                    {:else if review.commentFilter === 'done'}
                      <p class="empty">Nothing done yet<span>Comments the agent finishes land here.</span></p>
                    {:else if !job}
                      <p class="empty">
                        {review.versionRows.length ? 'All done' : 'No comments yet'}<span
                          >{!review.canRecord
                            ? 'Comments will show up here.'
                            : review.kindShown
                              ? review.touch
                                ? 'Type one below, or tap Record and talk it out.'
                                : 'Type one below, or press R and talk it out.'
                              : review.touch
                                ? 'Draw on the video, type below, or tap Record and talk.'
                                : 'Draw on the video, type below, or press R and talk.'}</span
                        >
                      </p>
                    {/if}
                  </ScrollArea.Viewport>
                  <ScrollArea.Scrollbar orientation="vertical" class="bar">
                    <ScrollArea.Thumb class="thumb" />
                  </ScrollArea.Scrollbar>
                </ScrollArea.Root>
              </div>
              <div class="strip" data-part="progress" aria-live="polite">
                {#if notes.deleted.length}
                  <div class="deleted-comment" role="status">
                    <span
                      >{notes.deleted.length === 1
                        ? 'Comment deleted'
                        : `${notes.deleted.length} comments deleted`}</span
                    ><button onclick={notes.undoDelete}>Undo</button>
                  </div>
                {:else if notes.deleteError}
                  <p class="delete-error" role="alert">{notes.deleteError}</p>
                {:else if job}
                  <button
                    class="job"
                    class:err={job.error}
                    title={job.error ? 'Dismiss' : ''}
                    onclick={notes.dismissJob}
                  >
                    <span class="pulse"></span>
                    <span class="jt"
                      >{job.text}{#if job.hint}<span class="hint">{job.hint}</span>{/if}</span
                    >
                  </button>
                {:else if review.seg}
                  <div class="recording-comment">
                    <span class="pulse"></span>{review.seg.talk
                      ? 'Recording.'
                      : 'Recording. Comments appear here as you talk.'}
                  </div>
                {:else if batch}
                  <!-- The agent's batch, as one group: one line while it works (spec/automation.md, "Batches"). -->
                  <div class="batch-line" role="status"><span class="pulse"></span>{batch}</div>
                {/if}
              </div>
            </div>
          {/if}
          {#if review.canRecord && !tab && !chatting}<CommentBox {review} />{/if}
        </Collapsible.Content>
      {/snippet}
    </Collapsible.Root>
  </Tooltip.Provider>
{/if}
