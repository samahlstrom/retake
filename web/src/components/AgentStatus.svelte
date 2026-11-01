<script lang="ts">
  // Next to Send: which agent edits, and whether its computer is there ("● Claude Code", green when online). Click it
  // for the few settings there are (the owner's): the agent, what happens when its usage runs out, and the computer.
  // With no computer connected the owner sees how to connect one; everyone else sees that Send waits for it. Local
  // mode that can't edit says what's missing.
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { statusText } from '../lib/status';
  import StyledSelect from './ui/Select.svelte';
  import ConnectBlock from './ConnectBlock.svelte';

  let { review, compact = false }: { review: Review; compact?: boolean } = $props();

  const automation = $derived(review.automation);
  const s = $derived(review.agent);
  const owner = $derived(review.owner);
  const ready = $derived(s.online && !s.blocked);
  const tone = $derived(ready ? (s.working ? 'busy' : 'ok') : 'off');
  const text = $derived(statusText(s, owner));
  const machine = $derived(s.worker);
  const config = $derived(automation.state?.config);
  const agents = $derived(machine?.agents ?? []);
  const agent = $derived(
    agents.find((a) => a.id === config?.agent && config?.worker === machine?.id)?.id ?? agents[0]?.id ?? '',
  );
  const fallback = $derived(config?.worker === machine?.id ? (config?.fallback ?? '') : '');
  const workers = $derived(automation.state?.workers ?? []);
  const needsConnect = $derived(owner && !ready && !(s.local && s.blocked));
  const title = $derived(
    ready && machine
      ? `${s.agent || 'Agent'} ${s.working ? 'is editing' : 'is ready'} on ${machine.name}`
      : s.blocked || (owner ? text : 'Waiting for the editing computer'),
  );

  $effect(() => {
    if (review.agentOpen && needsConnect) void automation.connect(review.homeCtx?.home ?? '');
  });

  async function set(change: { worker?: string; agent?: string; fallback?: string }) {
    await automation.configure({ worker: machine?.id ?? '', agent, fallback, ...change });
  }
</script>

<Popover.Root bind:open={() => review.agentOpen, (v) => (review.agentOpen = v)}>
  <Popover.Trigger id="agent" class="agent-status {tone} {compact ? 'compact' : ''}" aria-label="Agent: {text}" {title}
    ><span class="dot" aria-hidden="true"></span><span class="name">{text}</span></Popover.Trigger
  >
  <Popover.Portal>
    <Popover.Content
      class="pop agent-pop"
      side="top"
      align="end"
      sideOffset={12}
      collisionPadding={12}
      onOpenAutoFocus={(e) => e.preventDefault()}
    >
      {#if ready && machine}
        <div class="pop-head">
          <span class="dot {tone}" aria-hidden="true"></span><strong>{s.agent || 'Agent'}</strong><span class="dim"
            >on {machine.name}{s.working ? ' · editing' : ''}</span
          >
        </div>
        {#if owner}
          <div class="field">
            <span>Agent</span>
            <StyledSelect
              label="Agent"
              fullWidth
              value={agent}
              options={agents.map((a) => ({ value: a.id, label: `${a.name}${a.model ? ` · ${a.model}` : ''}` }))}
              onValueChange={(value) => set({ agent: value, fallback: fallback === value ? '' : fallback })}
            />
            <div class="sub">
              <span>If its usage runs out</span>
              <StyledSelect
                label="If its usage runs out"
                compact
                value={fallback}
                options={[
                  { value: '', label: 'Pause and ask me' },
                  ...agents.filter((a) => a.id !== agent).map((a) => ({ value: a.id, label: `Switch to ${a.name}` })),
                ]}
                onValueChange={(value) => set({ fallback: value })}
              />
            </div>
          </div>
          {#if workers.length > 1}
            <div class="field">
              <span>Computer</span>
              <StyledSelect
                label="Computer"
                fullWidth
                value={machine.id}
                options={workers.map((w) => ({ value: w.id, label: w.name }))}
                onValueChange={(value) =>
                  set({ worker: value, agent: workers.find((w) => w.id === value)?.agents[0]?.id ?? '', fallback: '' })}
              />
            </div>
          {/if}
        {/if}
      {:else if s.local && s.blocked}
        <div class="pop-head">
          <span class="dot off" aria-hidden="true"></span><strong>The agent can’t edit yet</strong>
        </div>
        <p class="note">{s.blocked}</p>
      {:else}
        <!-- Sent comments wait in the queue and start when a computer connects; only the owner can connect one. -->
        <div class="pop-head">
          <span class="dot off" aria-hidden="true"></span><strong>Waiting for the editing computer</strong>
        </div>
        {#if owner}
          {#if machine}<p class="note warn">
              {machine.name} is offline. Start Retake there again, or connect a computer:
            </p>{/if}
          <ConnectBlock command={automation.command} title={machine ? 'Connect a computer' : 'Connect your computer'} />
        {/if}
      {/if}
      {#if automation.error}<p class="note err" role="alert">{automation.error}</p>{/if}
    </Popover.Content>
  </Popover.Portal>
</Popover.Root>
