<script lang="ts">
  // The version label; click it for the compact list (newest first, the base marked ✓). Hidden for a single version.
  // C is handled globally; focus never moves into the list. ContentStatic: Bits UI's popover behavior
  // (outside click and Escape close it) with the legacy CSS placement, 12px above the button, centered.
  // A streamed version that isn't complete (spec/bridge.md, "Streaming") has a chip in the list ("pending",
  // "rendering 12/50"), and the button has a dot while it's on screen (drawn over the border, so nothing moves).
  // The agent's drafts (`v5-draft2`, contract C4) read "v5" with a quiet "draft" tag: they aren't versions yet. An
  // extension's kind of version comes first, named `v0 · Script` (spec/extensions.md, "Version kinds").
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { byLabel, draftOf, incomplete, latestVersion, streamChip } from '../lib/versions';
  import { fmt } from '../lib/util';

  let { review }: { review: Review } = $props();

  const title = $derived(`Review ${review.shown}. Choose a version to see its feedback and completed changes.`);
  const newestFirst = $derived([...review.versions.filter((v) => v.kind), ...[...review.cuts].reverse()]);
  const latest = $derived(latestVersion(review.versions)?.label);
  const shownStreaming = $derived(incomplete(byLabel(review.versions, review.shown)));
  const shownDraft = $derived(draftOf(review.shown));
  const keepFocus = (e: Event) => e.preventDefault();
</script>

<div id="ver" hidden={review.versions.length < 2}>
  <Popover.Root bind:open={() => review.verOpen, (v) => (review.verOpen = v)}>
    <Popover.Trigger
      id="verBtn"
      data-part="version-picker"
      class={[review.flipped && 'flipped', shownStreaming && 'rendering'].filter(Boolean).join(' ')}
      {title}
      >{shownDraft ?? review.nameOf(review.shown)}{#if shownDraft}<span class="draft">draft</span>{/if}<span
        class="version-chevron">⌄</span
      ></Popover.Trigger
    >
    <Popover.ContentStatic
      id="verList"
      data-part="version-list"
      trapFocus={false}
      onOpenAutoFocus={keepFocus}
      onCloseAutoFocus={keepFocus}
    >
      {#each newestFirst as v (v.label)}
        {@const chip = streamChip(v)}
        {@const draft = draftOf(v.label)}
        <button
          data-ver={v.label}
          class={[v.label === review.base && 'on', draft && 'is-draft'].filter(Boolean).join(' ')}
          onclick={() => review.pickVersion(v.label)}
          ><span
            >{draft ?? review.nameOf(v.label)}{v.label === review.base ? ' ✓' : ''}{v.label === latest
              ? ' · Latest'
              : ''}{#if draft}<span class="draft">draft</span>{/if}{#if chip}<span class="stream">{chip}</span
              >{/if}</span
          ><span class="d">{v.kind ? '' : fmt(v.duration || 0, false)}</span></button
        >
      {/each}
    </Popover.ContentStatic>
  </Popover.Root>
</div>
