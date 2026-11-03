<script lang="ts">
  // A comment's ⋯: everything you do to a comment now and then (edit its words, send just this one, retry, switch
  // agent, pause, its history, reopen, delete). It shows on hover or focus (always on touch), so the list stays words
  // and pictures. No ⋯ at all when there's nothing to do.
  import { DropdownMenu, Popover } from 'bits-ui';
  import type { Review } from '../../lib/review.svelte';
  import type { Row } from '../../lib/notes/view';
  import { activeTask } from '../../lib/status';
  import { canEdit } from '../../lib/notes/edit.svelte';

  let { review, row }: { review: Review; row: Row } = $props();

  const automation = $derived(review.automation);
  const task = $derived(review.taskOf(row));
  const active = $derived(activeTask(task));
  const status = $derived(review.statusOf(row));
  const owner = $derived(review.owner);
  const agents = $derived(review.agent.worker?.agents ?? []);
  const others = $derived(agents.filter((a) => a.id !== active?.agent));

  // Anyone who records sends and retries (with no computer online it waits in the queue); the owner also switches
  // the agent and pauses. Local mode that can't edit sends nothing, as the bar's Send only saves there.
  const blocked = $derived(!!(review.agent.local && review.agent.blocked));
  const canSend = $derived(
    review.canRecord && !blocked && row.kind === 'note' && status.state === 'open' && !status.paused && !active,
  );
  const canRetry = $derived(review.canRecord && !!status.paused && !!active);
  const canSwitch = $derived(owner && !!active && active.state !== 'stopping' && others.length > 0);
  const canPause = $derived(owner && !!active && (active.state === 'running' || active.state === 'queued'));
  const hasHistory = $derived(!!task?.events.length);
  const canDelete = $derived(row.kind === 'note' && review.canApprove);
  // Its words, while it's still to do and the agent isn't on it (spec/notes.md, "Editing a comment"); hidden otherwise.
  const canEditWords = $derived(review.canRecord && canEdit(row, status, task));
  const any = $derived(
    canEditWords || canSend || canRetry || canSwitch || canPause || hasHistory || row.reopen || canDelete,
  );

  let trigger = $state<HTMLElement | null>(null);
  let history = $state(false);

  async function act(action: string, body: object = {}) {
    if (active && (await automation.send(`/tasks/${active.id}/${action}`, body))) await review.notes.refresh();
    else if (automation.error) review.toast.say(automation.error, 5000);
  }
  async function send() {
    if (await automation.send('/tasks', { notes: [row.id] })) await review.notes.refresh();
    else review.toast.say(automation.error || 'Could not send. Try again.', 5000);
  }
  const time = (at: number) => new Date(at * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
</script>

{#if any}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class="more"
      data-part="more"
      aria-label="More for this comment"
      title="More"
      bind:ref={trigger}
    >
      <svg viewBox="0 0 20 20" aria-hidden="true"
        ><circle cx="4.5" cy="10" r="1.5" /><circle cx="10" cy="10" r="1.5" /><circle cx="15.5" cy="10" r="1.5" /></svg
      >
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content
        class="menu"
        data-part="menu"
        align="end"
        sideOffset={4}
        collisionPadding={10}
        onCloseAutoFocus={(e) => {
          // Edit put the focus in the field (the card's, or the comment box on a phone): it stays there.
          if (review.edit.editing(row.id, true)) e.preventDefault();
        }}
      >
        {#if canEditWords}<DropdownMenu.Item class="menu-item" onSelect={() => review.editComment(row)}
            >Edit</DropdownMenu.Item
          >{/if}
        {#if canSend}<DropdownMenu.Item class="menu-item" onSelect={send}>Send to agent</DropdownMenu.Item>{/if}
        {#if canRetry}<DropdownMenu.Item
            class="menu-item"
            onSelect={() => act('retry', owner ? { agent: active!.agent || agents[0]?.id || '' } : {})}
            >Retry</DropdownMenu.Item
          >{/if}
        {#if canSwitch}
          <DropdownMenu.Sub>
            <DropdownMenu.SubTrigger class="menu-item sub">Switch agent</DropdownMenu.SubTrigger>
            <DropdownMenu.SubContent class="menu" data-part="menu" sideOffset={6} collisionPadding={10}>
              {#each others as a (a.id)}
                <DropdownMenu.Item class="menu-item" onSelect={() => act('switch', { agent: a.id })}
                  >{a.name}{a.model ? ` · ${a.model}` : ''}</DropdownMenu.Item
                >
              {/each}
            </DropdownMenu.SubContent>
          </DropdownMenu.Sub>
        {/if}
        {#if canPause}<DropdownMenu.Item class="menu-item" onSelect={() => act('pause')}>Pause</DropdownMenu.Item>{/if}
        {#if hasHistory}<DropdownMenu.Item
            class="menu-item"
            onSelect={() => {
              history = true;
            }}>History</DropdownMenu.Item
          >{/if}
        {#if row.reopen}<DropdownMenu.Item class="menu-item" onSelect={() => review.reopen(row)}
            >Reopen</DropdownMenu.Item
          >{/if}
        {#if canDelete}
          {#if canEditWords || canSend || canRetry || canSwitch || canPause || hasHistory || row.reopen}<DropdownMenu.Separator
              class="menu-sep"
            />{/if}
          <DropdownMenu.Item
            class="menu-item danger"
            disabled={review.notes.deleting}
            onSelect={() => review.notes.deleteComment(row.id)}>Delete</DropdownMenu.Item
          >
        {/if}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
  {#if task && hasHistory}
    <Popover.Root bind:open={history}>
      <Popover.Portal>
        <Popover.Content class="history" customAnchor={trigger} side="left" align="start" sideOffset={8}>
          <strong>History</strong>
          <ol>
            {#each task.events.toReversed() as event, i (i)}
              <li>
                <span class="when">{time(event.at)}{event.agent ? ` · ${event.agent}` : ''}</span>
                <span>{event.text}</span>
              </li>
            {/each}
          </ol>
          {#if task.usage}<p class="usage">
              {task.usage.input_tokens ?? 0} in · {task.usage.output_tokens ?? 0} out tokens{task.usage
                .estimated_cost_usd != null
                ? ` · about $${task.usage.estimated_cost_usd.toFixed(2)}`
                : ''}
            </p>{/if}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  {/if}
{/if}
