<script lang="ts">
  // A video: its first frame, name, version and one status; in a folder's view, the project it's in.
  import { cardStatus, fmtLength, videoName, type Card } from '../lib/home';
  import type { Home } from './home.svelte';
  let { video, home, where = '' }: { video: Card; home: Home; where?: string } = $props();
  const status = $derived(cardStatus(video));
</script>

<li class="video-card" data-part="video-card" data-status={video.status}>
  <a class="card" href={video.url} data-name={video.name} data-status={video.status}>
    <div class="thumb">
      {#if video.thumb && !home.broken.has(video.name)}
        <img src={video.thumb} alt="" decoding="async" onerror={() => home.thumbFailed(video.name)} />
      {/if}
      {#if fmtLength(video.duration)}<span class="len">{fmtLength(video.duration)}</span>{/if}
    </div>
    <div class="meta">
      <span class="title">{videoName(video)}</span>
      {#if where}<span class="where">{where}</span>{/if}
      <span class="row">
        <span class="ver">{video.version}</span>
        <span class="chip {status.tone}" data-part="status"
          ><span class="dot" aria-hidden="true"></span>{status.text}</span
        >
      </span>
    </div>
  </a>
</li>

<style>
  @layer retake {
    .where {
      color: var(--muted);
      font-size: 12.5px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }
</style>
