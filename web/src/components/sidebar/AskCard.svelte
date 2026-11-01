<script lang="ts">
  // The agent's question under its comment (spec/automation.md, "What the page shows"): a thumbnail of that frame with
  // the agent's drawing on it (click it to go there and keep the drawing on the video), the question, the images it
  // made, its choices to tap, and the reply box (type, or answer out loud and draw).
  import type { Review } from '../../lib/review.svelte';
  import type { Row } from '../../lib/notes/view';
  import { drawsOnVideo, loose } from '../../lib/notes/agent';
  import AgentDrawing from '../AgentDrawing.svelte';
  import ReplyBox from '../ReplyBox.svelte';

  let { review, row }: { review: Review; row: Row } = $props();

  const ask = $derived(row.ask!);
  const v = $derived(ask.visuals ?? null);
  const W = 240;
  const H = $derived.by(() => {
    const [w, h] = review.aspect.split('/').map(Number);
    return Math.round(W / (w && h && w / h > 0.2 && w / h < 5 ? w / h : 16 / 9));
  });
  const shot = $derived(!!v?.frame || drawsOnVideo(v));
  const images = $derived(loose(v));
  const options = $derived(v?.options ?? []);
  const recording = $derived(review.seg?.answer_to === ask.id);
  let sending = $state(false);

  async function choose(text: string) {
    if (sending) return;
    sending = true;
    try {
      await review.answerAsk(row, text);
    } finally {
      sending = false;
    }
  }
</script>

<div class="agent-ask" data-part="question-card" data-ask={ask.id}>
  {#if shot}
    <button
      class="ask-shot"
      class:on={review.selectedAsk === ask.id}
      title="Go to {ask.time} and show the agent's drawing"
      aria-label="The agent's drawing at {ask.time}: show it on the video"
      onclick={() => review.selectAsk(row)}
    >
      <svg viewBox="0 0 {W} {H}" aria-hidden="true">
        {#if v?.frame}<image
            href={v.frame}
            x="0"
            y="0"
            width={W}
            height={H}
            preserveAspectRatio="xMidYMid slice"
          />{/if}
        <AgentDrawing visuals={v} width={W} height={H} chip={9} />
      </svg>
    </button>
  {/if}
  <p class="ask-text">
    <span class="agent-chip">Agent</span>
    <button class="ask-time" title="Go to {ask.time}" onclick={() => review.selectAsk(row)}>{ask.time}</button>
    <span>{ask.text}</span>
  </p>
  {#if images.length}
    <div class="ask-images">
      {#each images as im, i (i)}<a href={im.url} target="_blank" rel="noopener" class="agent-card"
          ><img src={im.url} alt="The agent's image {i + 1}" /></a
        >{/each}
    </div>
  {/if}
  {#if options.length && review.canRecord}
    <div class="choices" role="group" aria-label="Answer with a choice">
      {#each options as o, i (i)}
        <button
          class="choice"
          class:pic={!!o.image}
          disabled={sending || review.automation.busy}
          onclick={() => choose(o.text)}
        >
          {#if o.image}<span class="agent-card"><img src={o.image.url} alt="" /></span>{/if}
          <span class="choice-text">{o.text}</span>
        </button>
      {/each}
    </div>
  {:else if options.length}
    <ul class="choices-read">
      {#each options as o, i (i)}<li>{o.text}</li>{/each}
    </ul>
  {/if}
  {#if review.canRecord}
    <ReplyBox
      onsend={(text) => review.answerAsk(row, text)}
      onmic={() => review.answer(ask.id, ask.t)}
      {recording}
      busy={review.automation.busy}
    />
  {/if}
</div>

<style>
  @layer retake {
    .agent-ask {
      margin-top: 10px;
      padding: 10px;
      border: 1px dashed color-mix(in srgb, var(--agent) 55%, transparent);
      border-radius: var(--radius-md);
      background: color-mix(in srgb, var(--agent) 6%, transparent);
    }
    .ask-shot {
      display: block;
      width: 100%;
      margin: 0 0 8px;
      padding: 0;
      border: 1px solid color-mix(in srgb, var(--agent) 35%, transparent);
      border-radius: 7px;
      overflow: hidden;
      background: var(--surface-control);
      cursor: pointer;
    }
    .ask-shot.on,
    .ask-shot:hover {
      border-color: var(--agent);
    }
    .ask-shot svg {
      display: block;
      width: 100%;
      height: auto;
    }
    .ask-text {
      margin: 0;
      color: var(--text);
      font-size: 13px;
      line-height: 1.45;
    }
    .agent-chip {
      display: inline-block;
      margin-right: 6px;
      padding: 1px 7px;
      border-radius: 999px;
      background: var(--agent);
      color: var(--agent-ink);
      font-size: 10.5px;
      font-weight: 700;
      letter-spacing: 0.02em;
      vertical-align: 1px;
    }
    .ask-time {
      margin-right: 6px;
      padding: 0;
      border: 0;
      background: none;
      color: var(--agent);
      font: inherit;
      font-variant-numeric: tabular-nums;
      cursor: pointer;
    }
    .ask-images {
      display: flex;
      gap: 8px;
      margin-top: 8px;
      flex-wrap: wrap;
    }
    .agent-card {
      display: block;
      border: 1.5px solid var(--agent);
      border-radius: 7px;
      overflow: hidden;
      background: var(--surface-sunken);
    }
    .ask-images .agent-card {
      width: calc(50% - 4px);
    }
    .agent-card img {
      display: block;
      width: 100%;
      height: auto;
      max-height: 120px;
      object-fit: contain;
    }
    .choices {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-top: 10px;
    }
    .choice {
      display: flex;
      flex-direction: column;
      align-items: stretch;
      gap: 6px;
      flex: 1 1 calc(50% - 4px);
      min-width: 0;
      padding: 6px;
      border: 1px solid color-mix(in srgb, var(--agent) 45%, transparent);
      border-radius: 9px;
      background: color-mix(in srgb, var(--overlay) 3%, transparent);
      color: var(--text);
      font: inherit;
      font-size: 13px;
      cursor: pointer;
    }
    .choice:not(.pic) {
      flex: 0 1 auto;
      padding: 7px 12px;
    }
    .choice:hover:not(:disabled),
    .choice:focus-visible {
      border-color: var(--agent);
      background: color-mix(in srgb, var(--agent) 14%, transparent);
    }
    .choice:disabled {
      opacity: 0.6;
      cursor: default;
    }
    .choice-text {
      text-align: center;
    }
    .choices-read {
      margin: 8px 0 0;
      padding-left: 18px;
      color: var(--dim);
      font-size: 12.5px;
    }
    :global(html[data-phone]) .choice {
      min-height: 44px;
    }
  }
</style>
