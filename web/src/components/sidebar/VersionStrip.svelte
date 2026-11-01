<script lang="ts">
  // The versions over the comments, oldest first: the one on screen highlighted, each with what's still to do on it
  // ("v3 91"), the agent's drafts of the next one as one quiet "v8 · drafting". A click shows that version, like the
  // picker in the bar. Not there for a single version. One row of a fixed height: it scrolls sideways (a phone, or many
  // versions), never wraps, so nothing under it moves.
  import type { Review } from '../../lib/review.svelte';

  let { review }: { review: Review } = $props();

  const strip = $derived(review.versionStrip);
  let el = $state<HTMLElement | null>(null);

  // Keep the version on screen in view (the newest is at the far end of a long strip).
  $effect(() => {
    const on = strip.find((e) => e.on)?.version;
    const b = on ? el?.querySelector<HTMLElement>(`[data-version="${on}"]`) : null;
    if (!el || !b) return;
    const left = b.offsetLeft - el.offsetLeft,
      right = left + b.offsetWidth;
    if (left < el.scrollLeft + 12) el.scrollLeft = left - 12;
    else if (right > el.scrollLeft + el.clientWidth - 12) el.scrollLeft = right - el.clientWidth + 12;
  });
</script>

{#if strip.length}
  <nav class="versions" aria-label="Versions" data-part="version-strip" bind:this={el}>
    {#each strip as e (e.version)}
      <button
        data-version={e.version}
        class:on={e.on}
        class:drafting={e.drafting}
        aria-current={e.on ? 'true' : undefined}
        title={e.drafting ? `${e.version}: the agent's draft` : e.todo ? `${e.version}: ${e.todo} to do` : e.version}
        onclick={() => review.pickVersion(e.label)}
        >{e.version}{#if e.drafting}<span class="dr"> · drafting</span>{/if}{#if e.todo}<span class="n">{e.todo}</span
          >{/if}</button
      >
    {/each}
  </nav>
{/if}

<style>
  @layer retake {
    .versions {
      flex: none;
      display: flex;
      align-items: center;
      gap: 2px;
      height: 36px;
      padding: 0 12px;
      overflow-x: auto;
      overflow-y: hidden;
      scrollbar-width: none;
      overscroll-behavior-x: contain;
    }
    .versions::-webkit-scrollbar {
      display: none;
    }
    button {
      flex: none;
      display: inline-flex;
      align-items: baseline;
      height: 26px;
      padding: 0 7px;
      border: 0;
      border-radius: var(--radius-sm);
      background: none;
      color: var(--muted);
      font: inherit;
      font-size: 12.5px;
      font-weight: 500;
      line-height: 26px;
      white-space: nowrap;
    }
    button:hover {
      color: var(--text-3);
    }
    button.on {
      background: color-mix(in srgb, var(--overlay) 8%, transparent);
      color: var(--text);
    }
    .n {
      margin-left: 5px;
      color: var(--faint);
      font-size: 11.5px;
      font-variant-numeric: tabular-nums;
    }
    button.on .n {
      color: var(--text-6);
    }
    .dr {
      color: var(--faint);
      font-weight: 400;
    }
    button:focus-visible {
      outline: 2px solid var(--focus);
      outline-offset: -2px;
    }
    :global(html[data-phone]) button {
      height: 32px;
      padding: 0 10px;
      line-height: 32px;
    }
    :global(html[data-phone]) .versions {
      height: 44px;
    }
  }
</style>
