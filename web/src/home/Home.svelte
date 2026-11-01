<script lang="ts">
  import { STATUS_TEXT, toReview, workspaceUrl, type Card } from '../lib/home';
  import type { Home } from './home.svelte';
  import VideoCard from './VideoCard.svelte';
  import StyledSelect from '../components/ui/Select.svelte';

  let { home }: { home: Home } = $props();
  let search = $state('');
  let status = $state('');
  let form = $state<'project' | 'group' | 'rename' | ''>('');
  let name = $state('');
  let renameGroup = $state('');
  const project = $derived(home.project);
  const owner = $derived(home.res.role === 'owner' && !!home.res.workspace);
  const videos = $derived(project ? home.videos(project.id) : home.res.projects);
  const todo = $derived(toReview(videos));
  const title = $derived(project?.title || 'Projects');
  const matches = (video: Card): boolean =>
    video.title.toLowerCase().includes(search.toLowerCase()) && (!status || video.status === status);
  const visibleProjects = $derived(
    home.workspace.projects.filter(
      (p) => !search || p.title.toLowerCase().includes(search.toLowerCase()) || home.videos(p.id).some(matches),
    ),
  );
  const sections = $derived(
    project ? [{ id: '', title: project.groups.length ? 'Ungrouped' : 'Videos' }, ...project.groups] : [],
  );

  function startForm(kind: typeof form, title = '', group = '') {
    form = kind;
    name = title;
    renameGroup = group;
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
    document.title = `${title} · Retake`;
  });
</script>

<main class="home">
  <nav class="breadcrumbs" aria-label="Breadcrumb">
    <a class="brand" href="./">Retake</a>
    {#if home.selected}<span aria-hidden="true">/</span><a href="./">Projects</a><span aria-hidden="true">/</span><span
        aria-current="page">{project?.title || 'Project unavailable'}</span
      >
    {:else}<span aria-hidden="true">/</span><span aria-current="page">Projects</span>{/if}
  </nav>
  <header>
    <div class="heading">
      <h1>{title}</h1>
      <p class="sub" id="todo">
        {#if !home.loaded}Loading your projects…{:else if project}{videos.length}
          {videos.length === 1 ? 'video' : 'videos'} · {todo ? `${todo} to review` : 'All reviewed'}{:else}{home
            .workspace.projects.length}
          {home.workspace.projects.length === 1 ? 'project' : 'projects'} · {home.res.projects.length}
          {home.res.projects.length === 1 ? 'video' : 'videos'}{/if}
      </p>
    </div>
    {#if owner}
      <div class="actions">
        {#if project}
          <button onclick={() => startForm('rename', project.title)}>Rename project</button>
          <button class="primary" onclick={() => startForm('group')}>New group</button>
        {:else if !home.selected}
          <button class="primary" onclick={() => startForm('project')}>New project</button>
        {/if}
      </div>
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
      <input
        id="organization-name"
        bind:value={name}
        required
        maxlength="160"
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

  {#if home.loaded && (!home.selected || project)}
    <div class="filters">
      <input
        type="search"
        aria-label={project ? 'Search videos' : 'Search projects'}
        placeholder={project ? 'Search videos…' : 'Search projects…'}
        bind:value={search}
      />
      {#if project}<StyledSelect
          label="Review status"
          value={status}
          options={[
            { value: '', label: 'All statuses' },
            ...Object.entries(STATUS_TEXT).map(([value, label]) => ({ value, label })),
          ]}
          onValueChange={(value) => {
            status = value;
          }}
        />{/if}
    </div>
  {/if}

  {#if home.loaded && home.selected && !project}
    <p class="empty">This project is no longer available. <a href="./">Return to Projects</a>.</p>
  {:else if home.loaded && project}
    {#each sections as section (section.id)}
      {@const groupVideos = home.videos(project.id, section.id).filter(matches)}
      {#if groupVideos.length || (!search && !status)}
        <section class="video-group" aria-label={section.title}>
          <div class="group-heading">
            <h2>{section.title} <span>{groupVideos.length}</span></h2>
            {#if owner && section.id}<button
                class="quiet"
                aria-label={`Rename ${section.title}`}
                onclick={() => startForm('rename', section.title, section.id)}>Rename</button
              >{/if}
          </div>
          {#if groupVideos.length}
            <ul class="grid">
              {#each groupVideos as video (video.name)}<VideoCard {video} {home} />{/each}
            </ul>
          {:else}<p class="empty">
              {section.id
                ? 'Move videos into this group using “Move to” on a video.'
                : 'No videos here yet. Move a video here from another project.'}
            </p>{/if}
        </section>
      {/if}
    {/each}
    {#if (search || status) && !videos.some(matches)}<p class="empty">No videos match these filters.</p>{/if}
  {:else if home.loaded}
    <ul class="grid project-grid">
      {#each visibleProjects as p (p.id)}
        {@const projectVideos = home.videos(p.id)}
        <li>
          <a class="project-card" href={workspaceUrl('./', p.id)} data-project={p.id}>
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
                {projectVideos.length === 1 ? 'video' : 'videos'}{p.groups.length
                  ? ` · ${p.groups.length} ${p.groups.length === 1 ? 'group' : 'groups'}`
                  : ''}</span
              >
              <span class="project-open">Open project <span aria-hidden="true">→</span></span>
            </div>
          </a>
        </li>
      {/each}
    </ul>
    {#if !visibleProjects.length}<p class="empty">No projects match your search.</p>{/if}
  {/if}
</main>
