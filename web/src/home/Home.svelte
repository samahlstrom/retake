<script lang="ts">
  // The home: the folder tree on the left (FolderNav), and the selected folder's or project's videos as cards.
  // An owner with no computer connected (hosted, C5) gets "Connect your computer" at the top. An extension's page
  // (`#ext/<id>/…`, from its nav section) takes the cards' place.
  import { crumb, needsComputer, toReview, videoName, type Card } from '../lib/home';
  import { hashOf } from '../lib/tree';
  import { connectCommand } from '../lib/automation.svelte';
  import type { Home } from './state.svelte';
  import VideoCard from './VideoCard.svelte';
  import FolderNav from './FolderNav.svelte';
  import ConnectBlock from '../components/ConnectBlock.svelte';
  import ExtSlot from '../ext/ExtSlot.svelte';
  import Settings from './Settings.svelte';
  import VideoMenuHost from '../components/menus/VideoMenuHost.svelte';
  import { brandOf, homeTitle } from '../lib/brand';

  let { home }: { home: Home } = $props();
  let command = $state('');
  let asking = $state(false);
  const node = $derived(home.node);
  const videos = $derived(home.videos);
  const todo = $derived(toReview(videos));
  const extPage = $derived(home.extOpen);
  const title = $derived(
    home.settings
      ? 'Settings'
      : extPage
        ? (extPage.nav?.title ?? extPage.name)
        : node.path
          ? node.title
          : home.res.title || 'Videos',
  );
  const connect = $derived(needsComputer(home.res));
  const user = $derived(home.res.user);
  const navOpen = $derived(home.phone ? home.drawer : !home.hidden);
  // The theme's name and logo (spec/theme.md), or the built-in wordmark: Retake and a red period.
  const brand = brandOf();

  /** Where a card's video is, under the selected folder, when that says more than its name. */
  function where(video: Card): string {
    if (video.project === node.path) return '';
    const rel = node.path ? video.project.slice(node.path.length + 1) : video.project;
    if (!rel) return '';
    const parts = rel.split('/');
    if (parts.length === 1 && crumb(parts[0], videoName(video)).length === 1) return '';
    return parts.join(' / ');
  }

  function go(e: MouseEvent, path: string) {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
    e.preventDefault();
    home.select(path);
  }

  async function showCommand() {
    asking = true;
    command = await connectCommand('api/connect');
  }

  $effect(() => {
    document.title = homeTitle(title, brand);
  });
</script>

