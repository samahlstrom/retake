<script lang="ts">
  // The home screen (spec/home.md): one card per project, to review first. A card is the project's first frame, its
  // name, its newest version and length, and where it is in the loop. Opening one goes to its review page.
  import { fmtLength, STATUS_TEXT, streamText, toReview } from '../lib/home';
  import type { Home } from './home.svelte';

  let { home }: { home: Home } = $props();

  const projects = $derived(home.res.projects);
  const todo = $derived(toReview(projects));
  const title = $derived(home.res.title || 'Videos');
  const sub = $derived(!home.loaded || !projects.length ? '' : todo ? `${todo} to review` : 'All reviewed');

  $effect(() => {
    document.title = todo ? `(${todo}) ${title}` : title;
  });
</script>

<main class="home">
  <header>
    <h1>{title}</h1>
    {#if sub}<span class="sub" id="todo">{sub}</span>{/if}
    {#if home.error}<span class="err">{home.error}</span>{/if}
  </header>

  {#if home.loaded && !projects.length && !home.error}
    <p class="empty">
      No videos here yet. A video is a file named with its version, like <code>clip_v1.mp4</code>, in this folder or one
      of its folders (<code>clip/v2/clip_v2.mp4</code>). New ones show up here on their own.
    </p>
  {/if}

  <ul class="grid">
    {#each projects as p (p.name)}
      {@const stream = streamText(p.stream)}
      {@const len = fmtLength(p.duration)}
      <li>
        <a class="card" href={p.url} data-name={p.name} data-status={p.status}>
          <div class="thumb">
            {#if p.thumb && !home.broken.has(p.name)}
              <img src={p.thumb} alt="" decoding="async" onerror={() => home.thumbFailed(p.name)} />
            {/if}
            {#if len}<span class="len">{len}</span>{/if}
          </div>
          <div class="meta">
            <span class="title">{p.title}</span>
            <span class="row">
              {#if p.version}<span class="ver">{p.version}</span>{/if}
              <span class="chip {p.status}">{STATUS_TEXT[p.status]}</span>
              {#if stream}<span class="chip stream">{stream}</span>{/if}
            </span>
          </div>
        </a>
      </li>
    {/each}
  </ul>
</main>
