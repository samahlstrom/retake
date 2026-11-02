<script lang="ts">
  // voice/: the talk-outs, newest first: when, how long, play, the transcript, and Insert at caret.
  import { onMount, untrack } from 'svelte';
  import type { Files } from '../types';
  import { talkOuts, wordsLength, type TalkOut } from '../lib/files';
  import { sayError } from '../lib/errors';
  import { clock, when } from '../lib/stamp';

  let {
    files,
    rev,
    readonly,
    oninsert,
  }: {
    files: Files;
    /** Bumped when voice/ changed on disk. */
    rev: number;
    readonly: boolean;
    oninsert: (rel: string, text: string) => void;
  } = $props();

  interface Row extends TalkOut {
    text: string | null;
    dur: number | null;
  }

  let rows = $state<Row[]>([]);
  let loaded = $state(false);
  let error = $state('');
  let now = $state(new Date());
  /** What was read, by path and sha, so a reload reads only what changed. */
  const cache = new Map<string, { sha: string; value: unknown }>();

  async function readOnce<T>(path: string, sha: string, parse: (text: string) => T): Promise<T | null> {
    const hit = cache.get(path);
    if (hit && hit.sha === sha) return hit.value as T;
    try {
      const value = parse(await (await files.read(path)).text());
      cache.set(path, { sha, value });
      return value;
    } catch {
      return null;
    }
  }

  async function load(): Promise<void> {
    try {
      const list = talkOuts(await files.list('v0/voice/*'));
      rows = await Promise.all(
        list.map(async (t): Promise<Row> => {
          const [text, dur] = await Promise.all([
            t.md ? readOnce(t.md.path, t.md.sha256, (s) => s.trim()) : null,
            t.words ? readOnce(t.words.path, t.words.sha256, (s) => wordsLength(JSON.parse(s))) : null,
          ]);
          return { ...t, text: text || null, dur: dur ?? null };
        }),
      );
      error = '';
    } catch (e) {
      error = sayError(e);
    }
    now = new Date();
    loaded = true;
  }

  $effect(() => {
    void rev;
    untrack(() => void load());
  });

  onMount(() => {
    const t = setInterval(() => (now = new Date()), 60_000);
    return () => clearInterval(t);
  });

  function metadata(r: Row, a: HTMLAudioElement): void {
    if (Number.isFinite(a.duration) && a.duration > 0) r.dur = a.duration;
  }
</script>

<div class="ig-voice" data-part="ignition-voice">
  <div class="ig-col">
    {#if error}<p class="ig-err">{error}</p>{/if}
    {#if loaded && !rows.length && !error}
      <p class="ig-empty">No talk-outs yet. Press R or Record and talk: it lands in the text at the caret.</p>
    {/if}
    <ul class="ig-talks">
      {#each rows as r (r.st)}
        <li class="ig-talk" data-part="ignition-talkout">
          <div class="ig-talk-line">
            <span class="ig-when">{r.at ? when(r.at, now) : r.st}</span>
            {#if r.dur}<span class="ig-dur">{clock(r.dur)}</span>{/if}
            <span class="ig-grow"></span>
            {#if !readonly && r.text}
              <button class="ig-link" data-part="ignition-insert" onclick={() => oninsert(r.rel, r.text!)}
                >Insert at caret</button
              >
            {/if}
          </div>
          <audio
            class="ig-audio"
            controls
            preload="none"
            src={files.url(r.audio.path)}
            onloadedmetadata={(e) => metadata(r, e.currentTarget)}
          ></audio>
          <p class={['ig-transcript', !r.text && 'ig-quiet']}>{r.text ?? 'No transcript yet.'}</p>
        </li>
      {/each}
    </ul>
  </div>
</div>
