<script lang="ts">
  // One comment: when, its words, what you drew, and one status (Open, Working, Question, Done). A question from the
  // agent has the reply box under it; a paused one says why once, with Retry; a Working one waiting for its computer
  // says so. Everything else is in its ⋯.
  import type { Review } from '../../lib/review.svelte';
  import type { Row } from '../../lib/notes/view';
  import { STATE_TEXT } from '../../lib/status';
  import { verName } from '../../lib/versions';
  import CommentMenu from './CommentMenu.svelte';
  import ReplyBox from '../ReplyBox.svelte';
  import Sketch from './Sketch.svelte';

  let { review, row, active }: { review: Review; row: Row; active: boolean } = $props();

  const status = $derived(review.statusOf(row));
  const task = $derived(review.taskOf(row));
  const strokes = $derived(row.drawings.length || !row.text.trim() ? review.strokesOf(row) : []);
  const aspect = $derived.by(() => {
    const [w, h] = review.aspect.split('/').map(Number);
    return w && h ? w / h : 16 / 9;
  });
  /** Agent questions asked through the notes take a recorded answer; a worker's question takes typed words. */
  const voiceThread = $derived(row.answer);
  const workerAsks = $derived(task?.state === 'question' && review.canRecord ? task : null);
  const asked = $derived(row.kind === 'question' ? null : (workerAsks?.question ?? row.question));
  const answering = $derived(voiceThread != null && review.seg?.answer_to === voiceThread);
  const text = $derived(
    row.text.trim() || (row.drawings.length || row.images.length || strokes.length ? 'Drawing' : 'No words'),
  );

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

<li
  class="item"
  class:active
  class:asking={row.kind === 'question'}
  data-key={row.key}
  data-mark={row.mark}
  data-state={status.state}
>
  <div class="line1">
    {#if review.manyAuthors && row.kind === 'note'}<span class="who"
        ><span class="avatar" aria-hidden="true">{row.by.slice(0, 1).toUpperCase()}</span>{row.by}</span
      >{/if}
    <button class="time jump" title="Go to {row.time}" onclick={() => review.jumpToComment(row)}>{row.time}</button>
    <span class="status {status.state}"><span class="dot" aria-hidden="true"></span>{STATE_TEXT[status.state]}</span>
    {#if row.version && row.version !== review.shown && status.state !== 'done'}<span
        class="origin"
        title="Left on {verName(row.version)}">{verName(row.version)}</span
      >{/if}
    <span class="grow"></span>
    <CommentMenu {review} {row} />
  </div>
  {#if row.kind === 'question'}
    <p class="text asked">{row.text}</p>
  {:else}
    <p class="text" class:quiet={!row.text.trim()}>{text}</p>
  {/if}
  {#if strokes.length || row.images.length}
    <Sketch {strokes} images={row.images} {aspect} onclick={() => review.jumpToComment(row)} />
  {/if}
  {#if status.state === 'done' && row.change}
    <p class="change">
      <span>{row.change}</span>{#if row.see}<button class="link" onclick={() => review.setBase(row.see!)}
          >See {verName(row.see)}</button
        >{/if}
    </p>
  {/if}
  {#if status.paused}
    <p class="paused">
      <span>{status.paused}</span>{#if review.canRecord && task}<button class="link" onclick={retry}>Retry</button>{/if}
    </p>
  {/if}
  {#if status.waiting}<p class="waiting">{status.waiting}</p>{/if}
  {#if asked}<p class="asked">{asked}</p>{/if}
  {#if voiceThread != null}
    <ReplyBox onmic={() => review.answer(voiceThread, row.t0)} recording={answering} />
  {:else if workerAsks}
    <ReplyBox onsend={answer} busy={review.automation.busy} />
  {/if}
</li>
