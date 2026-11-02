<script lang="ts">
  // The comments, beside the video (desktop) or under the controls (phone): the versions (a strip, with what's to do on
  // each), two tabs, To do and Done, and a search behind its icon. Each version shows its own comments only; the latest
  // says in one quiet line what's still open on earlier ones. Progress (recording, transcribing, an undo after a
  // delete) floats at the bottom of the column, so nothing in the list moves. N or the › hides it (remembered); a
  // quiet pill over the video brings it back. Extensions add tabs after To do · Done (spec/extensions.md, "UI slots"):
  // a sidebar tab shows its slot in place of the list; a review panel widens the column to half the page.
  import { Collapsible, ScrollArea, Tooltip } from 'bits-ui';
  import { tick } from 'svelte';
  import type { Review } from '../../lib/review.svelte';
  import SidebarItem from './SidebarItem.svelte';
  import VersionStrip from './VersionStrip.svelte';
  import EarlierOpen from './EarlierOpen.svelte';
  import ExtSlot from '../../ext/ExtSlot.svelte';
  import AgentStatus from '../AgentStatus.svelte';
  import { composeKey } from '../../lib/notes/typed';
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

  // On an extension's version (v0): a line to type a comment for the agent (spec/extensions.md, "Typing one").
  const mac = typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.platform);
  let line = $state<HTMLTextAreaElement | null>(null);
  /** The line grows with what's typed, up to a few lines. */
  function grow() {
    if (!line) return;
    line.style.height = 'auto';
    line.style.height = `${Math.min(line.scrollHeight, 160)}px`;
  }
  $effect(() => {
    void review.commentDraft;
    grow();
  });
  function lineKey(e: KeyboardEvent) {
    const k = composeKey(e);
    if (!k) return;
    e.preventDefault();
    void review.addTypedComment(k === 'send');
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
                <Collapsible.Trigger {...props} data-part="sidebar-toggle" class={view.panel ? 'open' : ''}
                  ><span class="dot {job?.error ? 'err' : job ? 'busy' : asking ? 'q' : working ? 'w' : ''}"
                  ></span>{pill}</Collapsible.Trigger
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
            {#if review.kindDrawer && !searching}
              <!-- A phone on an extension's version: the bar holds only Record and Send, so the agent is here (and the
                   versions are the strip under it). -->
              {#if review.canRecord && review.agent.available}<AgentStatus {review} />{/if}
              <span class="h"></span>
            {:else if searching}
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
          <VersionStrip {review} />
          <div class="tabs" data-part="tabs" role="tablist" aria-label="Comments">
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
            {#each review.extTabs as t (t.key)}
              <button
                role="tab"
                data-ext-tab={t.key}
                aria-selected={tab?.key === t.key}
                class:on={tab?.key === t.key}
                onclick={() => (review.sideTab = t.key)}>{t.title}</button
              >
            {/each}
          </div>
          {#if review.kindShown && review.canRecord && !tab}
            <form
              class="compose"
              data-part="comment-line"
              onsubmit={(e) => {
                e.preventDefault();
                void review.addTypedComment(false);
              }}
            >
              <textarea
                bind:this={line}
                bind:value={review.commentDraft}
                rows="1"
                placeholder="Comment for the agent…"
                aria-label="Comment for the agent"
                readonly={review.commentBusy}
                onkeydown={lineKey}></textarea>
              {#if !review.touch}
                <span class="keys" class:shown={!!review.commentDraft.trim()}
                  >Enter adds · {mac ? '⌘' : 'Ctrl'}+Enter sends</span
                >
              {/if}
            </form>
          {/if}
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
            <EarlierOpen {review} />
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
                            ? 'Type one above, or tap Record and talk it out.'
                            : 'Type one above, or press R and talk it out.'
                          : review.touch
                            ? 'Tap ● and talk to leave one at this moment.'
                            : 'Press R and talk to leave one at this moment.'}</span
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
                  <span class="pulse"></span>{review.seg.talk
                    ? 'Recording.'
                    : 'Recording. Comments appear here as you talk.'}
                </div>
              {/if}
            </div>
          {/if}
        </Collapsible.Content>
      {/snippet}
    </Collapsible.Root>
  </Tooltip.Provider>
{/if}
