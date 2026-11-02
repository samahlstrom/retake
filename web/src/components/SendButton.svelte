<script lang="ts">
  // The bar's one action: Send. It finishes this take, saves a pending typed comment, and hands every open comment of
  // this version to the agent (lib/status.ts, sendButton). It never names the agent; the count is in its title.
  // Holding it (about half a second, mouse or touch; a right-click too) lists the agents the editing computer offers,
  // when there are two or more: the pick goes with this one send. With one agent, holding it is a plain Send.
  import { Popover } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';

  let { review }: { review: Review } = $props();

  /** How long a press is a hold. */
  const HOLD_MS = 500;

  const b = $derived(review.button);
  const agents = $derived(review.agent.worker?.agents ?? []);
  const pick = $derived(agents.length > 1);
  const configured = $derived(review.automation.state?.config?.agent ?? '');
  const title = $derived(pick ? `${b.title}. Hold to choose the agent.` : b.title);

  let btn = $state<HTMLButtonElement | null>(null);
  let timer: ReturnType<typeof setTimeout> | null = null;
  /** The press became a hold (the list opened): its click is not a Send. */
  let held = false;

  function down(e: PointerEvent) {
    if (e.button !== 0) return;
    held = false;
    stop();
    if (!pick) return;
    timer = setTimeout(() => {
      timer = null;
      held = true;
      review.pickerOpen = true;
      navigator.vibrate?.(10);
    }, HOLD_MS);
  }
  function stop() {
    if (timer) clearTimeout(timer);
    timer = null;
  }
  function click(e: MouseEvent) {
    stop();
    if (held) {
      held = false;
      e.preventDefault();
      return;
    }
    void review.primary();
  }
  function menu(e: MouseEvent) {
    e.preventDefault(); // a long touch's context menu, or a right-click
    if (!pick) return;
    stop();
    held = true;
    review.pickerOpen = true;
  }
</script>

<button
  id="done"
  data-part="send"
  class="primary"
  class:saved={b.label === 'Saved' || b.label === 'Sent'}
  aria-busy={review.finishing || review.sending}
  aria-label={b.title}
  aria-haspopup={pick ? 'menu' : undefined}
  {title}
  bind:this={btn}
  onpointerdown={down}
  onpointerup={stop}
  onpointerleave={stop}
  onpointercancel={stop}
  oncontextmenu={menu}
  onclick={click}>{b.label}</button
>
{#if pick}
  <Popover.Root bind:open={() => review.pickerOpen, (v) => (review.pickerOpen = v)}>
    <Popover.Portal>
      <Popover.Content
        class="menu agent-picker"
        data-part="agent-picker agent-menu"
        customAnchor={btn}
        side="top"
        align="end"
        sideOffset={10}
        collisionPadding={10}
        onOpenAutoFocus={(e) => e.preventDefault()}
        onCloseAutoFocus={(e) => e.preventDefault()}
      >
        <p class="menu-label">Send to</p>
        {#each agents as a (a.id)}
          <button class="menu-item" data-agent={a.id} onclick={() => void review.primary(a.id)}
            ><span>{a.name}{a.model ? ` · ${a.model}` : ''}</span>{#if a.id === configured}<span class="check"
                >default</span
              >{/if}</button
          >
        {/each}
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
{/if}
