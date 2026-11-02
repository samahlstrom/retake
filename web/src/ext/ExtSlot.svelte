<script lang="ts">
  // Where an extension's UI mounts (spec/extensions.md, "UI slots"): an empty element core owns. The module is imported
  // the first time a slot of its extension shows; a new extension, slot or `key` unmounts it (its cleanup, then
  // everything its ctx subscribed to) and mounts again. A failure stays here as one quiet line.
  import { untrack } from 'svelte';
  import { mountSlot, type ExtInfo, type ExtLoader } from './loader';
  import type { Host } from './ctx';
  import type { SlotName } from './retake-ext';

  let {
    ext,
    slot,
    host,
    loader,
    key = '',
    class: klass = '',
  }: { ext: ExtInfo; slot: SlotName; host: Host; loader: ExtLoader; key?: string; class?: string } = $props();
  let el = $state<HTMLDivElement>();

  $effect(() => {
    const target = el,
      e = ext,
      s = slot,
      h = host;
    void key;
    if (!target) return;
    // Only those remount it: whatever the module or its ctx reads while mounting isn't a dependency.
    const m = untrack(() => mountSlot(target, loader, e, s, h));
    return () => m.unmount();
  });
</script>

<div class="ext-slot {klass}" data-ext={ext.id} data-slot={slot} data-part="ext-slot" bind:this={el}></div>

<style>
  @layer retake {
    .ext-slot {
      min-width: 0;
      min-height: 0;
    }
    .ext-slot :global(.ext-error) {
      margin: 0;
      padding: 14px 18px;
      color: var(--muted);
      font-size: 13px;
      user-select: text;
    }
  }
</style>
