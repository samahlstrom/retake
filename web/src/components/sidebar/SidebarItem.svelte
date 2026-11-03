<script lang="ts">
  // One comment's content (the list draws its card, the <li>, and moves it: Sidebar.svelte): when, its words, what
  // you drew, one status (Open, Working, Question, Done) and, until it's done,
  // the version its edit goes into ("→ v8": a comment on v7 becomes an edit in v8). A question from the
  // agent has the reply box under it; a paused one says why once, with Retry; a Working one waiting for its computer
  // says so. The one the agent is on has the Working dot and a quiet line in the agent's words under its own; the rest
  // of its batch read quietly, only their "→ v8" (the word stays for screen readers); a done one says what changed and
  // "in v5", which plays v5 at that moment (spec/automation.md, "While the agent works"). Everything else is in its ⋯.
  // While it's still to do its words can be edited (spec/notes.md, "Editing a comment"): ⋯ → Edit, or a double-click on
  // them (a tap on a phone, where the comment box is the field), and here they become a field in their own place.
  import type { Review } from '../../lib/review.svelte';
  import { anchorText, sketchFrame, type Row } from '../../lib/notes/view';
  import { STATE_TEXT } from '../../lib/status';
  import { canEdit, editKey } from '../../lib/notes/edit.svelte';
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
  /** Sent, and the agent isn't on it (yet): no dot and no word, only where it goes. */
  const quiet = $derived(status.state === 'working' && !status.now);
  /** The agent's line: its words, trailed off ("slowing the mask fade…"). */
  const doing = $derived(status.doing ? (/[.…!?]$/.test(status.doing) ? status.doing : `${status.doing}…`) : '');

  /** Its words may change now: still to do, the agent not on it, and this page may. */
  const editable = $derived(review.canRecord && canEdit(row, status, task));
  const edit = $derived(review.edit);
  /** Its words are a field here (on a phone the comment box is the field instead). */
  const editing = $derived(edit.editing(row.id));
  let field = $state<HTMLTextAreaElement | null>(null);
  $effect(() => {
    // The field takes the focus once, with the caret after the words.
    if (!field) return;
    const el = field;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  });
  $effect(() => {
    // The field is as tall as its words, like the words it replaced: nothing around it moves.
    void edit.text;
    if (!field) return;
    field.style.height = 'auto';
    field.style.height = `${field.scrollHeight}px`;
  });
  $effect(() => {
    // The agent started on it (or it's done) while it was being edited: its words stay as they are.
    if (edit.editing(row.id, true) && !editable && !edit.busy) {
      edit.cancel();
      review.toast.say('The agent started on this comment, so its words stay as they were.', 5000);
    }
  });
  function editKeys(e: KeyboardEvent) {
    const k = editKey(e);
    if (!k) return;
    e.preventDefault();
    e.stopPropagation();
    if (k === 'cancel') edit.cancel();
    else void edit.save();
  }
  function startEdit() {
    getSelection()?.removeAllRanges();
    review.editComment(row);
  }

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
  <span class="status {status.state}" class:quiet data-part="status"
    >{#if !quiet}<span class="dot" aria-hidden="true"></span>{/if}{STATE_TEXT[status.state]}</span
  >
  {#if quiet && !row.into}<span class="into" aria-hidden="true">Sent</span>{/if}
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
  {#if editing}
    <!-- Cancel and Save take the ⋯'s place while its words are a field: no new row, nothing moves. -->
    <span class="edit-actions">
      <button type="button" title="Cancel (Esc)" onclick={edit.cancel} disabled={edit.busy}>Cancel</button>
      <button type="button" class="strong" title="Save (Enter)" onclick={edit.save} disabled={edit.busy}>Save</button>
    </span>
  {:else}
    <CommentMenu {review} {row} />
  {/if}
</div>
{#if row.kind === 'question'}
  <p class="text asked">{row.text}</p>
{:else if editing}
  <textarea
    bind:this={field}
    bind:value={edit.text}
    class="text editing"
    data-part="comment-edit"
    rows="1"
    placeholder="Add words…"
    aria-label="Edit the comment at {row.time}"
    readonly={edit.busy}
    onkeydown={editKeys}></textarea>
{:else}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
  <p
    class="text"
    class:quiet={!row.text.trim()}
    class:editable
    title={row.edited ? 'Edited' : undefined}
    ondblclick={editable && !review.touch ? startEdit : undefined}
    onclick={editable && review.touch ? startEdit : undefined}
  >
    {text}
  </p>
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
