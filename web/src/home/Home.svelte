<script lang="ts">
  // The home: your videos. With one project it opens straight to them; with more, a Projects page first. Organizing
  // (new project, new group, rename, move) lives in ⋯ menus, so the page is thumbnails, names and one status each.
  // An owner with no computer connected (hosted, C5) gets "Connect your computer" at the top.
  import { DropdownMenu } from 'bits-ui';
  import { needsComputer, toReview, workspaceUrl, type Card } from '../lib/home';
  import { connectCommand } from '../lib/automation.svelte';
  import type { Home } from './home.svelte';
  import VideoCard from './VideoCard.svelte';
  import ConnectBlock from '../components/ConnectBlock.svelte';
  import { brandOf, homeTitle } from '../lib/brand';

  let { home }: { home: Home } = $props();
  let search = $state('');
  let form = $state<'project' | 'group' | 'rename' | ''>('');
  let name = $state('');
  let renameGroup = $state('');
  let command = $state('');
  let asking = $state(false);
  const project = $derived(home.project);
  const owner = $derived(home.res.role === 'owner' && !!home.res.workspace);
  const videos = $derived(project ? home.videos(project.id) : home.res.projects);
  const todo = $derived(toReview(videos));
  const title = $derived(project?.title || 'Projects');
  const searchable = $derived(project ? videos.length > 8 : home.workspace.projects.length > 8);
  const matches = (video: Card): boolean => video.title.toLowerCase().includes(search.toLowerCase());
  const visibleProjects = $derived(
    home.workspace.projects.filter(
      (p) => !search || p.title.toLowerCase().includes(search.toLowerCase()) || home.videos(p.id).some(matches),
    ),
  );
  const sections = $derived(
    project ? [{ id: '', title: project.groups.length ? 'Ungrouped' : '' }, ...project.groups] : [],
  );
  const connect = $derived(needsComputer(home.res));
  const user = $derived(home.res.user);
  // The theme's name and logo (spec/theme.md), or Retake.
  const brand = brandOf();

  function startForm(kind: typeof form, title = '', group = '') {
    form = kind;
    name = title;
    renameGroup = group;
  }

  async function showCommand() {
    asking = true;
    command = await connectCommand('api/connect');
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    const title = name.trim();
    if (!title || home.saving) return;
    const kind = form,
      group = renameGroup,
      current = project?.id;
    const id = crypto.randomUUID();
    const ok = await home.save((layout) => {
      if (kind === 'project') layout.projects.push({ id, title, groups: [] });
      else {
        const p = layout.projects.find((p) => p.id === current);
        if (!p) return;
        if (kind === 'group') p.groups.push({ id, title });
        else if (group) {
          const g = p.groups.find((g) => g.id === group);
          if (g) g.title = title;
        } else p.title = title;
      }
    });
    if (ok) {
      form = '';
      name = '';
    }
  }

  $effect(() => {
    document.title = homeTitle(title, brand);
  });
</script>

