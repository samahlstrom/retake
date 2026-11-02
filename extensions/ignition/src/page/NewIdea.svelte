<script lang="ts">
  // New idea (#ext/ignition/new): a title and the project to put it in. Create makes <project>/<slug>/v0/idea.md and
  // opens it; Create and talk does the same and starts recording there.
  import { onMount } from 'svelte';
  import type { Ctx } from '../types';
  import {
    createIdea,
    defaultProject,
    handOff,
    offline,
    projectsOf,
    rememberedSelection,
    type Project,
  } from '../lib/ideas';
  import { isOffline, OFFLINE, sayError } from '../lib/errors';
  import { storageOf } from '../lib/prefs';
  import { slug } from '../lib/slug';

  let { ctx }: { ctx: Ctx } = $props();

  let title = $state('');
  let project = $state('');
  let projects = $state.raw<Project[]>([]);
  let loaded = $state(false);
  let away = $state(false);
  let busy = $state(false);
  let error = $state('');
  let input = $state<HTMLInputElement>();

  const owner = $derived(ctx.role === 'owner');
  const ready = $derived(loaded && owner && !away && !busy && !!title.trim() && projects.length > 0);
  const where = $derived(`${project ? `${project}/` : ''}${slug(title)}/v0/idea.md`);

  onMount(() => {
    input?.focus();
    void (async () => {
      try {
        const home = await ctx.home();
        projects = projectsOf(home);
        project = defaultProject(projects, rememberedSelection(storageOf('local'), location.pathname));
        away = offline(home);
      } catch (e) {
        error = sayError(e);
        away = isOffline(e);
      }
      loaded = true;
    })();
  });

  async function create(talk: boolean): Promise<void> {
    if (!ready) return;
    busy = true;
    error = '';
    try {
      const made = await createIdea(ctx, { project, title });
      handOff(storageOf('session'), made.name, talk);
      ctx.navigate({ video: made.name });
    } catch (e) {
      if (isOffline(e)) away = true;
      else error = sayError(e);
    } finally {
      busy = false;
    }
  }
</script>

<form
  class="ig-new"
  data-part="ignition-new"
  onsubmit={(e) => {
    e.preventDefault();
    void create(false);
  }}
>
  <h1 class="ig-h1">New idea</h1>
  <label class="ig-field">
    <span>Title</span>
    <input
      bind:this={input}
      bind:value={title}
      class="ig-input"
      data-part="ignition-title"
      maxlength="200"
      autocomplete="off"
      placeholder="What's the video?"
    />
  </label>
  <label class="ig-field">
    <span>Project</span>
    <select bind:value={project} class="ig-select" data-part="ignition-project" disabled={!projects.length}>
      {#each projects as p (p.path)}
        <option value={p.path}>{p.label}</option>
      {/each}
    </select>
  </label>
  <p class="ig-where">{title.trim() ? where : ' '}</p>
  <div class="ig-actions">
    <button type="submit" class="ig-primary" data-part="ignition-create" disabled={!ready}>Create</button>
    <button
      type="button"
      class="ig-button"
      data-part="ignition-create-talk"
      disabled={!ready}
      onclick={() => void create(true)}>Create and talk</button
    >
    <button type="button" class="ig-link ig-cancel" onclick={() => ctx.navigate({ route: '' })}>Cancel</button>
  </div>
  {#if away}
    <p class="ig-warn" data-part="ignition-offline">{OFFLINE}</p>
  {:else if loaded && !owner}
    <p class="ig-warn">Only the owner starts new ideas.</p>
  {:else if loaded && !projects.length && !error}
    <p class="ig-warn">There's no project to put it in.</p>
  {/if}
  {#if error}<p class="ig-err">{error}</p>{/if}
</form>
