<script lang="ts">
  // Pick a folder on this computer (spec/setup.md): the folders in one folder, Up, and Use this folder. The setup page
  // and Settings use it; the server lists the folders (a browser can't give a page a path).
  import { onMount } from 'svelte';
  import { join, listFolders, type FolderList } from '../lib/folders';

  let {
    start,
    endpoint,
    onpick,
    oncancel,
  }: {
    start: string;
    endpoint: string;
    onpick: (path: string) => void;
    oncancel: () => void;
  } = $props();

  let list = $state<FolderList | null>(null);
  let error = $state('');
  let busy = $state(false);

  async function go(path: string) {
    busy = true;
    error = '';
    try {
      list = await listFolders(endpoint, path);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Can’t read that folder';
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    void go(start).then(() => {
      if (!list && start !== '~') void go('~'); // a folder that isn't there yet: start at home
    });
  });
</script>

<div class="picker" data-part="folder-picker" aria-busy={busy}>
  <div class="head">
    <button
      class="up"
      disabled={!list?.parent || busy}
      onclick={() => list && go(list.parent)}
      aria-label="Up one folder"
      ><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 12.5v-9M4 7.5l4-4 4 4" /></svg></button
    >
    <code class="path">{list?.path ?? start}</code>
  </div>
  {#if error}<p class="err" role="alert">{error}</p>{/if}
  <ul class="folders" aria-label="Folders">
    {#each list?.folders ?? [] as name (name)}
      <li>
        <button class="folder" disabled={busy} onclick={() => list && go(join(list.path, name))}>
          <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 4.5h4l1.5 1.5H14v6.5H2z" /></svg>
          <span>{name}</span>
        </button>
      </li>
    {:else}
      {#if list}<li class="none">No folders in here</li>{/if}
    {/each}
  </ul>
  <div class="foot">
    <button class="quiet" onclick={oncancel}>Cancel</button>
    <button class="primary" disabled={!list || busy} onclick={() => list && onpick(list.path)}>Use this folder</button>
  </div>
</div>

<style>
  @layer retake {
    .picker {
      display: grid;
      gap: 0;
      margin-top: 10px;
      border: 1px solid var(--line-strong);
      border-radius: var(--radius);
      background: var(--surface-sunken);
      overflow: hidden;
    }
    .head {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 6px 8px;
      border-bottom: 1px solid var(--line);
    }
    .path {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font: 12px var(--font-mono);
      color: var(--text-3);
    }
    .up {
      flex: none;
      display: grid;
      place-items: center;
      width: 30px;
      height: 30px;
      padding: 0;
      border: 0;
      border-radius: var(--radius-sm);
      background: transparent;
      color: var(--text-4);
      cursor: pointer;
    }
    .up:hover:not(:disabled) {
      background: var(--surface-control-hover);
    }
    .up:disabled {
      opacity: 0.35;
      cursor: default;
    }
    svg {
      width: 16px;
      height: 16px;
      fill: none;
      stroke: currentColor;
      stroke-width: 1.4;
      stroke-linecap: round;
      stroke-linejoin: round;
    }
    .folders {
      height: 220px;
      margin: 0;
      padding: 4px;
      list-style: none;
      overflow-y: auto;
    }
    .folder {
      display: flex;
      align-items: center;
      gap: 10px;
      width: 100%;
      min-height: 34px;
      padding: 0 8px;
      border: 0;
      border-radius: var(--radius-sm);
      background: transparent;
      color: var(--text-2);
      font: inherit;
      text-align: left;
      cursor: pointer;
    }
    .folder svg {
      flex: none;
      color: var(--muted);
    }
    .folder span {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .folder:hover:not(:disabled) {
      background: var(--surface-control-hover);
    }
    .none {
      padding: 10px 8px;
      color: var(--muted);
    }
    .err {
      margin: 0;
      padding: 8px 10px 0;
      color: var(--danger);
      font-size: 12.5px;
    }
    .foot {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
      padding: 8px;
      border-top: 1px solid var(--line);
    }
    .foot button {
      min-height: 32px;
      padding: 0 14px;
      border-radius: var(--radius);
      font: inherit;
      font-weight: 600;
      cursor: pointer;
    }
    .quiet {
      border: 1px solid var(--border);
      background: transparent;
      color: var(--text-3);
    }
    .quiet:hover {
      border-color: var(--border-hover);
    }
    .primary {
      border: 0;
      background: var(--accent);
      color: var(--on-accent);
    }
    .primary:hover:not(:disabled) {
      background: var(--accent-hover);
    }
    .primary:disabled {
      opacity: 0.5;
      cursor: default;
    }
    button:focus-visible {
      outline: 2px solid var(--focus);
      outline-offset: 2px;
    }
    @media (max-width: 760px) {
      .folder,
      .foot button {
        min-height: 44px;
      }
      .up {
        width: 44px;
        height: 44px;
      }
    }
  }
</style>
