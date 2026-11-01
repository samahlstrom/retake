<script lang="ts">
  import StyledSelect from '../ui/Select.svelte';
  import type { Review } from '../../lib/review.svelte';
  import type { Row } from '../../lib/notes/view';
  import { taskLabel } from '../../lib/automation.svelte';
  let { review, row }: { review: Review; row: Row } = $props();
  const automation = $derived(review.automation);
  const task = $derived(automation.state?.tasks.find((t) => t.id === row.work?.id) ?? row.work);
  const machine = $derived(automation.state?.workers.find((w) => w.id === automation.state?.config.worker));
  let reply = $state(''),
    agent = $state(''),
    loaded = $state('');
  $effect(() => {
    const key = `${task?.id}:${task?.agent}`;
    if (key !== loaded) {
      loaded = key;
      agent = task?.agent || '';
    }
  });
  async function act(action: string, body: object = {}) {
    if (task && (await automation.send(`/tasks/${task.id}/${action}`, body))) {
      reply = '';
      await review.notes.refresh();
    }
  }
</script>

{#if task}
  <div class="task" data-state={task.state}>
    {#if task.state !== 'completed'}
      <div class="task-status">
        <span class:running={task.state === 'running'}>{taskLabel(task)}</span><span>{task.agent}</span>
      </div>
    {/if}
    {#if task.state === 'stopping'}<p>Stopping the current agent before handing over…</p>{/if}
    {#if task.error}<p class="task-error">{task.error}</p>{/if}
    {#if task.question}
      <p class="task-question">{task.question}</p>
      {#if review.canRecord}<form
          onsubmit={(e) => {
            e.preventDefault();
            void act('answer', { message: reply });
          }}
        >
          <textarea aria-label="Reply to agent question" placeholder="Clarify this edit…" bind:value={reply} rows="2"
          ></textarea>
          <button disabled={automation.busy || !reply.trim()} type="submit">Reply and continue</button>
        </form>{/if}
    {/if}
    {#if review.role === 'owner' && task.state !== 'completed'}
      <div class="task-actions">
        <StyledSelect
          label={`Agent for comment ${row.id}`}
          compact
          value={agent}
          disabled={automation.busy || task.state === 'stopping'}
          options={(machine?.agents ?? []).map((profile) => ({ value: profile.id, label: profile.name }))}
          onValueChange={(value) => {
            agent = value;
          }}
        />
        <button
          disabled={automation.busy || !agent || task.state === 'stopping'}
          onclick={() => act(task.state === 'paused' ? 'retry' : 'switch', { agent })}
          >{task.state === 'paused' ? 'Retry' : 'Switch agent'}</button
        >
        {#if task.state === 'running' || task.state === 'queued'}<button
            disabled={automation.busy}
            onclick={() => act('pause')}>Pause</button
          >{/if}
      </div>
    {/if}
    <details>
      <summary>Task history{task.usage ? ' · Usage' : ''}</summary>
      {#each task.events as event, i (i)}<p>
          <small
            >{new Date(event.at * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}{event.agent
              ? ` · ${event.agent}`
              : ''}</small
          ><br />{event.text}
        </p>{/each}
      {#if task.usage}<p>
          {task.usage.input_tokens ?? 0} input · {task.usage.output_tokens ?? 0} output tokens{task.usage
            .estimated_cost_usd != null
            ? ` · $${task.usage.estimated_cost_usd.toFixed(3)} estimated`
            : ''}
        </p>
        <small>Remaining subscription allowance is not reported by this agent.</small>{/if}
    </details>
    {#if automation.error}<p class="task-error" role="alert">{automation.error}</p>{/if}
  </div>
{:else if row.kind === 'note' && row.mark === 'open'}
  <div class="task-status">
    <span>Commented</span>{#if review.canRecord}<button
        disabled={automation.busy}
        onclick={() => automation.send('/tasks', { notes: [row.id] })}>Send to agent</button
      >{/if}
  </div>
{/if}

<style>
  .task {
    margin-top: 12px;
    padding-top: 10px;
    border-top: 1px solid #ffffff12;
    font-size: 12px;
  }
  .task-status,
  .task-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 8px;
    color: #aaa69d;
    font-size: 11px;
  }
  .task-status > span:first-child {
    color: #ccc6b6;
  }
  .task-status > span.running {
    color: #d7c28c;
  }
  button {
    border: 1px solid #ffffff20;
    border-radius: 5px;
    background: #ffffff08;
    color: #d4d0c7;
    padding: 5px 7px;
    font: inherit;
    cursor: pointer;
  }
  .task-actions :global(.retake-select-trigger) {
    max-width: 150px;
  }
  button:disabled {
    opacity: 0.5;
  }
  .task-error {
    color: #dda18f;
  }
  .task-question {
    color: #ead197;
    white-space: pre-wrap;
  }
  textarea {
    box-sizing: border-box;
    display: block;
    width: 100%;
    resize: vertical;
    background: #151514;
    border: 1px solid #ffffff24;
    color: #e7e3db;
    border-radius: 6px;
    padding: 8px;
    font: inherit;
    margin-bottom: 6px;
  }
  details {
    margin-top: 10px;
    color: #aaa69d;
  }
  summary {
    cursor: pointer;
    font-size: 11px;
  }
  details p {
    white-space: pre-wrap;
  }
  small {
    color: #8f8b83;
  }
</style>
