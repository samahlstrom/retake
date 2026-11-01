<script lang="ts">
  // The agent's visuals over the video (spec/automation.md, "What the page shows"): each question's boxes, arrows,
  // lines and placed images while the playhead is in its range, or while it is selected. Over the frame and under the
  // pen, and it takes no pointer, so the reviewer can draw over them while recording an answer. Entering the range
  // highlights them once.
  import type { Review } from '../lib/review.svelte';
  import AgentDrawing from './AgentDrawing.svelte';

  let { review }: { review: Review } = $props();

  const w = $derived(Math.max(1, review.frame.width));
  const h = $derived(Math.max(1, review.frame.height));
</script>

{#if review.shownAsks.length}
  <svg
    id="agentLayer"
    data-part="agent-visuals"
    aria-hidden="true"
    style:left="{review.frame.left}px"
    style:top="{review.frame.top}px"
    style:width="{w}px"
    style:height="{h}px"
    viewBox="0 0 {w} {h}"
  >
    {#each review.shownAsks as a (a.row.ask!.id)}
      {#key a.pulse}
        <g class="ask" class:selected={review.selectedAsk === a.row.ask!.id} data-ask={a.row.ask!.id}>
          <AgentDrawing visuals={a.row.ask!.visuals} width={w} height={h} chip={Math.max(11, Math.round(w / 90))} />
        </g>
      {/key}
    {/each}
  </svg>
{/if}

<style>
  @layer retake {
    #agentLayer {
      position: absolute;
      pointer-events: none;
      overflow: visible;
    }
    .ask {
      animation: agent-arrive 900ms ease-out;
      filter: drop-shadow(0 1px 2px color-mix(in srgb, var(--shadow) 55%, transparent));
    }
    @keyframes agent-arrive {
      0% {
        opacity: 0;
      }
      25% {
        opacity: 1;
        filter: drop-shadow(0 0 8px color-mix(in srgb, var(--agent) 95%, transparent));
      }
      100% {
        filter: drop-shadow(0 1px 2px color-mix(in srgb, var(--shadow) 55%, transparent));
      }
    }
    @media (prefers-reduced-motion: reduce) {
      .ask {
        animation: none;
      }
    }
  }
</style>
