<script lang="ts">
  // The version label; click it for the compact list (newest first, the base marked ✓). Hidden for a single version.
  // C is handled globally; focus never moves into the list. ContentStatic: Bits UI's popover behavior
  // (outside click and Escape close it) with the legacy CSS placement, 12px above the button, centered.
  // A streamed version that isn't complete (spec/bridge.md, "Streaming") has a chip in the list ("pending",
  // "rendering 12/50"), and the button has a dot while it's on screen (drawn over the border, so nothing moves).
  // The live version (spec/automation.md, "The live version") is one entry, `v5`, that plays its newest revision (the
  // agent's drafts are never called that), with a small red live dot while the agent works on it. An extension's kind
  // of version comes first, named `v0 · Script` (spec/extensions.md, "Version kinds").
  import { Popover } from 'bits-ui';
  import DownloadItem from './DownloadItem.svelte';
  import type { Review } from '../lib/review.svelte';
  import { byLabel, incomplete, pickerEntries, streamChip, verName } from '../lib/versions';
  import { fmt } from '../lib/util';

  let { review }: { review: Review } = $props();

  const title = $derived(
    `Review ${review.nameOf(review.shown)}. Choose a version to see its feedback and completed changes.`,
  );
  const entries = $derived(pickerEntries(review.cuts));
  const newestFirst = $derived([...review.versions.filter((v) => v.kind), ...[...entries].reverse()]);
  const latest = $derived(entries.at(-1)?.label);
  const shownStreaming = $derived(incomplete(byLabel(review.versions, review.shown)));
  const live = $derived(review.liveTarget);
  const shownLive = $derived(!!live && verName(review.shown) === live);
  const keepFocus = (e: Event) => e.preventDefault();
  // The last entry downloads the version on screen (spec/api.md, "Download"); with one version the menu is just that.
  const shownVersion = $derived(byLabel(review.versions, review.shown));
  const download = $derived(review.mayDownload && !!shownVersion && !shownVersion.kind);
</script>

<div id="ver" hidden={review.versions.length < 2 && !download}>
  <Popover.Root bind:open={() => review.verOpen, (v) => (review.verOpen = v)}>
    <Popover.Trigger
      id="verBtn"
      data-part="version-picker"
      class={[review.flipped && 'flipped', shownStreaming && !shownLive && 'rendering', shownLive && 'live']
        .filter(Boolean)
        .join(' ')}
      {title}>{review.nameOf(review.shown)}<span class="version-chevron">⌄</span></Popover.Trigger
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
        {@const isLive = !v.kind && !!live && verName(v.label) === live}
        {@const on = verName(v.label) === verName(review.base)}
        <button
          data-ver={v.label}
          data-version={v.kind ? v.label : verName(v.label)}
          class={[on && 'on', isLive && 'is-live'].filter(Boolean).join(' ')}
          onclick={() => review.pickVersion(v.label)}
          ><span
            >{review.nameOf(v.label)}{on ? ' ✓' : ''}{v.label === latest ? ' · Latest' : ''}{#if isLive}<span
                class="live"
                title="The agent is working on it: it updates as each comment's edit goes in"
                aria-label="live"
              ></span>{:else if chip}<span class="stream">{chip}</span>{/if}</span
          ><span class="d">{v.kind ? '' : fmt(v.duration || 0, false)}</span></button
        >
      {/each}
      {#if download}<DownloadItem {review} />{/if}
    </Popover.ContentStatic>
  </Popover.Root>
</div>