<div class="layout" class:nav-hidden={!home.phone && home.hidden} style:--nav-w="{home.width}px" data-part="home">
  <FolderNav {home} />
  <main class="home">
    <div class="top">
      <nav class="breadcrumbs" aria-label="Breadcrumb">
        <button
          class="navtoggle"
          data-part="nav-toggle"
          aria-label={navOpen ? 'Hide folders' : 'Show folders'}
          aria-expanded={navOpen}
          title={navOpen ? 'Hide folders' : 'Show folders'}
          onclick={() => home.toggleNav()}
          ><svg viewBox="0 0 20 20" aria-hidden="true"
            ><path
              d="M3.5 5.5h13M3.5 10h13M3.5 14.5h13"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
            /></svg
          ></button
        >
        <a class="brand" class:with-logo={!!brand.logo} data-part="brand" href="./#/" onclick={(e) => go(e, '')}
          >{#if brand.logo}<img data-part="logo" src={brand.logo} alt="" />{/if}<span data-part="brand-name"
            >{brand.name}{#if !brand.custom && !brand.logo}<span
                class="wordmark-dot"
                data-part="wordmark-dot"
                aria-hidden="true">.</span
              >{/if}</span
          ></a
        >
        {#if home.settings}
          <span aria-hidden="true">/</span><span class="here" aria-current="page">Settings</span>
        {:else if home.ext}
          {#if extPage}<span aria-hidden="true">/</span><span class="here" aria-current="page">{title}</span>{/if}
        {:else}
          {#each home.trail.slice(1) as n (n.path)}
            <span aria-hidden="true">/</span>
            {#if n.path === node.path}<span class="here" aria-current="page">{n.title}</span>{:else}<a
                href={`./${hashOf(n.path)}`}
                onclick={(e) => go(e, n.path)}>{n.title}</a
              >{/if}
          {/each}
        {/if}
      </nav>
      {#if user}
        <div class="account" data-part="account">
          <span class="who" title={user.email}>{user.name || user.email}</span>
          <a href="/signout">Sign out</a>
        </div>
      {/if}
    </div>
    {#if home.settings}
      <Settings />
    {:else if home.ext}
      {#if extPage}
        <ExtSlot
          ext={extPage}
          slot="page"
          host={home.extHost}
          loader={home.loader}
          key={extPage.id}
          class="page-slot"
        />
      {:else if home.extsLoaded}
        <p class="empty">That page is an extension’s, and it isn’t on here.</p>
      {/if}
    {:else}
      <header>
        <div class="heading">
          <h1>{title}</h1>
          <p class="sub" id="todo">
            {#if !home.loaded}Loading…{:else}{videos.length}
              {videos.length === 1 ? 'video' : 'videos'} · {todo ? `${todo} to review` : 'all reviewed'}{/if}
          </p>
        </div>
      </header>

      {#if home.error}<p class="err" role="alert">{home.error}</p>{/if}

      {#if connect}
        <section class="connect-card" data-part="connect-card" aria-label="Connect your computer">
          {#if asking}
            <ConnectBlock {command} />
          {:else}
            <div>
              <strong>Connect your computer</strong>
              <p>
                The agent edits on the computer that has your footage. Connect it once, and comments you send go there.
              </p>
            </div>
            <button class="primary" onclick={showCommand}>Show the command</button>
          {/if}
        </section>
      {/if}

      {#if home.loaded}
        {#if videos.length}
          <ul class="grid" data-part="home-grid">
            {#each videos as video (video.name)}<VideoCard {video} {home} where={where(video)} />{/each}
          </ul>
        {:else if node.project}
          <p class="empty">
            No videos in this project yet. Its first version shows up here (a file named like <code>clip_v1.mp4</code>).
          </p>
        {:else}
          <p class="empty">
            No videos here yet. A video is a file named like <code>clip_v1.mp4</code>, in this folder or one of its
            folders.
          </p>
        {/if}
      {/if}
    {/if}
  </main>
  <div class="toast" data-part="toast" class:show={home.toast.show} role="status">{home.toast.msg}</div>
  <VideoMenuHost say={(msg) => home.toast.say(msg, 3000)} onchanged={() => void home.refresh()} />
</div>

<style>
  @layer retake {
    .layout {
      display: grid;
      grid-template-columns: var(--nav-w, 264px) minmax(0, 1fr);
      min-height: 100vh;
    }
    .layout.nav-hidden {
      grid-template-columns: minmax(0, 1fr);
    }
    .layout > main {
      width: 100%;
      min-width: 0;
    }
    /* ☰: a glyph, no box. */
    .navtoggle {
      display: grid;
      place-items: center;
      width: 28px;
      height: 28px;
      min-height: 0;
      margin: 0 2px 0 -6px;
      padding: 0;
      border: 0;
      border-radius: var(--radius-sm);
      background: none;
      color: var(--muted);
      cursor: pointer;
    }
    .navtoggle:hover,
    .navtoggle:focus-visible {
      color: var(--text);
    }
    .navtoggle svg {
      width: 20px;
      height: 20px;
    }
    .here {
      color: var(--text);
    }
    /* An extension's page, where the cards go. */
    .home :global(.page-slot) {
      min-height: 60vh;
    }
    /* An extension's short message (ctx.toast), at the top of the page. */
    .toast {
      position: fixed;
      top: 18px;
      left: 50%;
      z-index: 40;
      transform: translateX(-50%);
      max-width: calc(100% - 32px);
      padding: 7px 15px;
      border-radius: var(--radius-pill);
      background: var(--surface-popover);
      color: var(--text-2);
      font-size: 13px;
      opacity: 0;
      pointer-events: none;
      transition: opacity 0.25s ease;
    }
    .toast.show {
      opacity: 1;
    }
    @media (max-width: 760px) {
      .layout,
      .layout.nav-hidden {
        display: block;
      }
      .navtoggle {
        width: 44px;
        height: 44px;
        margin: -8px 0 -8px -12px;
      }
    }
  }
</style>
