<script lang="ts">
  // The notes sidebar (milestone 2). On the newest version: the agent's open questions, and this session's notes as
  // they are transcribed. On an older version: its notes as they were resolved (✓ the change, ? the question).
  // Desktop: a column right of the video; N or its › hides it (remembered), and a quiet pill over the video brings it
  // back. Phone: a list under the controls behind the same pill. Nothing at all until there is something to show, so
  // the page looks exactly as before.
  import { Collapsible, ScrollArea, Tooltip } from 'bits-ui';
  import type { Review } from '../../lib/review.svelte';
  import { emptyText, heading, toggleLabel } from '../../lib/notes/view';
  import SidebarItem from './SidebarItem.svelte';
  import './sidebar.css';

  let { review }: { review: Review } = $props();

  const notes = $derived(review.notes);
  const view = $derived(review.sidebar);
  const res = $derived(notes.res);
  const job = $derived(notes.job);
  const pill = $derived(job && !view.rows.length ? job.text : toggleLabel(view.rows, !!res?.newest));
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
              {view.panel ? 'Hide notes' : 'Show notes'} <b>N</b>
            </Tooltip.Content>
          </Tooltip.Root>
        {/if}
        <Collapsible.Content id="side" aria-label="Notes">
          <header>
            <span class="h">{heading(res)}</span>
            {#if !view.phone}
              <Collapsible.Trigger class="hide" title="Hide notes (N)" aria-label="Hide notes">
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
                <p class="empty">{emptyText(res)}</p>
              {/if}
            </ScrollArea.Viewport>
            <ScrollArea.Scrollbar orientation="vertical" class="bar">
              <ScrollArea.Thumb class="thumb" />
            </ScrollArea.Scrollbar>
          </ScrollArea.Root>
        </Collapsible.Content>
      {/snippet}
    </Collapsible.Root>
  </Tooltip.Provider>
{/if}
