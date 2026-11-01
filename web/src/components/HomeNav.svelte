<script lang="ts">
  // In a home (retake open <folder>, spec/home.md): "← <project>" at the top left of the video (with more than one
  // project, "← Projects" and the project), and after Done "Next video →" at the bottom right, where players put their
  // next episode. Nothing at all outside a home.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();

  const next = (e: MouseEvent) => {
    e.preventDefault(); // goNext waits for Done's call before it leaves
    void review.goNext();
  };
</script>

{#if review.homeAvailable}
  {@const back = review.homeSingle && review.homeProject ? review.homeProject.title : 'Projects'}
  <a
    id="allVideos"
    data-part="home-nav"
    class="navpill back"
    href={review.homeHref}
    aria-label={review.homeSingle && review.homeProject ? `Back to ${back}` : back}
    title={review.homeSingle ? 'All videos' : 'Projects'}
    ><svg viewBox="0 0 24 24" aria-hidden="true"
      ><path
        d="M14.5 6 8.5 12l6 6"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      /></svg
    ><span class="label">{back}</span></a
  >
  {#if review.homeProject && !review.homeSingle}
    <a
      class="navpill containing"
      data-part="home-nav"
      href={review.projectHref}
      aria-label={`Back to ${review.homeProject.title}`}>{review.homeProject.title}</a
    >
  {/if}
  {#if review.doneSaved && review.homeNext}
    <a
      id="nextVideo"
      data-part="home-nav"
      class="navpill next"
      href={review.nextHref}
      title={`Next: ${review.homeNext.title}`}
      onclick={next}
      >Next video <span aria-hidden="true">→</span>{#if !review.touch}<span class="k">&gt;</span>{/if}</a
    >
  {/if}
{/if}

<style>
  @layer retake {
    .navpill {
      position: absolute;
      z-index: 3;
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 7px 14px;
      border-radius: var(--radius-pill);
      background: color-mix(in srgb, var(--scrim) 72%, transparent);
      color: var(--on-scrim);
      font-size: 13px;
      letter-spacing: 0.01em;
      text-decoration: none;
      white-space: nowrap;
      backdrop-filter: blur(10px);
      transition:
        opacity 0.2s ease,
        color 0.2s ease;
    }
    .navpill:hover,
    .navpill:focus-visible {
      color: var(--on-scrim-strong);
    }
    .back {
      top: 14px;
      left: 14px;
      padding-left: 9px;
      opacity: 0.6;
    }
    .back:hover,
    .back:focus-visible {
      opacity: 1;
    }
    .back svg {
      width: 16px;
      height: 16px;
    }
    .containing {
      top: 14px;
      left: 120px;
      max-width: calc(100vw - 270px);
      overflow: hidden;
      text-overflow: ellipsis;
      display: block;
    }
    .next {
      right: 18px;
      bottom: 22px;
      font-weight: 600;
      color: var(--on-scrim-strong);
    }
    .k {
      color: var(--dim);
      font-size: 12px;
      font-weight: 400;
      margin-left: 4px;
    }
    /* The phone layout: thumb-sized, and the back control is only its arrow (the toast shares the top edge). */
    :global(html[data-phone]) .navpill {
      min-height: 44px;
      min-width: 44px;
      justify-content: center;
    }
    :global(html[data-phone]) .back {
      top: 8px;
      left: 8px;
      padding: 0;
      opacity: 0.8;
    }
    :global(html[data-phone]) .back .label {
      display: none;
    }
    :global(html[data-phone]) .back svg {
      width: 22px;
      height: 22px;
    }
    :global(html[data-phone]) .containing {
      left: 58px;
      top: 8px;
      max-width: calc(100vw - 180px);
      line-height: 30px;
    }
    :global(html[data-phone]) .next {
      right: 10px;
      bottom: 10px;
      padding: 0 18px;
    }
  }
</style>
