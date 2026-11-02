<script lang="ts">
  // The comments, beside the video (desktop) or under the controls (phone). The header (spec/notes.md, "The comments
  // header"): a speech bubble, the version as a menu (every version with where its comments go and what's still to
  // do), search and hide; then To do · Done. Each version shows its own comments only. At the bottom, the comment box
  // ("Comment at 0:18 · goes into v8…"). Progress (recording, transcribing, an undo after a delete) floats over the
  // bottom of the list, so nothing in it moves. N or the › hides the column (remembered); the speech bubble over the
  // video, with the to-do count, brings it back. Extensions add tabs after To do · Done (spec/extensions.md, "UI
  // slots"): a sidebar tab shows its slot in place of the list; a review panel widens the column to half the page.
  import { Collapsible, ScrollArea, Tooltip } from 'bits-ui';
  import { tick } from 'svelte';
  import type { Review } from '../../lib/review.svelte';
  import SidebarItem from './SidebarItem.svelte';
  import VersionMenu from './VersionMenu.svelte';
  import CommentBox from './CommentBox.svelte';
  import ExtSlot from '../../ext/ExtSlot.svelte';
  import './sidebar.css';

  let { review }: { review: Review } = $props();

  const notes = $derived(review.notes);
  const view = $derived(review.sidebar);
  const job = $derived(notes.job);
  const tab = $derived(review.extTab);
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
  const dot = $derived(job?.error ? 'err' : job ? 'busy' : asking ? 'q' : working ? 'w' : '');
  /** A phone: the header and To do · Done share one row (the list needs the height), unless an extension adds
   * tabs. */
  const oneRow = $derived(review.layoutMode !== 'desktop' && !review.extTabs.length && !review.kindDrawer);
  const setOpen = (v: boolean) => {
    if (v !== view.panel) review.toggleSidebar();
  };

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
                <!-- A speech bubble and the to-do count (only the bubble at 0), as on Ignition's phone top bar; a status
                     shows as a dot on it. -->
                <Collapsible.Trigger
                  {...props}
                  data-part="sidebar-toggle"
                  class={['compact', view.panel && 'open'].filter(Boolean).join(' ')}
                  aria-label={`${view.panel ? 'Hide' : 'Show'} comments: ${pill}`}
                  ><svg viewBox="0 0 24 24" aria-hidden="true"
                    ><path
                      d="M6 5h12a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-7l-4.5 3.5V17H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Z"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.7"
                      stroke-linejoin="round"
                    /></svg
                  >{#if review.todoCount}<span class="n">{review.todoCount}</span>{/if}{#if dot}<span class="dot {dot}"
                    ></span>{/if}</Collapsible.Trigger
                >
              {/snippet}
            </Tooltip.Trigger>
            <Tooltip.Content class="sideTip" side="left" sideOffset={8}>
              {view.panel ? 'Hide comments' : 'Show comments'} <b>N</b>
            </Tooltip.Content>
          </Tooltip.Root>
        {/if}
        <Collapsible.Content
          id="side"
          data-part="sidebar"
          aria-label="Comments"
          class={[tab?.slot === 'review-panel' && 'wide', review.kindShown && 'kind', review.kindDrawer && 'drawer']
            .filter(Boolean)
            .join(' ') || undefined}
        >
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
              {#if oneRow}{@render tabs()}{/if}
              <span class="h"></span>
            {/if}
            <button
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
            </button>
            {#if review.kindDrawer}
              <Collapsible.Trigger class="icon hide" title="Hide comments" aria-label="Hide comments">
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
              <Collapsible.Trigger class="icon hide" title="Hide comments (N)" aria-label="Hide comments">
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
          {#if !oneRow}{@render tabs()}{/if}
          {#if tab}
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
                {/if}
              </div>
            </div>
          {/if}
          {#if review.canRecord && !tab}<CommentBox {review} />{/if}
        </Collapsible.Content>
      {/snippet}
    </Collapsible.Root>
  </Tooltip.Provider>
{/if}
