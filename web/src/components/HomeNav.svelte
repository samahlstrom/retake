<script lang="ts">
  // In a home (retake open <folder>, spec/home.md): "← Project › video" at the top left of the video, back to the
  // home at the place the reviewer came from, and after Done "Next video →" at the bottom right, where players put
  // their next episode. Nothing at all outside a home.
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();

  const next = (e: MouseEvent) => {
    e.preventDefault(); // goNext waits for Done's call before it leaves
    void review.goNext();
  };
</script>

{#if review.homeAvailable}
  <a
    id="allVideos"
    class="navpill back"
    href={review.homeBack.href}
    title={`Back to ${review.homeBack.title}`}
    aria-label={`Back to ${review.homeBack.title}`}
    data-part="review-back"
    ><svg viewBox="0 0 24 24" aria-hidden="true"
      ><path
        d="M14.5 6 8.5 12l6 6"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      /></svg
    ><span class="label"
      >{#each review.homeCrumb as part, i (i)}{#if i}<span class="sep" aria-hidden="true">{' › '}</span>{/if}<span
          class="part">{part}</span
        >{/each}</span
    ></a
  >
  {#if review.doneSaved && review.homeNext}
    <a
      id="nextVideo"
      class="navpill next"
      href={review.nextHref}
      title={`Next: ${review.homeNext.video || review.homeNext.title}`}
      onclick={next}
      >Next video <span aria-hidden="true">→</span>{#if !review.touch}<span class="k">&gt;</span>{/if}</a
    >
  {/if}
{/if}

<style>
  .navpill {
    position: absolute;
    z-index: 3;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 7px 14px;
    border-radius: 999px;
    background: rgba(9, 9, 10, 0.72);
    color: #dcdce1;
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
    color: #fff;
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
    flex: none;
    width: 16px;
    height: 16px;
  }
  .back .label {
    max-width: calc(100vw - 300px);
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sep {
    color: #85858d;
  }
  .next {
    right: 18px;
    bottom: 22px;
    font-weight: 600;
    color: #fff;
  }
  .k {
    color: #85858d;
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
  :global(html[data-phone]) .next {
    right: 10px;
    bottom: 10px;
    padding: 0 18px;
  }
</style>
