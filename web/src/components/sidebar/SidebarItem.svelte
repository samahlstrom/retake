<script lang="ts">
  // One sidebar row: its time and mark (✓ answered, ? asked, • open) and text, which jump to its moment when clicked;
  // then what it became (the change, or the question), version chips, and its actions.
  import type { Review } from '../../lib/review.svelte';
  import { MARKS, type Row } from '../../lib/notes/view';

  let { review, row, active }: { review: Review; row: Row; active: boolean } = $props();

  const answering = $derived(row.answer != null && review.seg?.answer_to === row.answer);
  const hasActs = $derived(!!row.answeredIn || row.answer != null || row.reopen);
</script>

<li class="item" class:active data-key={row.key} data-mark={row.mark}>
  <button class="jump" title="Go to {row.time}" onclick={() => review.jumpTo(row.t0)}>
    <span class="time">{row.time}</span>
    <span class="mark {row.mark}">{MARKS[row.mark]}</span>
    <span class="text">{row.text}</span>
  </button>
  <div class="more">
    {#if row.change}<p class="change">{row.change}</p>{/if}
    {#if row.question}<p class="asked">{row.question}</p>{/if}
    {#if row.images.length}
      <div class="thumbs">
        {#each row.images as im, i (i)}<img src={im.url} alt="" draggable="false" />{/each}
      </div>
    {/if}
    {#if row.options.length}
      <div class="chips">
        {#each row.options as c (c.label)}
          <button
            class="chip"
            class:on={c.on}
            disabled={!c.known}
            title={c.known ? `Show ${c.label} here` : `${c.label} isn't in this family`}
            onclick={() => review.peek(c.label)}>{c.label}</button
          >
        {/each}
      </div>
    {/if}
    {#if hasActs}
      <div class="acts">
        {#if row.answeredIn}<span class="meta">answered in {row.answeredIn}</span>{/if}
        {#if row.see}
          {@const see = row.see}
          <button class="act see" onclick={() => review.seeIn(see, row.t0)}>see it in {see}</button>
        {/if}
        {#if row.reopen}<button class="act reopen" onclick={() => review.reopen(row)}>Reopen</button>{/if}
        {#if row.answer != null}
          {@const thread = row.answer}
          <button
            class="answer"
            class:on={answering}
            title={answering ? 'Stop (R)' : 'Record your answer here'}
            onclick={() => review.answer(thread, row.t0)}
            ><span class="dot"></span>{answering ? 'Answering' : 'Answer'}</button
          >
        {/if}
      </div>
    {/if}
  </div>
</li>
