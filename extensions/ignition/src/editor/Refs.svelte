<script lang="ts">
  // refs/: what you collect for the video. Drop files to add them, click to open (images inline, others download);
  // refs/links.md is a document like the others.
  import { untrack } from 'svelte';
  import type { FileInfo, Files } from '../types';
  import { isDoc, isImage, MAX_REF, nthName, refName } from '../lib/files';
  import { isChanged, sayError } from '../lib/errors';
  import { size } from '../lib/stamp';

  let {
    files,
    rev,
    readonly,
    toast,
    onopen,
  }: {
    files: Files;
    /** Bumped when refs/ changed on disk. */
    rev: number;
    readonly: boolean;
    toast: (text: string) => void;
    onopen: (path: string) => void;
  } = $props();

  const LINKS = 'v0/refs/links.md';
  let list = $state<FileInfo[]>([]);
  let loaded = $state(false);
  let error = $state('');
  let shown = $state<string | null>(null);
  let adding = $state(0);
  let over = $state(0);
  let input = $state<HTMLInputElement>();

  async function load(): Promise<void> {
    try {
      list = (await files.list('v0/refs/**'))
        .filter((f) => !f.deleted && f.path.startsWith('v0/refs/'))
        .sort((a, b) => a.path.localeCompare(b.path));
      error = '';
    } catch (e) {
      error = sayError(e);
    }
    loaded = true;
  }

  $effect(() => {
    void rev;
    untrack(() => void load());
  });

  const rows = $derived([
    ...(list.some((f) => f.path === LINKS) ? [] : [{ path: LINKS, size: -1, modified: 0, sha256: '' }]),
    ...list,
  ]);
  const nameOf = (path: string): string => path.slice('v0/refs/'.length);

  async function put(name: string, f: File): Promise<void> {
    for (let n = 1; ; n++) {
      try {
        await files.write(`v0/refs/${nthName(name, n)}`, f, { ifMatch: '*' });
        return;
      } catch (e) {
        if (!isChanged(e) || n >= 50) throw e;
      }
    }
  }

  async function add(fs: Iterable<File>): Promise<void> {
    for (const f of fs) {
      if (f.size > MAX_REF) {
        toast(`${f.name} is over 25 MiB`);
        continue;
      }
      const name = refName(f.name);
      if (!name) continue;
      adding++;
      try {
        await put(name, f);
      } catch (e) {
        toast(`${f.name} wasn't added: ${sayError(e)}`);
      } finally {
        adding--;
      }
    }
    await load();
  }

  const dragging = (e: DragEvent): boolean => !readonly && !!e.dataTransfer?.types.includes('Files');
</script>

<div
  class={['ig-refs', over > 0 && 'ig-over']}
  data-part="ignition-refs"
  role="region"
  aria-label="Refs"
  ondragenter={(e) => {
    if (dragging(e)) {
      e.preventDefault();
      over++;
    }
  }}
  ondragover={(e) => {
    if (dragging(e)) e.preventDefault();
  }}
  ondragleave={() => (over = Math.max(0, over - 1))}
  ondrop={(e) => {
    over = 0;
    if (!dragging(e)) return;
    e.preventDefault();
    void add(e.dataTransfer?.files ?? []);
  }}
>
  <div class="ig-col">
    {#if !readonly}
      <p class="ig-refs-head">
        <span>{adding ? 'Adding…' : 'Drop files here to add them, 25 MiB each.'}</span>
        <button class="ig-link" onclick={() => input?.click()}>Add files</button>
        <input
          bind:this={input}
          type="file"
          multiple
          hidden
          onchange={() => {
            void add([...(input?.files ?? [])]);
            if (input) input.value = '';
          }}
        />
      </p>
    {/if}
    {#if error}<p class="ig-err">{error}</p>{/if}
    {#if loaded}
      <ul class="ig-files">
        {#each rows as f (f.path)}
          <li class="ig-file" data-part="ignition-ref">
            <div class="ig-file-line">
              {#if isDoc(f.path)}
                <button class="ig-file-name" onclick={() => onopen(f.path)}>{nameOf(f.path)}</button>
              {:else if isImage(f.path)}
                <button class="ig-file-name" onclick={() => (shown = shown === f.path ? null : f.path)}
                  >{nameOf(f.path)}</button
                >
              {:else}
                <a class="ig-file-name" href={files.url(f.path)} download={nameOf(f.path)}>{nameOf(f.path)}</a>
              {/if}
              <span class="ig-file-size">{f.size < 0 ? 'for links' : size(f.size)}</span>
            </div>
            {#if shown === f.path}
              <img class="ig-file-img" src={files.url(f.path)} alt={nameOf(f.path)} />
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