{#snippet more(label: string)}
  <DropdownMenu.Trigger class="more" data-part="more" aria-label={label} title="More">
    <svg viewBox="0 0 20 20" aria-hidden="true"
      ><circle cx="4.5" cy="10" r="1.5" /><circle cx="10" cy="10" r="1.5" /><circle cx="15.5" cy="10" r="1.5" /></svg
    >
  </DropdownMenu.Trigger>
{/snippet}

<main class="home" data-part="home">
  <div class="top">
    <nav class="breadcrumbs" aria-label="Breadcrumb">
      <a class="brand" class:with-logo={!!brand.logo} data-part="brand" href="./"
        >{#if brand.logo}<img data-part="logo" src={brand.logo} alt="" />{/if}<span data-part="brand-name"
          >{brand.name}{#if !brand.custom && !brand.logo}<span
              class="wordmark-dot"
              data-part="wordmark-dot"
              aria-hidden="true">.</span
            >{/if}</span
        ></a
      >
      {#if home.selected && !home.single}<span aria-hidden="true">/</span><a href="./">Projects</a>{/if}
    </nav>
    {#if user}
      <div class="account" data-part="account">
        <span class="who" title={user.email}>{user.name || user.email}</span>
        <a href="/signout">Sign out</a>
      </div>
    {/if}
  </div>
  <header>
    <div class="heading">
      <h1>{title}</h1>
      <p class="sub" id="todo">
        {#if !home.loaded}Loading…{:else if project}{videos.length}
          {videos.length === 1 ? 'video' : 'videos'} · {todo ? `${todo} to review` : 'all reviewed'}{:else}{home
            .workspace.projects.length}
          {home.workspace.projects.length === 1 ? 'project' : 'projects'} · {home.res.projects.length}
          {home.res.projects.length === 1 ? 'video' : 'videos'}{/if}
      </p>
    </div>
    {#if owner && home.loaded && (project || !home.selected)}
      <DropdownMenu.Root>
        {@render more(project ? 'Project options' : 'Options')}
        <DropdownMenu.Portal>
          <DropdownMenu.Content class="menu" data-part="menu" align="end" sideOffset={6} collisionPadding={12}>
            {#if project}
              <DropdownMenu.Item class="menu-item" onSelect={() => startForm('group')}>New group</DropdownMenu.Item>
              <DropdownMenu.Item class="menu-item" onSelect={() => startForm('rename', project.title)}
                >Rename project</DropdownMenu.Item
              >
              <DropdownMenu.Separator class="menu-sep" />
            {/if}
            <DropdownMenu.Item class="menu-item" onSelect={() => startForm('project')}>New project</DropdownMenu.Item>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    {/if}
  </header>

  {#if home.error}<p class="err" role="alert">{home.error}</p>{/if}
  {#if home.saveError}<p class="err" role="alert">{home.saveError}</p>{/if}

  {#if form}
    <form class="name-form" onsubmit={submit}>
      <label for="organization-name"
        >{form === 'group'
          ? 'Group name'
          : form === 'project'
            ? 'Project name'
            : renameGroup
              ? 'Group name'
              : 'Project name'}</label
      >
      <!-- svelte-ignore a11y_autofocus -->
      <input
        id="organization-name"
        bind:value={name}
        required
        maxlength="160"
        autofocus
        placeholder={form === 'group' ? 'e.g. Shorts' : 'e.g. Product launch'}
      />
      <button class="primary" disabled={home.saving || !name.trim()}
        >{home.saving ? 'Saving…' : form === 'rename' ? 'Save name' : 'Create'}</button
      >
      <button
        type="button"
        onclick={() => {
          form = '';
        }}
        disabled={home.saving}>Cancel</button
      >
    </form>
  {/if}

  {#if connect}
    <section class="connect-card" data-part="connect-card" aria-label="Connect your computer">
      {#if asking}
        <ConnectBlock {command} />
      {:else}
        <div>
          <strong>Connect your computer</strong>
          <p>The agent edits on the computer that has your footage. Connect it once, and comments you send go there.</p>
        </div>
        <button class="primary" onclick={showCommand}>Show the command</button>
      {/if}
    </section>
  {/if}

  {#if home.loaded && searchable && (!home.selected || project)}
    <div class="filters">
      <input
        type="search"
        aria-label={project ? 'Search videos' : 'Search projects'}
        placeholder={project ? 'Search videos…' : 'Search projects…'}
        bind:value={search}
      />
    </div>
  {/if}

  {#if home.loaded && home.selected && !project}
    <p class="empty">This project is no longer here. <a href="./">See all projects</a>.</p>
  {:else if home.loaded && project}
    {#each sections as section (section.id)}
      {@const groupVideos = home.videos(project.id, section.id).filter(matches)}
      {#if groupVideos.length || (!search && section.id)}
        <section class="video-group" aria-label={section.title || 'Videos'}>
          {#if section.title}
            <div class="group-heading">
              <h2>{section.title} <span>{groupVideos.length}</span></h2>
              {#if owner && section.id}
                <DropdownMenu.Root>
                  {@render more(`${section.title} options`)}
                  <DropdownMenu.Portal>
                    <DropdownMenu.Content
                      class="menu"
                      data-part="menu"
                      align="start"
                      sideOffset={6}
                      collisionPadding={12}
                    >
                      <DropdownMenu.Item
                        class="menu-item"
                        onSelect={() => startForm('rename', section.title, section.id)}>Rename group</DropdownMenu.Item
                      >
                    </DropdownMenu.Content>
                  </DropdownMenu.Portal>
                </DropdownMenu.Root>
              {/if}
            </div>
          {/if}
          {#if groupVideos.length}
            <ul class="grid" data-part="home-grid">
              {#each groupVideos as video (video.name)}<VideoCard {video} {home} />{/each}
            </ul>
          {:else}<p class="empty">Nothing here yet. Use ⋯ on a video to move it here.</p>{/if}
        </section>
      {/if}
    {/each}
    {#if !videos.length}
      <p class="empty">No videos yet. Put a versioned video (like <code>clip_v1.mp4</code>) in this folder.</p>
    {:else if search && !videos.some(matches)}<p class="empty">No videos match “{search}”.</p>{/if}
  {:else if home.loaded}
    <ul class="grid project-grid" data-part="project-grid">
      {#each visibleProjects as p (p.id)}
        {@const projectVideos = home.videos(p.id)}
        <li>
          <a class="project-card" data-part="project-card" href={workspaceUrl('./', p.id)} data-project={p.id}>
            <div class="project-cover" aria-hidden="true">
              {#each projectVideos.slice(0, 3) as video (video.name)}
                <div>
                  {#if video.thumb && !home.broken.has(video.name)}<img
                      src={video.thumb}
                      alt=""
                      decoding="async"
                      onerror={() => home.thumbFailed(video.name)}
                    />{/if}
                </div>
              {/each}
              {#if !projectVideos.length}<svg viewBox="0 0 48 48"
                  ><path d="M5 12h15l4 5h19v23H5z" fill="none" stroke="currentColor" stroke-width="2" /></svg
                >{/if}
            </div>
            <div class="project-meta">
              <h2>{p.title}</h2>
              <span class="sub"
                >{projectVideos.length}
                {projectVideos.length === 1 ? 'video' : 'videos'}{toReview(projectVideos)
                  ? ` · ${toReview(projectVideos)} to review`
                  : ''}</span
              >
            </div>
          </a>
        </li>
      {/each}
    </ul>
    {#if !visibleProjects.length}<p class="empty">No projects match “{search}”.</p>{/if}
  {/if}
</main>
