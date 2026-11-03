<script lang="ts">
  // The one share sheet, Move to Trash confirm and Undo of a page (spec/home.md, "A video's ⋯"), for whichever video's
  // ⋯ opened them. After a move the page shows "Moved to Trash · Undo" for a few seconds.
  import { onDestroy } from 'svelte';
  import { UNDO_MS } from '../../lib/video-menu';
  import { restoreFromTrash } from '../../lib/video-menu-api';
  import { menus } from './menus.svelte';
  import ShareSheet from './ShareSheet.svelte';
  import TrashDialog from './TrashDialog.svelte';

  let { say, onchanged }: { say: (msg: string) => void; onchanged?: () => void } = $props();

  $effect(() => {
    menus.say = say;
  });

  let timer: ReturnType<typeof setTimeout> | undefined;
  let undoing = $state(false);
  onDestroy(() => clearTimeout(timer));

  function moved(id: string) {
    const t = menus.trash;
    if (!t || t.home === null) return;
    menus.undo = { id, title: t.title, home: t.home };
    clearTimeout(timer);
    timer = setTimeout(() => (menus.undo = null), UNDO_MS);
    onchanged?.();
  }

  async function undo() {
    const u = menus.undo;
    if (!u || undoing) return;
    undoing = true;
    clearTimeout(timer);
    try {
      await restoreFromTrash(u.home, u.id);
      menus.undo = null;
      say(`${u.title} is back`);
      onchanged?.();
    } catch (e) {
      menus.undo = null;
      say(e instanceof Error ? e.message : 'Couldn’t put it back');
    } finally {
      undoing = false;
    }
  }
</script>

{#if menus.share}
  {#key menus.share}
    <ShareSheet target={menus.share} onclose={() => (menus.share = null)} />
  {/key}
{/if}
{#if menus.trash}
  {#key menus.trash}
    <TrashDialog target={menus.trash} onclose={() => (menus.trash = null)} onmoved={moved} />
  {/key}
{/if}
<div class="vm-undo" data-part="undo-toast" class:show={!!menus.undo} role="status" aria-live="polite">
  {#if menus.undo}
    <span>Moved to Trash</span><span class="vm-dot" aria-hidden="true">·</span><button
      class="vm-text"
      disabled={undoing}
      onclick={undo}>Undo</button
    >
  {/if}
</div>
