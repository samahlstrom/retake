<script lang="ts">
  // The one version menu (spec/notes.md, "The version menu"), in one place per layout: the bar on a computer, the
  // controls row under the video on a phone (the slim bar in landscape), and only on a phone's extension version in
  // portrait (no controls row there) the drawer's header. The button is the shown version's name (`v6`, `Script`) and a
  // chevron, with at most one dot (live, rendering, or earlier comments to do). The rows: newest first, one per version
  // (its revisions folded in, a pick plays the newest), the name with a small red dot right after it while the agent
  // works on it, the one on screen in bold, one quiet detail at the right; an extension's version last, set apart.
  // Hidden for a single version (nothing to choose). C is handled globally.
  import { DropdownMenu } from 'bits-ui';
  import type { Review } from '../lib/review.svelte';
  import { menuDot, menuRows, menuTitle } from '../lib/version-menu';
  import { byLabel, partner, verName } from '../lib/versions';

  let { review, side = 'top' }: { review: Review; side?: 'top' | 'bottom' } = $props();

  const rows = $derived(menuRows(review.versionStrip, review.versions, review.liveTarget));
  const cuts = $derived(rows.filter((r) => !r.kind));
  const kinds = $derived(rows.filter((r) => r.kind));
  const name = $derived(review.nameOf(review.shown));
  const shownLive = $derived(!!review.liveTarget && verName(review.shown) === review.liveTarget);
  const dot = $derived(
    menuDot({ shownLive, shown: byLabel(review.versions, review.shown), earlier: review.earlierOpen.length }),
  );
  const flipTo = $derived(review.kindShown ? null : partner(review.cuts, review.base));
  const title = $derived(
    menuTitle({
      name,
      flipTo: flipTo ? review.nameOf(flipTo.label) : null,
      flipped: review.flipped ? { shown: name, base: review.nameOf(review.base) } : null,
      earlier: review.earlierOpen,
    }),
  );
</script>

{#snippet row(r: (typeof rows)[number])}
  <DropdownMenu.Item
    class="menu-item{r.on ? ' on' : ''}"
    data-ver={r.label}
    data-version={r.version}
    aria-current={r.on ? 'true' : undefined}
    onSelect={() => review.pickVersion(r.label)}
    ><span class="name"
      >{r.name}{#if r.live}<span
          class="live"
          title="The agent is working on it: it updates as each comment's edit goes in"
          aria-label="live"
        ></span>{/if}</span
    >{#if r.detail}<span class="d">{r.detail}</span>{/if}</DropdownMenu.Item
  >
{/snippet}

{#if rows.length}
  <div id="ver">
    <DropdownMenu.Root bind:open={() => review.verOpen, (v) => (review.verOpen = v)}>
      <DropdownMenu.Trigger
        id="verBtn"
        data-part="version-picker version-menu version-strip"
        class={[review.flipped && 'flipped', dot].filter(Boolean).join(' ') || undefined}
        aria-label="Version {name}: choose another version"
        {title}
        ><span class="v">{name}</span><svg viewBox="0 0 16 16" aria-hidden="true"
          ><path
            d="M4.5 6.5 8 10l3.5-3.5"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          /></svg
        ></DropdownMenu.Trigger
      >
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          id="verList"
          class="menu vmenu-list"
          data-part="version-list version-menu-list earlier-versions"
          {side}
          align="center"
          sideOffset={8}
          collisionPadding={10}
        >
          {#each cuts as r (r.version)}{@render row(r)}{/each}
          {#if kinds.length && cuts.length}<DropdownMenu.Separator class="menu-sep" />{/if}
          {#each kinds as r (r.version)}{@render row(r)}{/each}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  </div>
{/if}
