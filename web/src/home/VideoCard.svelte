<script lang="ts">
  import { fmtLength, STATUS_TEXT, streamText, placementOf, type Card } from '../lib/home';
  import type { Home } from './home.svelte';
  import StyledSelect from '../components/ui/Select.svelte';
  let { video, home }: { video: Card; home: Home } = $props();
  const placement = $derived(placementOf(home.workspace, video.name));
  const stream = $derived(streamText(video.stream));
</script>

<li class="video-card">
  <a class="card" href={video.url} data-name={video.name} data-status={video.status}>
    <div class="thumb">
      {#if video.thumb && !home.broken.has(video.name)}
        <img src={video.thumb} alt="" decoding="async" onerror={() => home.thumbFailed(video.name)} />
      {/if}
      {#if fmtLength(video.duration)}<span class="len">{fmtLength(video.duration)}</span>{/if}
    </div>
    <div class="meta">
      <span class="title">{video.title}</span>
      <span class="row">
        <span class="ver">{video.version}</span>
        <span class="chip {video.status}">{STATUS_TEXT[video.status]}</span>
        {#if stream}<span class="chip stream">{stream}</span>{/if}
      </span>
    </div>
  </a>
  {#if home.res.role === 'owner' && home.res.workspace}
    <div class="move-video">
      <span>Move to</span>
      <StyledSelect
        label={`Move ${video.title}`}
        value={`${placement.project}/${placement.group}`}
        disabled={home.saving}
        options={home.workspace.projects.flatMap((project) => [
          { value: `${project.id}/`, label: `${project.title} · Ungrouped` },
          ...project.groups.map((group) => ({
            value: `${project.id}/${group.id}`,
            label: `${project.title} · ${group.title}`,
          })),
        ])}
        onValueChange={(value) => home.move(video.name, value)}
      />
    </div>
  {/if}
</li>
