<script lang="ts">
  // A video's ⋯ (spec/home.md, "A video's ⋯"; spec/notes.md, "The video's ⋯"): Share…, Agent for this video ▸,
  // Download, Show in folder and Move to Trash…, each only where it can work, and no ⋯ at all when none can. On a card it
  // shows on hover or focus (always, quiet, on a phone); on the review page it's a round button over the video. Nothing
  // about the agent shows anywhere else: the list checks the one in use.
  import { DropdownMenu } from 'bits-ui';
  import {
    agentChoices,
    anyItem,
    menuItems,
    type AgentState,
    type MenuContext,
    type VideoTarget,
  } from '../../lib/video-menu';
  import { loadAgents, revealFolder, setVideoAgent } from '../../lib/video-menu-api';
  import { menus } from './menus.svelte';
  import './menus.css';

  let {
    target,
    context,
    variant = 'card',
    ondownload,
  }: {
    target: VideoTarget;
    context: Omit<MenuContext, 'agents'>;
    variant?: 'card' | 'review';
    /** The version menu's Download, when the page has it (a hook). */
    ondownload?: () => void;
  } = $props();

  let open = $state(false);
  let auto = $state.raw<AgentState | null>(null);
  let loaded = $state(false);
  const choices = $derived(agentChoices(auto));
  const items = $derived(menuItems({ ...context, agents: loaded ? choices.agents.length > 0 : null }));

  // The agents are read when the menu opens: a card doesn't ask its video's server until then.
  $effect(() => {
    if (!open || !items.agent) return;
    void loadAgents(target.api).then((a) => {
      auto = a;
      loaded = true;
    });
  });

  async function pick(agent: string) {
    try {
      await setVideoAgent(target.api, agent);
      const name = agent ? choices.agents.find((a) => a.id === agent)?.name : choices.defaultName;
      menus.say(agent ? `${name} edits this video` : `Back to your default${name ? ` (${name})` : ''}`);
      auto = await loadAgents(target.api);
    } catch (e) {
      menus.say(e instanceof Error ? e.message : 'Couldn’t change the agent');
    }
  }

  async function reveal() {
    if (target.home === null) return;
    try {
      await revealFolder(target.home, target.name);
    } catch (e) {
      menus.say(e instanceof Error ? e.message : 'Couldn’t open the folder');
    }
  }
</script>

{#if anyItem(items)}
  <DropdownMenu.Root bind:open>
    <DropdownMenu.Trigger
      class="more vm-more {variant}"
      data-part={variant === 'review' ? 'more video-menu home-nav' : 'more video-menu'}
      aria-label={`More for ${target.title}`}
      title="More"
    >
      <svg viewBox="0 0 20 20" aria-hidden="true"
        ><circle cx="4.5" cy="10" r="1.5" /><circle cx="10" cy="10" r="1.5" /><circle cx="15.5" cy="10" r="1.5" /></svg
      >
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content
        class="menu vm-menu"
        data-part="menu"
        align={variant === 'review' ? 'start' : 'end'}
        sideOffset={4}
        collisionPadding={10}
      >
        {#if items.share}
          <DropdownMenu.Item class="menu-item" onSelect={() => (menus.share = target)}>Share…</DropdownMenu.Item>
        {/if}
        {#if items.agent}
          <DropdownMenu.Sub>
            <DropdownMenu.SubTrigger class="menu-item sub">Agent for this video</DropdownMenu.SubTrigger>
            <DropdownMenu.SubContent class="menu vm-agents" data-part="menu" sideOffset={6} collisionPadding={10}>
              {#if !loaded}
                <DropdownMenu.Item class="menu-item" disabled>Loading…</DropdownMenu.Item>
              {:else}
                {#each choices.agents as a (a.id)}
                  <DropdownMenu.Item class="menu-item" onSelect={() => pick(a.id)}
                    >{a.name}<span class="check" aria-hidden="true">{a.checked ? '✓' : ''}</span></DropdownMenu.Item
                  >
                {/each}
                <DropdownMenu.Separator class="menu-sep" />
                <DropdownMenu.Item class="menu-item" onSelect={() => pick('')}
                  >Use my default{choices.defaultName ? ` (${choices.defaultName})` : ''}<span
                    class="check"
                    aria-hidden="true">{choices.useDefault ? '✓' : ''}</span
                  ></DropdownMenu.Item
                >
              {/if}
            </DropdownMenu.SubContent>
          </DropdownMenu.Sub>
        {/if}
        {#if items.download && ondownload}
          <DropdownMenu.Item class="menu-item" onSelect={ondownload}>{context.download?.label}</DropdownMenu.Item>
        {/if}
        {#if items.reveal}
          <DropdownMenu.Item class="menu-item" onSelect={reveal}>Show in folder</DropdownMenu.Item>
        {/if}
        {#if items.trash}
          {#if items.share || items.agent || items.download || items.reveal}<DropdownMenu.Separator
              class="menu-sep"
            />{/if}
          <DropdownMenu.Item class="menu-item danger" onSelect={() => (menus.trash = target)}
            >Move to {menus.bin}…</DropdownMenu.Item
          >
        {/if}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
{/if}
