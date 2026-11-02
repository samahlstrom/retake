<script lang="ts">
  // A video: its first frame, name, version and one status; in a folder's view, the project it's in. A video whose only
  // version is an extension's (an idea) has an empty frame and its kind's word ("Idea") as its chip, no dot.
  import { cardStatus, fmtLength, videoName, type Card } from '../lib/home';
  import { kindStatus } from '../ext/kinds';
  import type { Home } from './home.svelte';
  let { video, home, where = '' }: { video: Card; home: Home; where?: string } = $props();
  const status = $derived(cardStatus(video));
  const kindWord = $derived(kindStatus(home.exts, video));
</script>

<li class="video-card" data-part="video-card" data-status={video.status}>
  <a class="card" href={video.url} data-name={video.name} data-status={video.status}>
    <div class="thumb">
      {#if video.thumb && !home.broken.has(video.name) && !kindWord}
        <img src={video.thumb} alt="" decoding="async" onerror={() => home.thumbFailed(video.name)} />
      {/if}
      {#if fmtLength(video.duration)}<span class="len">{fmtLength(video.duration)}</span>{/if}
    </div>
    <div class="meta">
      <span class="title">{videoName(video)}</span>
      {#if where}<span class="where">{where}</span>{/if}
      <span class="row">
        <span class="ver">{video.version}</span>
        {#if kindWord}
          <span class="chip {status.tone}" data-part="status">{kindWord}</span>
        {:else}
          <span class="chip {status.tone}" data-part="status"
            ><span class="dot" aria-hidden="true"></span>{status.text}</span
          >
        {/if}
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
