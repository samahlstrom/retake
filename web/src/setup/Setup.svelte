<script lang="ts">
  // The first-run setup page (spec/setup.md): three questions with an answer already chosen, and one button. Your
  // videos folder, your agent (found, or how to get one), and where agents work. Everything here is in Settings too.
  import { onMount } from 'svelte';
  import FolderPicker from '../components/FolderPicker.svelte';
  import { brandOf } from '../lib/brand';
  import {
    ffmpegLine,
    installSteps,
    normalizeState,
    placeText,
    placesShown,
    post,
    terminalName,
    tryLine,
    videosLine,
    waitForHome,
    type PlaceId,
    type SetupHook,
    type SetupState,
    type TryResult,
  } from './setup';

  let { hook }: { hook: SetupHook } = $props();

  const brand = brandOf();
  let s = $state.raw<SetupState | null>(null);
  let error = $state('');
  let agent = $state('');
  let place = $state<PlaceId>('background');
  let picking = $state(false);
  let checking = $state(false);
  let finishing = $state(false);
  let tries = $state<Record<string, TryResult | 'busy'>>({});
  let copied = $state('');

  function take(next: SetupState) {
    s = next;
    if (!next.agents.some((a) => a.id === agent)) agent = next.agent;
    if (!next.places.some((p) => p.id === place && p.available) || !hook.loaded) place = next.open_in;
    hook.state = next;
    hook.loaded = true;
  }

  async function load() {
    try {
      const r = await fetch('api/setup', { cache: 'no-store' });
      if (!r.ok) throw new Error();
      take(normalizeState(await r.json()));
    } catch {
      error = 'Retake isn’t answering. Is it still running?';
    }
  }
  onMount(() => void load());

  async function pickFolder(path: string) {
    picking = false;
    error = '';
    try {
      take(normalizeState(await post('api/setup', { folder: path })));
    } catch (e) {
      error = e instanceof Error ? e.message : 'That didn’t work';
    }
  }

  async function checkAgain() {
    checking = true;
    error = '';
    try {
      take(normalizeState(await post('api/setup/check', {})));
    } catch (e) {
      error = e instanceof Error ? e.message : 'That didn’t work';
    } finally {
      checking = false;
    }
  }

  async function test(id: string) {
    tries = { ...tries, [id]: 'busy' };
    try {
      const r = await post<TryResult>('api/setup/test', { agent: id });
      tries = { ...tries, [id]: r };
    } catch (e) {
      tries = {
        ...tries,
        [id]: { ok: false, error: e instanceof Error ? e.message : 'It didn’t answer.', seconds: 0 },
      };
    }
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // no clipboard API: the line is selectable
    }
    copied = text;
    setTimeout(() => (copied = copied === text ? '' : copied), 1600);
  }

  async function finish() {
    if (!s) return;
    finishing = true;
    error = '';
    try {
      await post('api/setup/done', { folder: s.folder_path, agent, open_in: place });
      hook.done = true;
      if (await waitForHome()) location.replace('./');
      else error = 'Your videos didn’t open. Reload this page.';
    } catch (e) {
      error = e instanceof Error ? e.message : 'That didn’t work';
    } finally {
      finishing = false;
    }
  }
</script>

<main class="setup" data-part="setup">
  <header>
    <span class="brand"
      >{brand.name}{#if !brand.custom}<span class="wordmark-dot">.</span>{/if}</span
    >
  </header>

  <h1>Set up {brand.name}</h1>
  <p class="lead">Three questions. You can change any of them later in Settings.</p>

  {#if error}<p class="err" role="alert">{error}</p>{/if}

  {#if s}
    {#if !s.ffmpeg}
      <p class="warn" role="status">
        ffmpeg isn’t installed, so videos won’t play. In {terminalName(s.os)}: <code>{ffmpegLine(s.os)}</code>
      </p>
    {/if}

    <section aria-labelledby="q-folder">
      <h2 id="q-folder">Your videos</h2>
      <div class="row">
        <div class="what">
          <code class="folder" data-testid="setup-folder">{s.folder || 'Choose a folder'}</code>
          <span class="note"
            >{s.folder ? videosLine(s) : 'Only the folder you choose, and its subfolders, will be scanned.'}</span
          >
        </div>
        {#if !picking}<button class="quiet" onclick={() => (picking = true)}
            >{s.folder ? 'Change' : 'Choose folder'}</button
          >{/if}
      </div>
      {#if picking}
        <FolderPicker
          start={s.folder_exists ? s.folder : '~'}
          endpoint="api/setup/folders"
          onpick={pickFolder}
          oncancel={() => (picking = false)}
        />
      {/if}
    </section>

    <section aria-labelledby="q-agent">
      <h2 id="q-agent">Your agent</h2>
      {#if s.agents.length}
        <div class="choices" role="radiogroup" aria-labelledby="q-agent">
          {#each s.agents as a (a.id)}
            {@const t = tries[a.id]}
            <div class="choice">
              <label>
                <input type="radio" name="agent" value={a.id} bind:group={agent} />
                <span class="name">{a.name}</span>
              </label>
              <span class="result" class:ok={t && t !== 'busy' && t.ok} class:bad={t && t !== 'busy' && !t.ok}
                >{t === 'busy' ? 'Testing…' : t ? tryLine(t) : ''}</span
              >
              <button class="quiet small" disabled={t === 'busy'} onclick={() => test(a.id)}>Test</button>
            </div>
          {/each}
        </div>
        <p class="note">Test asks it for a one-word reply, to check it’s signed in. It takes a few seconds.</p>
      {:else}
        <p class="body">
          An agent is the AI that edits your videos when you press Send. Retake doesn’t include one: install one of
          these, sign in, then check again. Paste the line into {terminalName(s.os)}.
        </p>
        <div class="installs">
          {#each installSteps(s.os) as step (step.agent)}
            <div class="install">
              <strong>{step.agent}</strong>
              <div class="cmd">
                <code>{step.install}</code>
                <button onclick={() => copy(step.install)} aria-label={`Copy the ${step.agent} install line`}
                  >{copied === step.install ? 'Copied' : 'Copy'}</button
                >
              </div>
              <span class="note">{step.signIn}</span>
            </div>
          {/each}
        </div>
        <button class="quiet" disabled={checking} onclick={checkAgain}>{checking ? 'Checking…' : 'Check again'}</button>
      {/if}
    </section>

    <section aria-labelledby="q-place">
      <h2 id="q-place">Where agents work</h2>
      <div class="choices" role="radiogroup" aria-labelledby="q-place">
        {#each placesShown(s) as p (p.id)}
          {@const t = placeText(p)}
          <label class="choice place" class:off={!p.available}>
            <input type="radio" name="place" value={p.id} bind:group={place} disabled={!p.available} />
            <span class="what">
              <span class="name">{t.label}</span>
              <span class="note">{p.available ? t.note : 'Not installed.'}</span>
            </span>
          </label>
        {/each}
      </div>
    </section>

    <div class="go">
      <button class="primary" disabled={finishing || !s.folder_path} onclick={finish}
        >{finishing ? 'Opening your videos…' : 'Open my videos'}</button
      >
    </div>
  {:else if !error}
    <p class="note">Loading…</p>
  {/if}
</main>
