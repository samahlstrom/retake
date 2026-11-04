<script lang="ts">
  import { onMount } from 'svelte';
  import { join, listFolders, type FolderList } from '../lib/folders';
  import { loadLibrary, saveLibrary, type Library, type LinkedFolder } from './library';

  type Candidate = Pick<LinkedFolder, 'path' | 'subfolders'>;
  let { changed = () => {}, openAdd = false }: { changed?: () => void; openAdd?: boolean } = $props();
  let library = $state.raw<Library | null>(null);
  let browse = $state<FolderList | null>(null);
  let path = $state('~');
  let candidates = $state.raw<Candidate[]>([]);
  let locating = $state.raw<LinkedFolder | null>(null);
  let error = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let adding = $state(false);
  let removing = $state('');
  let dropNote = $state('');

  const computer = $derived(library?.computer.name || 'this computer');
  const newCandidates = $derived(
    candidates.filter((candidate) => !library?.folders.some((folder) => folder.path === candidate.path)),
  );

  async function read() {
    loading = true;
    error = '';
    try {
      library = await loadLibrary();
      if (openAdd) openPicker();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Couldn’t read linked folders.';
    } finally {
      loading = false;
    }
  }

  async function go(next = path) {
    error = '';
    try {
      browse = await listFolders('api/settings/folders', next);
      path = browse.path;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Can’t read that folder.';
    }
  }

  function openPicker() {
    adding = true;
    locating = null;
    candidates = [];
    dropNote = '';
    if (!browse) void go(path);
  }

  function addCandidate(next: string) {
    if (
      !library?.folders.some((folder) => folder.path === next) &&
      !candidates.some((candidate) => candidate.path === next)
    ) {
      candidates = [...candidates, { path: next, subfolders: true }];
    }
  }

  function toggleCandidate(next: string) {
    if (candidates.some((candidate) => candidate.path === next))
      candidates = candidates.filter((candidate) => candidate.path !== next);
    else addCandidate(next);
  }

  function setCandidate(next: string, subfolders: boolean) {
    candidates = candidates.map((candidate) => (candidate.path === next ? { ...candidate, subfolders } : candidate));
  }

  async function nativePick() {
    error = '';
    try {
      const r = await fetch('api/library/pick', { method: 'POST' });
      const body = (await r.json().catch(() => ({}))) as { paths?: unknown; error?: unknown };
      if (!r.ok) throw new Error(typeof body.error === 'string' ? body.error : 'Couldn’t choose folders.');
      for (const value of Array.isArray(body.paths) ? body.paths : [])
        if (typeof value === 'string') addCandidate(value);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Couldn’t choose folders.';
    }
  }

  async function save(folders: LinkedFolder[]) {
    if (!library || saving) return;
    saving = true;
    error = '';
    try {
      library = await saveLibrary(library.revision, folders);
      candidates = [];
      adding = false;
      locating = null;
      removing = '';
      changed();
    } catch (e) {
      const message = e instanceof Error ? e.message : 'Couldn’t save linked folders.';
      error = /overlap/i.test(message)
        ? `${message} Remove the broad original folder, then add its children.`
        : message;
    } finally {
      saving = false;
    }
  }

  function addFolders() {
    if (!library) return;
    void save([...library.folders, ...newCandidates.map((candidate) => ({ id: '', available: true, ...candidate }))]);
  }

  function startLocate(folder: LinkedFolder) {
    adding = false;
    locating = folder;
    path = folder.path;
    void go(folder.path);
  }

  function locateHere() {
    if (!library || !locating) return;
    void save(library.folders.map((folder) => (folder.id === locating!.id ? { ...folder, path } : folder)));
  }

  function updateSubfolders(folder: LinkedFolder, subfolders: boolean) {
    if (!library) return;
    void save(library.folders.map((item) => (item.id === folder.id ? { ...item, subfolders } : item)));
  }

  function drop(e: DragEvent) {
    e.preventDefault();
    const candidate = e.dataTransfer?.getData('application/x-retake-folder');
    if (candidate) {
      addCandidate(candidate);
      dropNote = '';
    } else {
      dropNote = library?.native_picker
        ? `Choose folders on ${computer}; browser drops cannot reveal folder paths.`
        : `Choose folders on ${computer}; Retake cannot use folders dropped from Finder here.`;
    }
  }

  onMount(() => void read());
