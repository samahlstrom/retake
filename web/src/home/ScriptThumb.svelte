<script lang="ts">
  // An idea's picture on its card (spec/home.md, "Every card has a picture"): its document, read through its
  // extension's file routes (so it works hosted too): the first image in it, else its title and first lines, small on
  // the card's dark box. Nothing shows until it's read (the box keeps its size, so nothing moves). Read again only
  // when the video's files change.
  import { untrack } from 'svelte';
  import { kindOf } from '../ext/kinds';
  import type { ExtInfo } from '../ext/loader';
  import { pickDocument, scriptPreview, videoName, type Card, type ScriptPreview } from '../lib/home';

  let { video, exts }: { video: Card; exts: readonly ExtInfo[] } = $props();

  let preview = $state<ScriptPreview | null>(null);
  let imageFailed = $state(false);
  const k = $derived(kindOf(exts, video.kind));
  /** What the preview depends on, as one value: the effect runs again only when it changes. */
  const key = $derived(
    k ? [video.url, k.ext.id, k.kind.folder, k.kind.marker.join(','), video.title, video.changed ?? 0].join('\n') : '',
  );
  const enc = (p: string) => p.split('/').map(encodeURIComponent).join('/');

  async function load(signal: AbortSignal): Promise<void> {
    const kk = k;
    if (!kk) return;
    const base = `${video.url}api/ext/${encodeURIComponent(kk.ext.id)}/files`;
    let paths: string[] = [];
    for (const m of kk.kind.marker.filter((x) => /\.(md|markdown|txt)$/i.test(x))) {
      const r = await fetch(`${base}?glob=${encodeURIComponent(`${kk.kind.folder}/${m}`)}`, { signal });
      if (!r.ok) continue;
      const body = (await r.json()) as { files?: { path?: unknown }[] };
      paths = (body.files ?? []).map((f) => f.path).filter((p): p is string => typeof p === 'string');
      if (paths.length) break;
    }
    const doc = pickDocument(paths, video.title);
    if (!doc) return;
    const r = await fetch(`${base}/${enc(doc)}`, { signal });
    if (!r.ok) return;
    const p = scriptPreview((await r.text()).slice(0, 64_000), kk.kind.folder);
    imageFailed = false;
    preview = { ...p, image: p.image ? `${base}/${enc(p.image)}` : null };
  }

  $effect(() => {
    if (!key) return;
    const ac = new AbortController();
    untrack(() => load(ac.signal)).catch(() => {}); // unreadable: the quiet box stays
    return () => ac.abort();
  });
</script>

{#if preview?.image && !imageFailed}
  <img src={preview.image} alt="" decoding="async" onerror={() => (imageFailed = true)} />
{:else if preview}
  <div class="idea" data-part="idea-preview">
    <b>{preview.title || videoName(video)}</b>
    {#each preview.lines as line, i (i)}<span>{line}</span>{/each}
  </div>
{/if}

<style>
  @layer retake {
    .idea {
      position: absolute;
      inset: 0;
      display: flex;
      flex-direction: column;
      gap: 6px;
      padding: 14px 16px;
      overflow: hidden;
      color: var(--muted);
      font-size: 12.5px;
      line-height: 1.45;
      /* Long lines fade out at the bottom edge instead of being cut. */
      mask-image: linear-gradient(to bottom, var(--text) 72%, transparent);
    }
    .idea b {
      color: var(--text);
      font-size: 15px;
      font-weight: 600;
      line-height: 1.3;
    }
    .idea span {
      display: -webkit-box;
      -webkit-line-clamp: 2;
      line-clamp: 2;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }
  }
</style>
