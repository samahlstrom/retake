<script lang="ts">
  // A video: its first frame, name, version and one status; in a folder's view, the project it's in. The version reads
  // as the page names it everywhere (`verName`: the live v6's revision `v6-draft11` is `v6`, never "draft"), with a
  // small red dot while the agent works on it. A video whose only version is an extension's (an idea) shows its
  // document in place of a frame (ScriptThumb) and its kind's word ("Idea") as its chip, no dot.
  import { cardStatus, fmtLength, videoName, type Card } from '../lib/home';
  import { verName } from '../lib/versions';
  import { kindStatus } from '../ext/kinds';
  import type { Home } from './home.svelte';
  import ScriptThumb from './ScriptThumb.svelte';
  let { video, home, where = '' }: { video: Card; home: Home; where?: string } = $props();
  const status = $derived(cardStatus(video));
  const kindWord = $derived(kindStatus(home.exts, video));
</script>

<li class="video-card" data-part="video-card" data-status={video.status}>
  <a class="card" href={video.url} data-name={video.name} data-status={video.status}>
    <div class="thumb">
      {#if kindWord}
        <ScriptThumb {video} exts={home.exts} />
      {:else if video.thumb && !home.broken.has(video.name)}
        <img src={video.thumb} alt="" decoding="async" onerror={() => home.thumbFailed(video.name)} />
      {/if}
      {#if fmtLength(video.duration)}<span class="len">{fmtLength(video.duration)}</span>{/if}
    </div>
    <div class="meta">
      <span class="title">{videoName(video)}</span>
      {#if where}<span class="where">{where}</span>{/if}
      <span class="row">
        <span class="ver"
          >{verName(video.version)}{#if video.live}<span
              class="live"
              title="The agent is working on it"
              aria-label="live"
            ></span>{/if}</span
        >
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
    .ver .live {
      display: inline-block;
      width: 6px;
      height: 6px;
      margin-left: 6px;
      border-radius: 50%;
      background: var(--record);
      vertical-align: 2px;
    }
    .where {
      color: var(--muted);
      font-size: 12.5px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }
</style>
