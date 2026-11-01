<script lang="ts">
  // The notes sidebar (milestone 2). On the newest version: the agent's open questions, and this session's notes as
  // they are transcribed. On an older version: its notes as they were resolved (✓ the change, ? the question).
  // Desktop: a column right of the video; N or its › hides it (remembered), and a quiet pill over the video brings it
  // back. Phone: a list under the controls behind the same pill. Nothing at all until there is something to show, so
  // the page looks exactly as before.
  import { Collapsible, ScrollArea, Tooltip } from 'bits-ui';
  import type { Review } from '../../lib/review.svelte';
  import SidebarItem from './SidebarItem.svelte';
  import StyledSelect from '../ui/Select.svelte';
  import './sidebar.css';

  let { review }: { review: Review } = $props();

  const notes = $derived(review.notes);
  const view = $derived(review.sidebar);
  const res = $derived(notes.res);
  const job = $derived(notes.job);
  const pill = $derived(job && !view.rows.length ? job.text : `${review.rows.length} comments`);
  const setOpen = (v: boolean) => {
    if (v !== view.panel) review.toggleSidebar();
  };
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
            <span class="h">Comments <span class="count">{review.shown}</span></span>
            {#if !view.phone}
              <Collapsible.Trigger class="hide" title="Hide comments (N)" aria-label="Hide comments">
                <svg viewBox="0 0 24 24"
                  ><path
                    d="M9.5 6l6 6-6 6"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  /></svg
                >
              </Collapsible.Trigger>
            {/if}
          </header>
          <div class="review-status" aria-label="Review progress">
            <button
              class:on={review.commentScope === 'current' && review.commentFilter === 'open'}
              onclick={() => {
                review.commentScope = 'current';
                review.commentFilter = 'open';
              }}>Open <b>{review.currentOpenCount}</b></button
            >
            <button
              class:on={review.commentScope === 'current' && review.commentFilter === 'resolved'}
              onclick={() => {
                review.commentScope = 'current';
                review.commentFilter = 'resolved';
              }}>Resolved <b>{review.resolvedCount}</b></button
            >
            <button
              class:on={review.commentScope === 'earlier'}
              onclick={() => {
                review.commentScope = 'earlier';
                review.commentFilter = 'open';
              }}>Earlier <b>{review.earlierCount}</b></button
            >
          </div>
          {#if review.commentScope === 'earlier'}<p class="scope-help">
              Unresolved feedback from earlier versions. Original drawings are included.
            </p>{/if}
          <div class="comment-tools">
            <label class="comment-search"
              ><span aria-hidden="true">⌕</span><input
                type="search"
                placeholder="Search comments"
                aria-label="Search comments"
                bind:value={review.commentSearch}
              /></label
            >
            <div class="comment-filters">
              <StyledSelect
                label="Filter comments"
                compact
                value={review.commentFilter}
                options={[
                  { value: 'all', label: 'All comments' },
                  { value: 'open', label: 'Open' },
                  { value: 'resolved', label: 'Resolved' },
                ]}
                onValueChange={(value) => {
                  review.commentFilter = value as typeof review.commentFilter;
                }}
              />
              <StyledSelect
                label="Comment version"
                compact
                value={review.commentScope}
                options={[
                  { value: 'current', label: `${review.shown} review` },
                  { value: 'earlier', label: 'Earlier unresolved' },
                  { value: 'all', label: 'All relevant feedback' },
                ]}
                onValueChange={(value) => {
                  review.commentScope = value as typeof review.commentScope;
                }}
              />
            </div>
          </div>
          {#if review.seg}<div class="recording-comment">
              <span class="pulse"></span>Recording feedback…<small
                >Comments arrive as short clips finish transcribing.</small
              >
            </div>{/if}
          {#if notes.deleted.length}
            <div class="deleted-comment" role="status">
              <span>{notes.deleted.length === 1 ? 'Comment deleted' : `${notes.deleted.length} comments deleted`}</span
              ><button disabled={notes.deleting} onclick={notes.undoDelete}>Undo</button>
            </div>
          {/if}
          {#if notes.deleteError}<p class="delete-error" role="alert">{notes.deleteError}</p>{/if}
          {#if job}
            <button
              class="job"
              class:err={job.error}
              disabled={!job.error}
              title={job.error ? 'Dismiss' : ''}
              onclick={notes.dismissJob}
            >
              <span class="pulse"></span>
              <span class="jt"
                >{job.text}{#if job.hint}<span class="hint">{job.hint}</span>{/if}</span
              >
            </button>
          {/if}
          <ScrollArea.Root class="scroll" type="hover">
            <ScrollArea.Viewport class="vp">
              {#if view.rows.length}
                <ol>
                  {#each view.rows as row (row.key)}
                    <SidebarItem {review} {row} active={row.key === view.active} />
                  {/each}
                </ol>
              {:else if !job}
                <p class="empty">
                  {review.rows.length ? 'No matching comments' : 'Start the conversation'}<span
                    >{review.rows.length
                      ? 'Try another search or filter.'
                      : review.canRecord
                        ? 'Press R to leave a voice comment at this moment.'
                        : 'Comments will appear here.'}</span
                  >
                </p>
              {/if}
            </ScrollArea.Viewport>
            <ScrollArea.Scrollbar orientation="vertical" class="bar">
              <ScrollArea.Thumb class="thumb" />
            </ScrollArea.Scrollbar>
          </ScrollArea.Root>
          <footer class="comment-footer">Saved feedback stays open until the editor resolves it.</footer>
        </Collapsible.Content>
      {/snippet}
    </Collapsible.Root>
  </Tooltip.Provider>
{/if}
