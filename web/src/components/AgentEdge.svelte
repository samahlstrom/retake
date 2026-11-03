<script lang="ts">
  // The red edge light (spec/automation.md, "While the agent works"): while the agent works on this video's comments,
  // a slow segment of light in the record red travels around the edge of the video's frame, so it can't be taken for
  // recording's steady red. It's the live version's mark. With reduced motion it is a faint, still hairline. It takes
  // no pointer and draws over the frame only, never the letterbox.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();

  const w = $derived(Math.max(1, review.frame.width));
  const h = $derived(Math.max(1, review.frame.height));
  /** Around the frame, clockwise from the top left, 1.5 px in. Its length is 100 (pathLength), so the light is a
   * share of the edge however big the frame is. */
  const d = $derived(`M1.5 1.5H${w - 1.5}V${h - 1.5}H1.5Z`);
</script>

{#if review.edgeLit && review.frame.width > 0}
  <svg
    id="agentEdge"
    data-part="agent-edge"
    aria-hidden="true"
    style:left="{review.frame.left}px"
    style:top="{review.frame.top}px"
    style:width="{w}px"
    style:height="{h}px"
    viewBox="0 0 {w} {h}"
  >
    <path class="glow" {d} pathLength="100" />
    <path class="light" {d} pathLength="100" />
  </svg>
{/if}

<style>
  @layer retake {
    #agentEdge {
      position: absolute;
      pointer-events: none;
      overflow: visible;
    }
    path {
      fill: none;
      stroke: var(--agent-edge);
      stroke-linecap: round;
      stroke-linejoin: round;
    }
    /* A soft tail, and the light at its head: both travel the whole edge in 5 s. */
    .glow {
      stroke-width: 6;
      stroke-opacity: 0.28;
      stroke-dasharray: 22 78;
      animation: edge-tail 5s linear infinite;
    }
    .light {
      stroke-width: 2;
      stroke-dasharray: 9 91;
      animation: edge-head 5s linear infinite;
    }
    @keyframes edge-tail {
      from {
        stroke-dashoffset: 0;
      }
      to {
        stroke-dashoffset: -100;
      }
    }
    @keyframes edge-head {
      from {
        stroke-dashoffset: -13;
      }
      to {
        stroke-dashoffset: -113;
      }
    }
    @media (prefers-reduced-motion: reduce) {
      .glow {
        display: none;
      }
      .light {
        stroke-width: 1;
        stroke-opacity: 0.4;
        stroke-dasharray: none;
        animation: none;
      }
    }
  }
</style>
