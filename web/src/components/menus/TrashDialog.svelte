<script lang="ts">
  // Move to Trash's confirm (spec/home.md, "Move to Trash"): exactly what goes, the folder, its versions and its size,
  // before anything moves. Cancel has the focus. A video that can't go says why.
  import { AlertDialog } from 'bits-ui';
  import { trashLine, type TrashInfo, type VideoTarget } from '../../lib/video-menu';
  import { moveToTrash, trashInfo } from '../../lib/video-menu-api';

  let { target, onclose, onmoved }: { target: VideoTarget; onclose: () => void; onmoved: (id: string) => void } =
    $props();

  let open = $state(true);
  let info = $state.raw<TrashInfo | null>(null);
  let error = $state('');
  let busy = $state(false);
  let cancel = $state<HTMLButtonElement | null>(null);

  $effect(() => {
    if (target.home === null) return;
    void trashInfo(target.home, target.name).then(
      (i) => (info = i),
      (e: unknown) => (error = e instanceof Error ? e.message : 'Couldn’t read the folder'),
    );
  });

  async function move() {
    if (target.home === null || busy) return;
    busy = true;
    error = '';
    try {
      const r = await moveToTrash(target.home, target.name);
      open = false;
      onmoved(r.id);
      onclose();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Couldn’t move it to the Trash';
    } finally {
      busy = false;
    }
  }
</script>

<AlertDialog.Root
  bind:open
  onOpenChange={(v) => {
    if (!v) onclose();
  }}
>
  <AlertDialog.Portal>
    <AlertDialog.Overlay class="vm-overlay" />
    <AlertDialog.Content
      class="vm-sheet vm-confirm"
      data-part="trash-confirm"
      onOpenAutoFocus={(e) => {
        e.preventDefault(); // Cancel has the focus: Enter never moves a folder by itself
        setTimeout(() => cancel?.focus(), 0);
      }}
    >
      <AlertDialog.Title class="vm-title"
        >Move <span class="vm-what">{target.title}</span> to the Trash?</AlertDialog.Title
      >
      <AlertDialog.Description class="vm-desc">
        {#if info}
          <code class="vm-path" data-testid="trash-path">{info.path}</code>
          <span class="vm-sum" data-testid="trash-sum">{trashLine(info)}</span>
          {#if info.ok}
            <span class="vm-note"
              >Its versions, comments and the agent’s work go with it. Undo is there for a few seconds; after that,
              restore it from the Trash.</span
            >
          {:else}
            <span class="vm-err">{info.why}</span>
          {/if}
        {:else if !error}
          <span class="vm-note">Reading the folder…</span>
        {/if}
      </AlertDialog.Description>
      {#if error}<p class="vm-err" role="alert">{error}</p>{/if}
      <div class="vm-actions">
        <AlertDialog.Cancel class="vm-btn" bind:ref={cancel}>{info && !info.ok ? 'Close' : 'Cancel'}</AlertDialog.Cancel
        >
        {#if !info || info.ok}
          <AlertDialog.Action class="vm-btn danger-fill" disabled={!info || busy} onclick={move}
            >{busy ? 'Moving…' : 'Move to Trash'}</AlertDialog.Action
          >
        {/if}
      </div>
    </AlertDialog.Content>
  </AlertDialog.Portal>
</AlertDialog.Root>
