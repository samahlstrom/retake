<script lang="ts">
  // The share sheet (spec/team.md, "Sharing a video from the page"): who it's for, Can comment or Can watch, how long,
  // then Create link gives the link with Copy. Under it, the video's links that still work, each with Revoke. A link
  // opens this video only. On this computer it reaches people through a tunnel (started now when needed) or the local
  // network, and the sheet says so in one line when only the network can.
  import { Dialog } from 'bits-ui';
  import {
    ACCESS,
    EXPIRES,
    creatingWord,
    linkLine,
    type Access,
    type Expiry,
    type Reach,
    type ShareLink,
    type VideoTarget,
  } from '../../lib/video-menu';
  import { copyText, listShares, makeShare, revokeShare, type Made } from '../../lib/video-menu-api';

  let { target, onclose }: { target: VideoTarget; onclose: () => void } = $props();

  let open = $state(true);
  let label = $state('');
  let access = $state<Access>('reviewer');
  let expires = $state<Expiry>('7d');
  let links = $state.raw<ShareLink[]>([]);
  let reach = $state.raw<Reach | null>(null);
  let made = $state.raw<Made | null>(null);
  let busy = $state(false);
  let error = $state('');
  let copied = $state(false);
  let field = $state<HTMLInputElement | null>(null);

  async function load() {
    try {
      const r = await listShares(target.api);
      links = r.links;
      reach = r.reach;
    } catch (e) {
      error = e instanceof Error ? e.message : 'Couldn’t read the links';
    }
  }
  $effect(() => {
    void load();
  });

  async function create() {
    if (busy) return;
    busy = true;
    error = '';
    copied = false;
    try {
      made = await makeShare(target.api, { label: label.trim(), role: access, expires });
      label = '';
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Couldn’t make a link';
    } finally {
      busy = false;
    }
  }

  async function copy() {
    if (made && (await copyText(made.url, field))) {
      copied = true;
      setTimeout(() => (copied = false), 1600);
    }
  }

  async function revoke(id: number) {
    error = '';
    try {
      await revokeShare(target.api, id);
      if (made?.link.id === id) made = null;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Couldn’t revoke it';
    }
  }

  const note = $derived(made?.reach.note || '');
</script>

<Dialog.Root
  bind:open
  onOpenChange={(v) => {
    if (!v) onclose();
  }}
>
  <Dialog.Portal>
    <Dialog.Overlay class="vm-overlay" />
    <Dialog.Content class="vm-sheet" data-part="share-sheet" aria-describedby={undefined}>
      <header class="vm-head">
        <Dialog.Title class="vm-title">Share <span class="vm-what">{target.title}</span></Dialog.Title>
        <Dialog.Close class="vm-x" aria-label="Close"
          ><svg viewBox="0 0 20 20" aria-hidden="true"
            ><path d="M5 5l10 10M15 5L5 15" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" /></svg
          ></Dialog.Close
        >
      </header>

      {#if made}
        <div class="vm-link" data-testid="share-link">
          <input
            bind:this={field}
            readonly
            value={made.url}
            aria-label="The link"
            onfocus={(e) => (e.currentTarget as HTMLInputElement).select()}
          />
          <button class="vm-btn primary" onclick={copy}>{copied ? 'Copied' : 'Copy'}</button>
        </div>
        <p class="vm-note">
          {#if note}{note}{:else}Opens {target.title} only.
            {made.link.label ? `Their comments say ${made.link.label}.` : ''}{/if}
        </p>
        <button class="vm-text" onclick={() => (made = null)}>New link</button>
      {:else}
        <form
          class="vm-form"
          onsubmit={(e) => {
            e.preventDefault();
            void create();
          }}
        >
          <input
            class="vm-name"
            bind:value={label}
            maxlength="80"
            placeholder="Who it’s for (optional)"
            aria-label="Who it’s for"
            autocomplete="off"
          />
          <div class="vm-row">
            <span class="vm-label" id="vm-access">Access</span>
            <div class="vm-seg" role="radiogroup" aria-labelledby="vm-access">
              {#each ACCESS as a (a.value)}
                <button
                  type="button"
                  role="radio"
                  aria-checked={access === a.value}
                  class:on={access === a.value}
                  onclick={() => (access = a.value)}>{a.label}</button
                >
              {/each}
            </div>
          </div>
          <div class="vm-row">
            <span class="vm-label" id="vm-expires">Expires</span>
            <div class="vm-seg" role="radiogroup" aria-labelledby="vm-expires">
              {#each EXPIRES as x (x.value)}
                <button
                  type="button"
                  role="radio"
                  aria-checked={expires === x.value}
                  class:on={expires === x.value}
                  onclick={() => (expires = x.value)}>{x.label}</button
                >
              {/each}
            </div>
          </div>
          {#if reach?.note}<p class="vm-note">{reach.note}</p>{/if}
          <button class="vm-btn primary vm-create" type="submit" disabled={busy}
            >{busy ? creatingWord(reach) : 'Create link'}</button
          >
        </form>
      {/if}

      {#if error}<p class="vm-err" role="alert">{error}</p>{/if}

      {#if links.length}
        <section class="vm-links" aria-label="Links that work">
          <h3>Links that work</h3>
          <ul>
            {#each links as l (l.id)}
              {@const line = linkLine(l)}
              <li data-link={l.id}>
                <span class="vm-who">{line.who}</span>
                <span class="vm-detail">{line.detail}</span>
                <button class="vm-text danger" onclick={() => revoke(l.id)} aria-label={`Revoke ${line.who}`}
                  >Revoke</button
                >
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>
