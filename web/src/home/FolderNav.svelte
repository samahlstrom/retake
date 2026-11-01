<script lang="ts">
  // The left column of the home (spec/home.md): a filter, "All videos", and the folder tree: folders and projects
  // with a chevron, a project's videos under it, one status dot each. A name selects (the main area shows its
  // videos), a chevron opens or closes, a video opens its review. Desktop: a column with a resize handle. Phone: a
  // drawer that ☰ opens. Arrow keys move through the rows.
  import { tick } from 'svelte';
  import { DOT_TEXT, rows, type Row } from '../lib/tree';
  import { NAV_WIDTH, type Home } from './home.svelte';

  let { home }: { home: Home } = $props();

  const ALL = '';
  const list = $derived(rows(home.tree, home.expanded, home.filter));
  /** The row that takes Tab (roving focus): the one last moved to, else the selection, else All videos. */
  let focusKey = $state<string | null>(null);
  const tabKey = $derived.by(() => {
    const keys = new Set(list.map((r) => r.key));
    if (focusKey !== null && (focusKey === ALL || keys.has(focusKey))) return focusKey;
    return keys.has(home.selected) ? home.selected : ALL;
  });
  let tree = $state<HTMLElement>();

  const selectedKey = $derived(home.node.path);

  function focusRow(key: string) {
    focusKey = key;
    void tick().then(() => {
      const el = tree?.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`);
      el?.focus();
      el?.scrollIntoView({ block: 'nearest' });
    });
  }

  function onKey(e: KeyboardEvent, row: Row | null) {
    const keys = [ALL, ...list.map((r) => r.key)];
    const at = keys.indexOf(row ? row.key : ALL);
    const go = (i: number) => focusRow(keys[Math.max(0, Math.min(keys.length - 1, i))]);
    switch (e.key) {
      case 'ArrowDown':
        go(at + 1);
        break;
      case 'ArrowUp':
        go(at - 1);
        break;
      case 'Home':
        go(0);
        break;
      case 'End':
        go(keys.length - 1);
        break;
      case 'ArrowRight':
        if (row?.canOpen && !row.open) home.toggle(row.path, true);
        else if (row?.open) go(at + 1);
        break;
      case 'ArrowLeft':
        if (row?.canOpen && row.open) home.toggle(row.path, false);
        else if (row) focusRow(row.parent || ALL);
        break;
      case 'Enter':
      case ' ':
        if (row?.kind === 'video') {
          if (e.key === ' ') (e.currentTarget as HTMLElement).click();
          else return; // the link opens
        } else home.select(row ? row.path : '');
        break;
      default:
        return;
    }
    e.preventDefault();
  }

  // The drawer: Escape closes it; opening it puts the focus on the selected row.
  $effect(() => {
    if (!home.drawer) return;
    focusRow(tabKey);
    const esc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') home.drawer = false;
    };
    addEventListener('keydown', esc);
    return () => removeEventListener('keydown', esc);
  });

  // The resize handle: drag, ←/→, double-click to reset.
  function grab(e: PointerEvent) {
    if (e.button !== 0) return;
    const el = e.currentTarget as HTMLElement;
    const start = e.clientX,
      from = home.width;
    el.setPointerCapture(e.pointerId);
    const move = (m: PointerEvent) => home.resize(from + m.clientX - start);
    const up = () => {
      el.removeEventListener('pointermove', move);
      el.removeEventListener('pointerup', up);
      el.removeEventListener('pointercancel', up);
    };
    el.addEventListener('pointermove', move);
    el.addEventListener('pointerup', up);
    el.addEventListener('pointercancel', up);
    e.preventDefault();
  }
  function gripKey(e: KeyboardEvent) {
    const step = e.shiftKey ? 48 : 16;
    if (e.key === 'ArrowLeft') home.resize(home.width - step);
    else if (e.key === 'ArrowRight') home.resize(home.width + step);
    else return;
    e.preventDefault();
  }
</script>

{#snippet dot(d: Row['dot'])}
  <span
    class="dot {d}"
    data-dot={d}
    role={d ? 'img' : undefined}
    title={d ? DOT_TEXT[d] : undefined}
    aria-label={d ? DOT_TEXT[d] : undefined}
  ></span>
{/snippet}

{#if home.phone && home.drawer}
  <!-- Escape closes it too. -->
  <div class="scrim" aria-hidden="true" onclick={() => (home.drawer = false)}></div>
{/if}
<nav class="nav" class:open={home.drawer} class:hidden={home.hidden} data-part="nav" aria-label="Folders">
  <div class="search">
    <input
      type="search"
      data-part="nav-search"
      placeholder="Filter"
      aria-label="Filter folders and videos"
      autocomplete="off"
      spellcheck="false"
      bind:value={home.filter}
    />
  </div>
  <div class="rows" role="tree" aria-label={home.tree.title || 'Folders'} bind:this={tree}>
    <div
      class="row all"
      role="treeitem"
      aria-level={1}
      aria-selected={selectedKey === ALL}
      class:selected={selectedKey === ALL}
      data-key={ALL}
      data-part="nav-all"
      tabindex={tabKey === ALL ? 0 : -1}
      onclick={() => home.select('')}
      onkeydown={(e) => onKey(e, null)}
      onfocus={() => (focusKey = ALL)}
    >
      <span class="label">All videos</span>
      <span class="count">{home.res.projects.length || ''}</span>
    </div>
    {#each list as row (row.key)}
      {#if row.kind === 'video' && row.card}
        <a
          class="row video"
          role="treeitem"
          aria-level={row.depth + 1}
          aria-selected="false"
          href={row.card.url}
          data-key={row.key}
          data-name={row.card.name}
          data-part="nav-video"
          style:--depth={row.depth}
          tabindex={tabKey === row.key ? 0 : -1}
          onkeydown={(e) => onKey(e, row)}
          onfocus={() => (focusKey = row.key)}
          ><span class="chev" aria-hidden="true"></span><span class="label">{row.title}</span><span class="ver"
            >{row.card.version}</span
          >{@render dot(row.dot)}</a
        >
      {:else}
        <div
          class="row {row.kind}"
          role="treeitem"
          aria-level={row.depth + 1}
          aria-expanded={row.canOpen ? row.open : undefined}
          aria-selected={selectedKey === row.path}
          class:selected={selectedKey === row.path}
          data-key={row.key}
          data-path={row.path}
          data-part={row.kind === 'project' ? 'nav-project' : 'nav-folder'}
          style:--depth={row.depth}
          tabindex={tabKey === row.key ? 0 : -1}
          onclick={() => home.select(row.path)}
          onkeydown={(e) => onKey(e, row)}
          onfocus={() => (focusKey = row.key)}
        >
          <!-- → and ← do the same from the keyboard. -->
          <span
            class="chev"
            class:open={row.open}
            class:none={!row.canOpen}
            aria-hidden="true"
            onclick={(e) => {
              e.stopPropagation();
              if (row.canOpen) home.toggle(row.path);
            }}
            ><svg viewBox="0 0 16 16"
              ><path
                d="m6 4 4 4-4 4"
                fill="none"
                stroke="currentColor"
                stroke-width="1.6"
                stroke-linecap="round"
                stroke-linejoin="round"
              /></svg
            ></span
          ><span class="label">{row.title}</span>{@render dot(row.dot)}
        </div>
      {/if}
    {/each}
  </div>
  {#if home.loaded && home.filter.trim() && !list.length}
    <p class="none-found">Nothing matches “{home.filter.trim()}”.</p>
  {/if}
  {#if !home.phone}
    <!-- A focusable separator with a value is a widget (WAI-ARIA window splitter); svelte counts it as static. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    <div
      class="grip"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the folders"
      aria-valuenow={home.width}
      aria-valuemin={200}
      aria-valuemax={480}
      tabindex="0"
      data-part="nav-grip"
      onpointerdown={grab}
      onkeydown={gripKey}
      ondblclick={() => home.resize(NAV_WIDTH)}
    ></div>
  {/if}
</nav>

<style>
  .nav {
    position: sticky;
    top: 0;
    height: 100vh;
    height: 100dvh;
    display: flex;
    flex-direction: column;
    min-width: 0;
    border-right: 1px solid var(--hair, rgba(255, 255, 255, 0.07));
    background: #0c0c0e;
  }
  .nav.hidden {
    display: none;
  }
  .search {
    flex: none;
    padding: 18px 12px 10px;
  }
  .search input {
    width: 100%;
    min-height: 32px;
    padding: 6px 10px;
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 8px;
    background: #131316;
    color: inherit;
    font: inherit;
  }
  .search input::placeholder {
    color: #6c6c76;
  }
  .search input:focus {
    outline: none;
    border-color: rgba(166, 147, 250, 0.6);
  }
  .rows {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    scrollbar-gutter: stable;
    padding: 2px 6px 24px;
  }
  .row {
    --indent: calc(8px + var(--depth, 0) * 14px);
    display: flex;
    align-items: center;
    gap: 2px;
    height: 30px;
    padding: 0 8px 0 var(--indent);
    border-radius: 6px;
    color: #b4b4bd;
    text-decoration: none;
    white-space: nowrap;
    cursor: pointer;
    user-select: none;
    outline: none;
    -webkit-tap-highlight-color: transparent;
  }
  .row:hover {
    background: rgba(255, 255, 255, 0.04);
    color: #ececef;
  }
  .row:focus-visible {
    box-shadow: inset 0 0 0 1.5px rgba(90, 200, 250, 0.7);
  }
  .row.selected {
    background: rgba(255, 255, 255, 0.07);
    color: #fff;
  }
  .row.all {
    padding-left: 12px;
    margin-bottom: 4px;
    color: #d8d8de;
  }
  .row.project {
    color: #dcdce2;
    font-weight: 550;
  }
  .row.video {
    color: #a3a3ad;
  }
  .row.video:hover {
    color: #ececef;
  }
  .label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .count,
  .ver {
    flex: none;
    padding-left: 8px;
    color: #6c6c76;
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  /* The chevron: a glyph in a fixed slot, no box; it turns when open. Videos keep the slot so names line up. */
  .chev {
    flex: none;
    display: grid;
    place-items: center;
    width: 18px;
    height: 30px;
    color: #6c6c76;
  }
  .chev svg {
    width: 14px;
    height: 14px;
    transition: transform 0.15s ease;
  }
  .chev.open svg {
    transform: rotate(90deg);
  }
  .chev.none svg {
    visibility: hidden;
  }
  .row:hover .chev {
    color: #b4b4bd;
  }
  /* The status dot, in a fixed slot at the end of every row, so nothing moves when one comes or goes. */
  .dot {
    flex: none;
    width: 7px;
    height: 7px;
    margin-left: 8px;
    border-radius: 50%;
  }
  .dot.to_review {
    background: var(--voice, #5ac8fa);
  }
  .dot.being_edited {
    background: var(--st-working, #efb441);
  }
  .dot.question {
    background: var(--st-question, #a693fa);
  }
  .none-found {
    position: absolute;
    top: 100px;
    left: 18px;
    right: 12px;
    margin: 0;
    color: #6c6c76;
  }
  .grip {
    position: absolute;
    top: 0;
    right: -4px;
    bottom: 0;
    width: 8px;
    cursor: col-resize;
    z-index: 2;
    outline: none;
  }
  .grip:hover,
  .grip:focus-visible {
    background: linear-gradient(90deg, transparent 3px, rgba(90, 200, 250, 0.5) 3px 5px, transparent 5px);
  }

  /* The phone: a drawer over the page, shown only while open (☰). */
  @media (max-width: 760px) {
    .nav,
    .nav.hidden {
      position: fixed;
      inset: 0 auto 0 0;
      z-index: 30;
      width: min(86vw, 340px);
      height: auto;
      display: none;
      box-shadow: 12px 0 40px rgba(0, 0, 0, 0.5);
    }
    .nav.open {
      display: flex;
      transition: transform 0.18s ease;
    }
    .row {
      height: 44px;
    }
    .chev {
      width: 26px;
      height: 44px;
    }
    .search {
      padding-top: 14px;
    }
    .search input {
      min-height: 44px;
    }
    .grip {
      display: none;
    }
  }
  @starting-style {
    .nav.open {
      transform: translateX(-100%);
    }
  }
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 29;
    background: rgba(0, 0, 0, 0.55);
  }
</style>
