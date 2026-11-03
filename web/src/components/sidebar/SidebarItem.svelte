<script lang="ts">
  // One comment's content (the list draws its card, the <li>, and moves it: Sidebar.svelte): when, its words, what
  // you drew, one status (Open, Working, Question, Done) and, until it's done,
  // the version its edit goes into ("→ v8": a comment on v7 becomes an edit in v8). A question from the
  // agent has the reply box under it; a paused one says why once, with Retry; a Working one waiting for its computer
  // says so. The one the agent is working on has a quiet line in the agent's words under its own; a done one says what
  // changed and "in v5", which plays v5 at that moment (spec/automation.md, "While the agent works"). Everything else
  // is in its ⋯.
  import type { Review } from '../../lib/review.svelte';
  import { anchorText, sketchFrame, type Row } from '../../lib/notes/view';
  import { STATE_TEXT } from '../../lib/status';
  import { verName } from '../../lib/versions';
  import CommentMenu from './CommentMenu.svelte';
  import ReplyBox from '../ReplyBox.svelte';
  import Sketch from './Sketch.svelte';
  import AskCard from './AskCard.svelte';

  let { review, row }: { review: Review; row: Row } = $props();

  const status = $derived(review.statusOf(row));
  const task = $derived(review.taskOf(row));
  const strokes = $derived(row.drawings.length || !row.text.trim() ? review.strokesOf(row) : []);
  const frame = $derived(sketchFrame(row, review.versions, review.shown, !!review.homeCtx));
  const aspect = $derived.by(() => {
    const [w, h] = review.aspect.split('/').map(Number);
    return w && h ? w / h : 16 / 9;
  });
  /** Agent questions asked through the notes take a recorded answer; a worker's question takes typed words. */
  const voiceThread = $derived(row.answer);
  const workerAsks = $derived(task?.state === 'question' && !row.ask && review.canRecord ? task : null);
  // A question from inside the task (row.ask) has its own card, with its reply box.
  const asked = $derived(row.kind === 'question' || row.ask ? null : (workerAsks?.question ?? row.question));
  const answering = $derived(voiceThread != null && review.seg?.answer_to === voiceThread);
  const text = $derived(
    row.text.trim() || (row.drawings.length || row.images.length || strokes.length ? 'Drawing' : 'No words'),
  );
  /** What changed: the agent's words when it marked it done, else the report's. */
  const change = $derived(status.change ?? row.change);
  /** The version a done comment's edit is in: the live target it was marked into, else the one that answered it. */
  const doneIn = $derived(
    status.state !== 'done' || row.kind !== 'note'
      ? null
      : status.change != null
        ? (task?.target ?? row.into)
        : row.answeredIn
          ? verName(row.answeredIn)
          : null,
  );
  /** The agent's line: its words, trailed off ("slowing the mask fade…"). */
  const doing = $derived(status.doing ? (/[.…!?]$/.test(status.doing) ? status.doing : `${status.doing}…`) : '');

  async function answer(message: string): Promise<boolean> {
    if (!workerAsks) return false;
    const ok = await review.automation.send(`/tasks/${workerAsks.id}/answer`, { message });
    if (ok) await review.notes.refresh();
    else review.toast.say(review.automation.error || 'Could not send your answer. Try again.', 5000);
    return ok;
  }
  async function retry() {
    if (!task) return;
    // The owner may pick the agent; anyone else's retry keeps the task's own (the server ignores theirs).
    const body = review.owner ? { agent: task.agent || review.agent.worker?.agents[0]?.id || '' } : {};
    if (await review.automation.send(`/tasks/${task.id}/retry`, body)) await review.notes.refresh();
    else review.toast.say(review.automation.error || 'Could not retry. Try again.', 5000);
  }
</script>

<div class="line1">
  {#if review.manyAuthors && row.kind === 'note'}<span class="who"
      ><span class="avatar" aria-hidden="true">{row.by.slice(0, 1).toUpperCase()}</span>{row.by}</span
    >{/if}
  {#if row.typed}
    <!-- A typed comment on an extension's version has no moment: where in the document it is, if anywhere. -->
    {#if row.anchor && review.kindShown}<button
        class="time anchor"
        title="Show it in {row.anchor.file}"
        onclick={() => review.reveal(row.anchor)}>{anchorText(row.anchor)}</button
      >{:else if row.anchor}<span class="time anchor" title={row.anchor.file}>{anchorText(row.anchor)}</span>{/if}
  {:else}
    <button class="time jump" title="Go to {row.time}" onclick={() => review.jumpToComment(row)}>{row.time}</button>
  {/if}
  <span class="status {status.state}" data-part="status"
    ><span class="dot" aria-hidden="true"></span>{STATE_TEXT[status.state]}</span
  >
  {#if doneIn}<button
      class="into in"
      data-part="into"
      title="See it in {doneIn} at {row.time}"
      onclick={() => review.showIn(row, doneIn)}>in {doneIn}</button
    >{:else if row.into && row.kind === 'note' && status.state !== 'done'}<span
      class="into"
      data-part="into"
      title="Its edit goes into {row.into}{row.later ? ` (${row.later} is newer: ${row.into} updates in place)` : ''}"
      >→ {row.into}</span
    >{/if}
  <span class="grow"></span>
  <CommentMenu {review} {row} />
</div>
{#if row.kind === 'question'}
  <p class="text asked">{row.text}</p>
{:else}
  <p class="text" class:quiet={!row.text.trim()}>{text}</p>
{/if}
{#if doing}<p class="doing" data-part="doing">{doing}</p>{/if}
{#if strokes.length || row.images.length}
  <Sketch {strokes} images={row.images} {aspect} {frame} onclick={() => review.jumpToComment(row)} />
{/if}
{#if status.state === 'done' && change}
  <p class="change"><span>{change}</span></p>
{/if}
{#if status.paused}
  <p class="paused">
    <span>{status.paused}</span>{#if review.canRecord && task}<button class="link" onclick={retry}>Retry</button>{/if}
  </p>
{/if}
{#if status.waiting}<p class="waiting">{status.waiting}</p>{/if}
{#if asked}<p class="asked">{asked}</p>{/if}
{#if row.ask}
  <AskCard {review} {row} />
{:else if voiceThread != null}
  <ReplyBox onmic={() => review.answer(voiceThread, row.t0)} recording={answering} />
{:else if workerAsks}
  <ReplyBox onsend={answer} busy={review.automation.busy} />
{/if}
