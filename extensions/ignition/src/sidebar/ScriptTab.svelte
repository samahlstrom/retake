<script lang="ts">
  // The Script tab next to the comments, on a video that has v0 (spec/ignition.md): script.md read-only, the section
  // under the playhead highlighted (from v0/cuts/<this version>.json). Clicking a section seeks to its start; sections
  // this version doesn't cover are dimmed.
  import { onMount, untrack } from 'svelte';
  import type { Ctx } from '../types';
  import { blocks, sections } from '../lib/doc';
  import { parseCut, sectionAt, sectionFor, type Cut } from '../lib/cuts';
  import { isMissing, sayError } from '../lib/errors';
  import { clock } from '../lib/stamp';

  let { ctx }: { ctx: Ctx } = $props();

  const c = untrack(() => ctx);
  const files = c.files();
  const label = c.version?.label ?? '';
  /** On a video version, with a playhead; on v0 itself there's nothing to follow. */
  const playing = !!c.player && !c.version?.kind;
  const SCRIPT = 'v0/script.md';
  const CUT = `v0/cuts/${label}.json`;

  let text = $state<string | null>(null);
  let cut = $state.raw<Cut | null>(null);
  let error = $state('');
  let t = $state(untrack(() => c.player?.time() ?? 0));
  let list = $state<HTMLDivElement>();

  async function loadScript(): Promise<void> {
    try {
      text = await (await files.read(SCRIPT)).text();
      error = '';
    } catch (e) {
      if (isMissing(e)) text = '';
      else error = sayError(e);
    }
  }

  async function loadCut(): Promise<void> {
    if (!playing || !label) return;
    try {
      cut = parseCut(JSON.parse(await (await files.read(CUT)).text()), label);
    } catch {
      cut = null;
    }
  }

  const secs = $derived(text ? sections(text) : []);
  const active = $derived(sectionAt(cut, t));

  $effect(() => {
    const h = active?.heading;
    if (!h) return;
    untrack(() =>
      list?.querySelector<HTMLElement>('.ig-sec.ig-on')?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' }),
    );
  });

  onMount(() => {
    void loadScript();
    void loadCut();
    const offs = [
      files.watch((changed) => {
        if (changed.some((f) => f.path === SCRIPT)) void loadScript();
        if (changed.some((f) => f.path === CUT)) void loadCut();
      }),
    ];
    if (playing && c.player) offs.push(c.player.onTime((x) => (t = x)));
    return () => offs.forEach((off) => off());
  });
</script>

<div class="ig-script" data-part="ignition-script" bind:this={list}>
  {#if c.ext.machine === false}
    <p class="ig-script-note">
      The editing computer doesn't have Ignition. Run <code>retake ext enable ignition</code> there.
    </p>
  {:else if error}
    <p class="ig-err">{error}</p>
  {:else if text !== null && !text.trim()}
    <p class="ig-empty">No script yet. Write it in v0's Script tab.</p>
  {:else if text !== null}
    {#if playing && !cut}
      <p class="ig-script-note">{label} has no cuts file yet, so its sections aren't linked.</p>
    {/if}
    {#each secs as s (s.start)}
      {@const cs = s.heading !== null ? sectionFor(cut, s.heading) : null}
      {#if s.heading === null}
        <div class="ig-sec ig-pre">
          {#each blocks(s.lines) as b, i (i)}
            <span class={b.heading ? 'ig-sec-sub' : 'ig-sec-p'}>{b.text}</span>
          {/each}
        </div>
      {:else}
        <button
          class={['ig-sec', !!cs && active === cs && 'ig-on', playing && !cs && 'ig-dim']}
          data-part="ignition-section"
          disabled={!playing || !cs}
          onclick={() => cs && c.player?.seek(cs.from)}
        >
          <span class="ig-sec-h">
            <span>{s.heading}</span>
            {#if cs}<span class="ig-sec-t">{clock(cs.from)}</span>{/if}
          </span>
          {#each blocks(s.lines.slice(1)) as b, i (i)}
            <span class={b.heading ? 'ig-sec-sub' : 'ig-sec-p'}>{b.text}</span>
          {/each}
        </button>
      {/if}
    {/each}
  {/if}
</div>