</script>

<section
  class="library"
  data-part="linked-folders"
  aria-labelledby="linked-folders-title"
  aria-busy={loading || saving}
>
  <a class="back" href="#/">Back to videos</a>
  <header>
    <div class="heading">
      <h1 id="linked-folders-title">Linked folders</h1>
      <p class="sub">Folders on {computer}</p>
    </div>
    {#if !adding && !locating && library?.folders.length}<button class="quiet" onclick={openPicker}>Add folders</button
      >{/if}
  </header>
  <p class="intro">These folders are on the computer running Retake. Linking does not share them.</p>
  {#if error}<p class="err" role="alert">{error}</p>{/if}
  {#if !library && !loading}<button class="quiet" onclick={read}>Try again</button>{/if}

  {#if adding || locating}
    <section class="browser" aria-label={`Choose folders on ${computer}`}>
      {#if locating}
        <p>
          Locate <code>{locating.path}</code> on {computer}. Choose the folder you moved; its files and reviews stay in
          place.
        </p>
      {:else}
        <p>Retake will link these folders. Your files stay where they are.</p>
      {/if}
      <div class="browse-bar">
        <button
          class="up"
          disabled={!browse?.parent}
          onclick={() => browse && go(browse.parent)}
          aria-label="Up one folder"
          ><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 12.5v-9M4 7.5l4-4 4 4" /></svg></button
        >
        <input aria-label="Folder path" bind:value={path} onkeydown={(e) => e.key === 'Enter' && go()} />
        <button class="quiet" onclick={() => go()}>Go</button>
      </div>
      {#if locating}
        <div class="locate-confirm">
          <span>Replace <code>{locating.path}</code> with <code>{path}</code>.</span><button
            class="primary"
            disabled={saving || !browse}
            onclick={locateHere}>Locate this folder</button
          >
        </div>
      {:else}
        {#if library?.native_picker}<button class="quiet" onclick={nativePick}>Choose folders on {computer}</button
          >{/if}
        <button class="quiet" disabled={!browse} onclick={() => addCandidate(path)}>Select this folder</button>
      {/if}
      <ul class="browser-list" aria-label="Folders to browse">
        {#each browse?.folders ?? [] as name (name)}
          {@const candidate = browse ? join(browse.path, name) : name}
          {@const linked = library?.folders.some((folder) => folder.path === candidate)}
          <li
            draggable={!locating}
            ondragstart={(e) => e.dataTransfer?.setData('application/x-retake-folder', candidate)}
          >
            {#if locating}<span class="folder-name">{name}</span>{:else}<label
                ><input
                  type="checkbox"
                  disabled={linked}
                  checked={linked || candidates.some((item) => item.path === candidate)}
                  onchange={() => toggleCandidate(candidate)}
                />{name}{#if linked}<small>Already linked</small>{/if}</label
              >{/if}
            <button class="folder-open" aria-label={`Open ${name}`} onclick={() => go(candidate)}
              ><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 4.5h4l1.5 1.5H14v6.5H2z" /></svg>Open</button
            >
          </li>
        {:else}{#if browse}<li class="muted">No folders here.</li>{/if}{/each}
      </ul>
      {#if adding}
        <div class="drop" role="status" ondragover={(e) => e.preventDefault()} ondrop={drop}>
          Drag a folder from this list here to select it. {dropNote || 'External files are never uploaded.'}
        </div>
        {#if candidates.length}<div class="candidates" aria-label="Selected folders">
            {#each candidates as candidate (candidate.path)}
              <div>
                <code>{candidate.path}</code><label
                  ><input
                    type="checkbox"
                    checked={candidate.subfolders}
                    onchange={(e) => setCandidate(candidate.path, e.currentTarget.checked)}
                  /> Include subfolders</label
                ><button class="link" onclick={() => toggleCandidate(candidate.path)}>Remove</button>
              </div>
            {/each}
          </div>{/if}
      {/if}
      <div class="actions">
        <button
          class="quiet"
          disabled={saving}
          onclick={() => {
            adding = false;
            locating = null;
            candidates = [];
            error = '';
          }}>Cancel</button
        >{#if adding}<button class="primary" disabled={!newCandidates.length || saving} onclick={addFolders}
            >{saving
              ? 'Adding…'
              : newCandidates.length
                ? `Add ${newCandidates.length} folder${newCandidates.length === 1 ? '' : 's'}`
                : 'Add folders'}</button
          >{/if}
      </div>
    </section>
  {/if}

  {#if !loading && library && !library.folders.length && !adding && !locating}
    <div class="empty">
      <p>
        No folders are linked yet. Choose only folders you want Retake to scan, including their subfolders when
        selected.
      </p>
      <button class="primary" onclick={openPicker}>Add folders</button>
    </div>
  {/if}
  {#if !loading && library && library.folders.length && !adding && !locating}
    <div class="rows">
      {#each library.folders as folder (folder.id)}
        <article class="folder-row" class:offline={!folder.available}>
          <div>
            <strong
              ><svg class="folder-icon" viewBox="0 0 16 16" aria-hidden="true"
                ><path d="M2 4.5h4l1.5 1.5H14v6.5H2z" /></svg
              >{folder.path.split('/').filter(Boolean).at(-1) || folder.path}</strong
            ><code>{folder.path}</code><span>{computer} · {folder.available ? 'Available' : 'Offline'}</span><label
              class="subfolders"
              ><input
                type="checkbox"
                checked={folder.subfolders}
                disabled={saving}
                onchange={(e) => updateSubfolders(folder, e.currentTarget.checked)}
              /> Include subfolders</label
            >
          </div>
          <div class="row-actions">
            {#if !folder.available}<button class="quiet" onclick={() => startLocate(folder)}>Locate folder</button>{/if}
            {#if removing === folder.id}<span class="confirm"
                >Files stay on your computer. <button class="link" onclick={() => (removing = '')}>Cancel</button
                ><button
                  class="danger"
                  disabled={saving}
                  onclick={() => save(library!.folders.filter((item) => item.id !== folder.id))}
                  >Remove from Retake</button
                ></span
              >{:else}<button class="link" onclick={() => (removing = folder.id)}>Remove from Retake</button>{/if}
          </div>
        </article>
      {/each}
    </div>
  {/if}
</section>

<style>
  @layer retake {
    .library {
      max-width: 720px;
    }
    .back {
      display: inline-block;
      margin: 0 0 12px;
      color: var(--text-3);
      font-size: 12.5px;
      text-decoration: none;
    }
    .back:hover {
      color: var(--text);
    }
    .library header {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      margin-bottom: 8px;
    }
    .intro,
    .muted {
      color: var(--muted);
      line-height: 1.5;
    }
    .err {
      color: var(--danger);
    }
    .quiet,
    .primary,
    .link,
    .danger,
    .folder-open,
    .up {
      font: inherit;
      cursor: pointer;
    }
    .quiet,
    .primary {
      min-height: 32px;
      padding: 0 12px;
      border-radius: var(--radius);
      font-weight: 600;
    }
    .quiet {
      border: 1px solid var(--border);
      background: transparent;
      color: var(--text-3);
    }
    .quiet:hover:not(:disabled),
    .up:hover:not(:disabled) {
      border-color: var(--border-hover);
      color: var(--text);
    }
    .primary {
      border: 0;
      background: var(--accent);
      color: var(--on-accent);
    }
    .primary:hover:not(:disabled) {
      background: var(--accent-hover);
    }
    button:disabled {
      cursor: default;
      opacity: 0.55;
    }
    input[type='checkbox'] {
      width: 16px;
      height: 16px;
      min-width: 16px;
      min-height: 16px;
      flex: none;
      margin: 0;
      accent-color: var(--accent);
    }
    .candidates label {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .browser-list label,
    .candidates label,
    .subfolders {
      min-height: 32px;
      cursor: pointer;
    }
    .browser-list label,
    .folder-name,
    .folder-row strong {
      overflow-wrap: anywhere;
    }
    button:focus-visible,
    input:focus-visible,
    .back:focus-visible {
      outline: 2px solid var(--focus);
      outline-offset: 2px;
    }
    .browser,
    .empty {
      display: grid;
      gap: 10px;
      margin-top: 16px;
      padding: 12px;
      border: 1px solid var(--line-strong);
      border-radius: var(--radius);
      background: var(--surface-sunken);
    }
    .browser p {
      margin: 0;
      color: var(--text-3);
    }
    .browser {
      max-width: 640px;
      box-sizing: border-box;
    }
    .browse-bar {
      display: flex;
      gap: 8px;
    }
    .browse-bar input {
      min-width: 0;
      flex: 1;
      padding: 7px 9px;
      border: 1px solid var(--border);
      border-radius: var(--radius);
      background: var(--surface-control);
      color: inherit;
      font: 12px var(--font-mono);
    }
    .up {
      display: grid;
      place-items: center;
      width: 32px;
      border: 1px solid var(--border);
      border-radius: var(--radius);
      background: transparent;
      color: var(--text-3);
    }
    .up svg,
    .folder-open svg,
    .folder-icon {
      width: 16px;
      height: 16px;
      fill: none;
      stroke: currentColor;
      stroke-width: 1.4;
      stroke-linecap: round;
      stroke-linejoin: round;
    }
    .folder-icon {
      margin-right: 8px;
      vertical-align: middle;
      color: var(--muted);
    }
    .browser-list {
      max-height: 220px;
      margin: 0;
      padding: 4px;
      overflow: auto;
      list-style: none;
      border: 1px solid var(--line);
      border-radius: var(--radius);
    }
    .browser-list li {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
      min-height: 34px;
      padding: 0 6px;
    }
    .browser-list label {
      display: flex;
      min-width: 0;
      gap: 8px;
      align-items: center;
    }
    small {
      color: var(--muted);
    }
    .folder-name {
      color: var(--text-3);
    }
    .folder-open,
    .link,
    .danger {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      border: 0;
      background: transparent;
      padding: 5px;
      color: var(--text-3);
    }
    .folder-open:hover,
    .link:hover {
      color: var(--text);
    }
    .danger {
      color: var(--danger);
    }
    .drop {
      padding: 10px;
      border: 1px dashed var(--line-strong);
      border-radius: var(--radius);
      color: var(--muted);
      font-size: 12.5px;
    }
    .candidates {
      display: grid;
      gap: 6px;
    }
    .candidates > div,
    .locate-confirm {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      align-items: center;
      padding: 8px;
      border-radius: var(--radius-sm);
      background: var(--surface-control);
    }
    code {
      overflow-wrap: anywhere;
      font: 12px var(--font-mono);
      color: var(--text-3);
    }
    .candidates code {
      flex: 1 1 14rem;
    }
    .actions,
    .row-actions {
      display: flex;
      flex-wrap: wrap;
      justify-content: flex-end;
      gap: 8px;
      align-items: center;
    }
    .rows {
      margin-top: 16px;
      border-top: 1px solid var(--line);
    }
    .folder-row {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      padding: 12px 0;
      border-bottom: 1px solid var(--line);
    }
    .folder-row > div:first-child {
      display: grid;
      gap: 3px;
      min-width: 0;
    }
    .folder-row span {
      color: var(--muted);
      font-size: 12.5px;
    }
    .folder-row.offline span {
      color: var(--warn);
    }
    .subfolders {
      display: flex;
      gap: 7px;
      align-items: center;
      color: var(--text-3);
      font-size: 12.5px;
    }
    .confirm {
      max-width: 260px;
      color: var(--text-3) !important;
    }
    @media (max-width: 760px) {
      .quiet,
      .primary,
      .up,
      .folder-open,
      .link,
      .danger {
        min-height: 44px;
      }
      .browser-list label,
      .candidates label,
      .subfolders {
        min-height: 44px;
      }
      .folder-row {
        display: grid;
      }
      .row-actions {
        justify-content: flex-start;
      }
      .browse-bar {
        align-items: stretch;
      }
    }
  }
</style>
