<script lang="ts">
  import { Popover } from 'bits-ui';
  import StyledSelect from './ui/Select.svelte';
  import type { Review } from '../lib/review.svelte';
  import { taskLabel } from '../lib/automation.svelte';
  let { review }: { review: Review } = $props();
  const automation = $derived(review.automation);
  const data = $derived(automation.state);
  const owner = $derived(review.role === 'owner');
  let worker = $state(''),
    agent = $state(''),
    fallback = $state(''),
    enabled = $state(false);
  let loaded = $state('');
  const machine = $derived(data?.workers.find((w) => w.id === worker));
  const active = $derived(data?.tasks.find((t) => t.state === 'running' || t.state === 'stopping'));
  $effect(() => {
    const key = JSON.stringify(data?.config);
    if (data && key !== loaded) {
      loaded = key;
      worker = data.config.worker || data.workers[0]?.id || '';
      agent = data.config.agent || data.workers.find((w) => w.id === worker)?.agents[0]?.id || '';
      fallback = data.config.fallback || '';
      enabled = data.config.enabled;
    }
  });
  const online = (seen: number) => Date.now() / 1000 - seen < 10;
</script>

<Popover.Root>
  <Popover.Trigger class="agent-trigger" aria-label="Agent controls">{active ? '● Editing' : 'Agent'}</Popover.Trigger>
  <Popover.Portal>
    <Popover.Content class="agent-panel" side="top" align="end" sideOffset={12}>
      <div class="agent-title">
        <strong>Editing agent</strong><Popover.Close aria-label="Close agent controls">×</Popover.Close>
      </div>
      {#if owner}
        {#if data?.workers.length}
          <div class="agent-field">
            <span>Editing machine</span>
            <StyledSelect
              label="Editing machine"
              fullWidth
              value={worker}
              options={data.workers.map((w) => ({
                value: w.id,
                label: `${w.name} · ${online(w.seen) ? 'Online' : 'Offline'}`,
              }))}
              onValueChange={(value) => {
                worker = value;
                agent = data.workers.find((w) => w.id === value)?.agents[0]?.id || '';
                fallback = '';
              }}
            />
          </div>
          <div class="agent-field">
            <span>Default agent</span>
            <StyledSelect
              label="Default agent"
              fullWidth
              value={agent}
              options={(machine?.agents ?? []).map((a) => ({
                value: a.id,
                label: `${a.name}${a.model ? ` · ${a.model}` : ''}`,
              }))}
              onValueChange={(value) => {
                agent = value;
                if (fallback === value) fallback = '';
              }}
            />
          </div>
          <div class="agent-field">
            <span>When usage runs out</span>
            <StyledSelect
              label="When usage runs out"
              fullWidth
              value={fallback}
              options={[
                { value: '', label: 'Pause and let me choose' },
                ...(machine?.agents.filter((a) => a.id !== agent) ?? []).map((a) => ({
                  value: a.id,
                  label: `Switch to ${a.name}`,
                })),
              ]}
              onValueChange={(value) => {
                fallback = value;
              }}
            />
          </div>
          <label class="agent-check"
            ><input type="checkbox" bind:checked={enabled} /> Start edits when feedback is submitted</label
          >
          <button
            class="agent-primary"
            disabled={automation.busy}
            onclick={() => automation.send('', { worker, agent, fallback, enabled })}>Save agent settings</button
          >
        {:else}
          <p>Connect the machine holding this project's footage and source. Its installed agents will appear here.</p>
          <code>retake worker --remote NAME --project DIR</code>
        {/if}
      {:else}
        <p>
          {data?.config.enabled
            ? 'Submitted feedback goes to the project’s editing machine.'
            : 'The project owner has not enabled automatic editing.'}
        </p>
      {/if}
      {#if review.canRecord && review.rows.some((r) => r.kind === 'note' && r.mark === 'open' && !r.work)}
        <button
          disabled={automation.busy}
          onclick={() =>
            automation.send('/tasks', {
              notes: review.rows.filter((r) => r.kind === 'note' && r.mark === 'open' && !r.work).map((r) => r.id),
            })}>Submit existing open comments</button
        >
      {/if}
      {#if data?.tasks.length}
        <div class="agent-queue">
          {#each data.tasks.slice(-6).toReversed() as task (task.id)}
            <div>
              <strong>Comment {task.notes.join(', ')} · {taskLabel(task)}</strong><span
                >{task.agent || 'Waiting for an agent'}{task.state === 'stopping'
                  ? ' · Stopping for handoff…'
                  : ''}</span
              >
              {#if task.error}<p class="agent-error">{task.error}</p>{/if}
              {#if task.question}<p>{task.question}</p>{/if}
              {#if task.result}<button onclick={() => review.setBase(task.result!)}>View {task.result}</button>{/if}
            </div>
          {/each}
        </div>
      {/if}
      {#if automation.error}<p class="agent-error" role="alert">{automation.error}</p>{/if}
      <p class="agent-foot">
        Edits stream as they render. Previous versions stay available while the editing machine is online.
      </p>
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>

<style>
  :global(.agent-trigger) {
    flex-shrink: 0;
    color: #e2ded5;
    border: 1px solid #ffffff24;
    border-radius: 7px;
    padding: 6px 10px;
    background: #ffffff08;
    font-size: 12px;
  }
  :global(.agent-panel) {
    z-index: 80;
    width: min(350px, calc(100vw - 24px));
    max-height: min(680px, 80dvh);
    overflow: auto;
    background: #242321;
    color: #e8e4dd;
    padding: 18px;
    border: 1px solid #ffffff24;
    border-radius: 12px;
    box-shadow: 0 16px 60px #0008;
    font: 13px/1.5 system-ui;
  }
  .agent-title {
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 15px;
    margin-bottom: 15px;
  }
  label,
  .agent-field {
    display: block;
    margin: 12px 0;
    color: #c0bcb3;
  }
  .agent-field :global(.retake-select-trigger) {
    margin-top: 5px;
  }
  .agent-check {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  button,
  :global(.agent-title button) {
    border: 1px solid #ffffff24;
    border-radius: 6px;
    background: #ffffff09;
    color: inherit;
    padding: 7px 10px;
    cursor: pointer;
  }
  button:disabled {
    opacity: 0.5;
  }
  .agent-primary {
    width: 100%;
    background: #ddd5b9;
    color: #25231d;
    margin-bottom: 8px;
  }
  .agent-queue {
    margin-top: 16px;
    border-top: 1px solid #ffffff20;
  }
  .agent-queue > div {
    padding: 12px 0;
    border-bottom: 1px solid #ffffff15;
  }
  .agent-queue strong,
  .agent-queue span {
    display: block;
  }
  .agent-queue span,
  .agent-foot {
    color: #aaa69d;
    font-size: 12px;
  }
  .agent-error {
    color: #edaa96;
  }
  code {
    display: block;
    overflow-wrap: anywhere;
    background: #151514;
    padding: 10px;
    border-radius: 6px;
    font-size: 11px;
  }
</style>
