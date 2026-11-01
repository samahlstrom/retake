<script lang="ts">
  // The comments, beside the video (desktop) or under the controls (phone): two tabs, To do and Done, and a search
  // behind its icon. Progress (recording, transcribing, an undo after a delete) floats at the bottom of the column, so
  // nothing in the list moves. N or the › hides it (remembered); a quiet pill over the video brings it back.
  import { Collapsible, ScrollArea, Tooltip } from 'bits-ui';
  import { tick } from 'svelte';
  import type { Review } from '../../lib/review.svelte';
  import SidebarItem from './SidebarItem.svelte';
  import './sidebar.css';

  let { review }: { review: Review } = $props();

  const notes = $derived(review.notes);
  const view = $derived(review.sidebar);
  const res = $derived(notes.res);
  const job = $derived(notes.job);
  const pill = $derived(
    job && !view.rows.length
      ? job.text
      : review.todoCount === 1
        ? '1 to do'
        : review.todoCount
          ? `${review.todoCount} to do`
          : `${review.rows.length} comments`,
  );
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

{#if notes.available}
  <Tooltip.Provider delayDuration={500}>
    <Collapsible.Root bind:open={() => view.panel, setOpen}>
      {#snippet child()}
        {#if view.toggle}
          <Tooltip.Root>
            <Tooltip.Trigger id="sideToggle">
              {#snippet child({ props })}
                <Collapsible.Trigger {...props} class={view.panel ? 'open' : ''}
                  ><span class="dot {job?.error ? 'err' : job ? 'busy' : res?.newest ? 'q' : ''}"
                  ></span>{pill}</Collapsible.Trigger
                >
              {/snippet}
            </Tooltip.Trigger>
            <Tooltip.Content class="sideTip" side="left" sideOffset={8}>
              {view.panel ? 'Hide comments' : 'Show comments'} <b>N</b>
            </Tooltip.Content>
          </Tooltip.Root>
        {/if}
        <Collapsible.Content id="side" aria-label="Comments">
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
              <span class="h">Comments</span>
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
            {#if !view.phone}
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
          <div class="tabs" role="tablist" aria-label="Comments">
            <button
              role="tab"
              aria-selected={review.commentFilter === 'todo'}
              class:on={review.commentFilter === 'todo'}
              onclick={() => (review.commentFilter = 'todo')}>To do <b>{review.todoCount}</b></button
            >
            <button
              role="tab"
              aria-selected={review.commentFilter === 'done'}
              class:on={review.commentFilter === 'done'}
              onclick={() => (review.commentFilter = 'done')}>Done <b>{review.doneCount}</b></button
            >
          </div>
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
                  {review.rows.length ? 'All done' : 'No comments yet'}<span
                    >{review.canRecord
                      ? review.touch
                        ? 'Tap ● and talk to leave one at this moment.'
                        : 'Press R and talk to leave one at this moment.'
                      : 'Comments will show up here.'}</span
                  >
                </p>
              {/if}
            </ScrollArea.Viewport>
            <ScrollArea.Scrollbar orientation="vertical" class="bar">
              <ScrollArea.Thumb class="thumb" />
            </ScrollArea.Scrollbar>
          </ScrollArea.Root>
          <div class="strip" aria-live="polite">
            {#if notes.deleted.length}
              <div class="deleted-comment" role="status">
                <span
                  >{notes.deleted.length === 1 ? 'Comment deleted' : `${notes.deleted.length} comments deleted`}</span
                ><button onclick={notes.undoDelete}>Undo</button>
              </div>
            {:else if notes.deleteError}
              <p class="delete-error" role="alert">{notes.deleteError}</p>
            {:else if job}
              <button class="job" class:err={job.error} title={job.error ? 'Dismiss' : ''} onclick={notes.dismissJob}>
                <span class="pulse"></span>
                <span class="jt"
                  >{job.text}{#if job.hint}<span class="hint">{job.hint}</span>{/if}</span
                >
              </button>
            {:else if review.seg}
              <div class="recording-comment">
                <span class="pulse"></span>Recording. Comments appear here as you talk.
              </div>
            {/if}
          </div>
        </Collapsible.Content>
      {/snippet}
    </Collapsible.Root>
  </Tooltip.Provider>
{/if}
