<script lang="ts">
  // The ideas page: New idea, then each idea (a video with only v0): its title, its project, when it was last edited
  // and its first two lines. Clicking one opens it.
  import { onMount } from 'svelte';
  import type { Card, Ctx } from '../types';
  import { firstLines, titleOf } from '../lib/doc';
  import { ideasOf, projectsOf, type Project } from '../lib/ideas';
  import { sayError } from '../lib/errors';
  import { ago } from '../lib/stamp';
  import { folderOf, OLD, pickDoc } from '../lib/v0';

  let { ctx }: { ctx: Ctx } = $props();

  interface Row {
    card: Card;
    title: string;
    lines: string[];
  }

  let rows = $state<Row[]>([]);
  let projects = $state.raw<Project[]>([]);
  let loaded = $state(false);
  let error = $state('');
  let now = $state(Date.now());

  const projectOf = (path: string): string => projects.find((p) => p.path === path)?.label || path || 'This folder';

  async function load(): Promise<void> {
    try {
      const home = await ctx.home();
      projects = projectsOf(home);
      rows = ideasOf(home, `${ctx.ext.id}.doc`).map((card) => ({ card, title: card.title, lines: [] }));
      error = '';
    } catch (e) {
      error = sayError(e);
    }
    now = Date.now();
    loaded = true;
    // Each idea's title and first lines, a few at a time.
    let next = 0;
    const work = async () => {
      while (next < rows.length) {
        const r = rows[next++]!;
        try {
          const f = ctx.files(r.card.name);
          const paths = (await f.list('v0/*')).map((x) => x.path);
          const doc = pickDoc(paths, folderOf(r.card)) ?? (paths.includes(OLD.idea) ? OLD.idea : null);
          if (!doc) continue;
          const md = await (await f.read(doc)).text();
          r.title = titleOf(md) || r.card.title;
          r.lines = firstLines(md, 2);
        } catch {
          /* the folder's name stands */
        }
      }
    };
    await Promise.all(Array.from({ length: 4 }, work));
  }

  onMount(() => {
    void load();
    const t = setInterval(() => (now = Date.now()), 60_000);
    return () => clearInterval(t);
  });

  function open(e: MouseEvent, card: Card): void {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    ctx.navigate({ video: card.name });
  }
</script>

<div class="ig-ideas" data-part="scripts-ideas">
  <div class="ig-head">
    <h1 class="ig-h1">{ctx.ext.name}</h1>
    <button
      class="ig-primary"
      data-part="scripts-new-idea"
      disabled={ctx.role !== 'owner'}
      title={ctx.role !== 'owner' ? 'Only the owner starts new scripts' : undefined}
      onclick={() => ctx.navigate({ route: 'new' })}>New script</button
    >
  </div>
  {#if error}
    <p class="ig-err">{error}</p>
  {:else if loaded && !rows.length}
    <p class="ig-empty">No scripts yet. New script starts one: give it a title, then talk it out.</p>
  {/if}
  <ul class="ig-list">
    {#each rows as r (r.card.name)}
      <li>
        <a class="ig-idea" data-part="scripts-idea" href={r.card.url ?? '#'} onclick={(e) => open(e, r.card)}>
          <span class="ig-idea-title">{r.title}</span>
          <span class="ig-idea-meta"
            >{projectOf(r.card.project)}{#if r.card.changed}<span class="ig-sep">·</span>{ago(
                r.card.changed,
                now,
              )}{/if}</span
          >
          {#each r.lines as l, i (i)}
            <span class="ig-idea-line">{l}</span>
          {/each}
        </a>
      </li>
    {/each}
  </ul>
</div>
