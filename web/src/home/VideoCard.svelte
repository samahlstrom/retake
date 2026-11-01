<script lang="ts">
  // A video: its first frame, name, version and one status. Moving it is in its ⋯ (owners, when there's somewhere to
  // move it to).
  import { DropdownMenu } from 'bits-ui';
  import { cardStatus, fmtLength, moveTargets, type Card } from '../lib/home';
  import type { Home } from './home.svelte';
  let { video, home }: { video: Card; home: Home } = $props();
  const status = $derived(cardStatus(video));
  const targets = $derived(
    home.res.role === 'owner' && home.res.workspace ? moveTargets(home.workspace, video.name) : [],
  );
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
      <span class="title">{video.title}</span>
      <span class="row">
        <span class="ver">{video.version}</span>
        <span class="chip {status.tone}" data-part="status"
          ><span class="dot" aria-hidden="true"></span>{status.text}</span
        >
      </span>
    </div>
  </a>
  {#if targets.length}
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        class="more card-more"
        data-part="more"
        aria-label={`Options for ${video.title}`}
        title="More"
      >
        <svg viewBox="0 0 20 20" aria-hidden="true"
          ><circle cx="4.5" cy="10" r="1.5" /><circle cx="10" cy="10" r="1.5" /><circle
            cx="15.5"
            cy="10"
            r="1.5"
          /></svg
        >
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content class="menu" data-part="menu" align="end" sideOffset={6} collisionPadding={12}>
          <DropdownMenu.Sub>
            <DropdownMenu.SubTrigger class="menu-item sub" aria-label={`Move ${video.title}`}
              >Move to</DropdownMenu.SubTrigger
            >
            <DropdownMenu.SubContent class="menu menu-scroll" data-part="menu" sideOffset={6} collisionPadding={12}>
              {#each targets as t (t.value)}
                <DropdownMenu.Item
                  class="menu-item"
                  disabled={home.saving}
                  onSelect={() => home.move(video.name, t.value)}>{t.label}</DropdownMenu.Item
                >
              {/each}
            </DropdownMenu.SubContent>
          </DropdownMenu.Sub>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  {/if}
</li>
