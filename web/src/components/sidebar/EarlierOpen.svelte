<script lang="ts">
  // On the latest version, one quiet line: what's still to do on older versions, newest first ("Open on earlier
  // versions: v6 · 2, v3 · 91"). Each part shows that version. Not there when nothing earlier is open.
  import type { Review } from '../../lib/review.svelte';

  let { review }: { review: Review } = $props();

  const earlier = $derived(review.earlierOpen);
</script>

{#if earlier.length}
  <p class="earlier" data-part="earlier-versions">
    Open on earlier versions:
    {#each earlier as e, i (e.version)}<button
        data-version={e.version}
        title="Show {e.version}"
        onclick={() => review.pickVersion(e.label)}>{e.version} · {e.todo}</button
      >{i < earlier.length - 1 ? ', ' : ''}{/each}
  </p>
{/if}

<style>
  @layer retake {
    .earlier {
      flex: none;
      margin: 0;
      padding: 9px 18px 10px;
      border-bottom: 1px solid var(--line);
      color: var(--muted);
      font-size: 12.5px;
      line-height: 1.5;
    }
    button {
      padding: 0;
      border: 0;
      background: none;
      color: var(--text-3);
      font: inherit;
      font-variant-numeric: tabular-nums;
      text-decoration: underline;
      text-decoration-color: color-mix(in srgb, var(--overlay) 22%, transparent);
      text-underline-offset: 3px;
    }
    button:hover {
      color: var(--text-strong);
      text-decoration-color: currentColor;
    }
    button:focus-visible {
      outline: 2px solid var(--focus);
      outline-offset: 2px;
    }
  }
</style>
