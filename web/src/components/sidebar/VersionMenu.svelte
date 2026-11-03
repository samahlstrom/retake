<script lang="ts">
  // The comments' version, in their header: "v8 ⌄" (spec/notes.md, "The comments header"). Its menu lists every
  // version, newest first (an extension's version first), each with where its comments go and what's still to do
  // ("v7 → v8 · 3 to do"); a version and its revisions are one entry that plays the newest, with a small red live dot
  // while the agent works on it (spec/automation.md, "The live version"). A pick shows that version,
  // like the bar's picker. While an earlier version still has something to do, the button has a small dot and its
  // title says what. A single version is its label alone, without a menu.
  import { DropdownMenu } from 'bits-ui';
  import type { Review } from '../../lib/review.svelte';
  import { intoVersion } from '../../lib/notes/view';

  let { review }: { review: Review } = $props();

  const strip = $derived(review.versionStrip);
  const entries = $derived([...strip.filter((e) => e.kind), ...strip.filter((e) => !e.kind).reverse()]);
  const shown = $derived(strip.find((e) => e.on));
  const label = $derived(shown?.name ?? review.nameOf(review.shown));
  const earlier = $derived(review.earlierOpen);
  const into = $derived(review.kindShown ? null : intoVersion(review.shown));
  const title = $derived(
    [
      into ? `Comments on ${label} go into ${into}` : '',
      earlier.length ? `Open on earlier versions: ${earlier.map((e) => `${e.version} · ${e.todo}`).join(', ')}` : '',
    ]
      .filter(Boolean)
      .join('. ') || label,
  );
</script>

{#if entries.length}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class="vmenu"
      data-part="version-menu version-strip"
      aria-label="Version {label}: show another version's comments"
      {title}
      ><span class="v">{label}</span>{#if earlier.length}<span class="dot" aria-hidden="true"></span>{/if}<svg
        viewBox="0 0 16 16"
        aria-hidden="true"
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
        class="menu vmenu-list"
        data-part="version-menu-list earlier-versions"
        align="start"
        sideOffset={6}
        collisionPadding={10}
      >
        {#each entries as e (e.version)}
          <DropdownMenu.Item
            class="menu-item{e.on ? ' on' : ''}"
            data-version={e.version}
            aria-current={e.on ? 'true' : undefined}
            onSelect={() => review.pickVersion(e.label)}
            ><span class="name"
              >{e.name}{#if e.version === review.liveTarget}<span class="live" aria-label="live"
                ></span>{/if}{#if e.into && e.todo}<span class="into">→ {e.into}</span>{/if}</span
            ><span class="n"
              >{e.todo ? `${e.todo} to do` : ''}{#if e.on}<span class="check" aria-hidden="true">✓</span>{/if}</span
            ></DropdownMenu.Item
          >
        {/each}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
{:else}
  <span class="vmenu single" data-part="version-menu" {title}><span class="v">{label}</span></span>
{/if}
