<script lang="ts">
  // The settings page (spec/home.md, "Settings"), the owner's, in the home's main area: which agent edits and what
  // happens when its usage runs out, where agents run, the computers, the extensions and the theme folder. Locally a
  // change is written to config.toml at once; on the team server the agents are the server's setting, and the rest is
  // each computer's own (shown, not changed here).
  import { onMount } from 'svelte';
  import StyledSelect from '../components/ui/Select.svelte';
  import ConnectBlock from '../components/ConnectBlock.svelte';
  import FolderPicker from '../components/FolderPicker.svelte';
  import { post, tryLine, waitForHome, type TryResult } from '../setup/setup';
  import { connectCommand } from '../lib/automation.svelte';
  import { copyText } from '../lib/video-menu-api';
  import {
    OPEN_IN,
    SHARE_TUNNEL,
    agentChange,
    agentOptions,
    effectiveAgent,
    fallbackOptions,
    loadSettings,
    normalizeSettings,
    openInOptions,
    pendingRestart,
    saveSettings,
    type Settings,
    type SettingsChange,
  } from './settings';

  let s = $state.raw<Settings | null>(null);
  let error = $state('');
  let saved = $state('');
  let busy = $state(false);
  let command = $state('');
  let picking = $state(false);
  let moving = $state(false);
  let tried = $state<TryResult | 'busy' | null>(null);
  let copiedInstall = $state(false);
  let savedTimer: ReturnType<typeof setTimeout> | null = null;

  const online = $derived(s ? s.machines.filter((m) => m.online) : []);
  const restart = $derived(s ? pendingRestart(s) : []);
  const openNote = $derived(OPEN_IN.find((o) => o.value === s?.open_in)?.note ?? '');

  onMount(() => {
    void (async () => {
      const r = await loadSettings();
      s = r.settings;
      error = r.error;
    })();
    return () => {
      if (savedTimer) clearTimeout(savedTimer);
    };
  });

  // Hosted with no computer online: the one line to connect one.
  $effect(() => {
    if (s?.hosted && !online.length && !command) void connectCommand('api/connect').then((c) => (command = c));
  });

  // A new videos folder: retake start serves it at once, so the home reloads once the server answers for it.
  async function moveTo(folder: string) {
    picking = false;
    const before = s?.folder ?? '';
    if (folder === before) return; // the same folder: nothing moves
    await save({ folder });
    if (error) return;
    moving = true;
    if (
      await waitForHome(
        fetch,
        30_000,
        300,
        async (r) => normalizeSettings(await r.json()).folder !== before,
        'api/settings',
      )
    )
      location.replace('./');
    else error = 'Your videos folder didn’t open. Reload the page.';
    moving = false;
  }

  async function test(agent: string) {
    tried = 'busy';
    try {
      tried = await post<TryResult>('api/settings/test', { agent });
    } catch (e) {
      tried = { ok: false, error: e instanceof Error ? e.message : 'It didn’t answer.', seconds: 0 };
    }
  }

  async function save(change: SettingsChange) {
    if (busy) return;
    busy = true;
    error = '';
    try {
      s = await saveSettings(change);
      saved = 'Saved';
      if (savedTimer) clearTimeout(savedTimer);
      savedTimer = setTimeout(() => (saved = ''), 1800);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Couldn’t save that';
    } finally {
      busy = false;
    }
  }
</script>

<section class="settings" data-part="settings" aria-labelledby="settings-title">
  <header>
    <div class="heading">
      <h1 id="settings-title">Settings</h1>
      <p class="sub">
        {#if s?.hosted}This server’s settings.{:else if s?.config}Saved in <code>{s.config}</code>.{:else}&nbsp;{/if}
        <span class="saved" role="status" aria-live="polite">{saved}</span>
      </p>
    </div>
  </header>

  {#if error}<p class="err" role="alert">{error}</p>{/if}

  {#if s}
    {#if s.folder && !s.hosted}
      <div class="group" data-part="settings-section" aria-labelledby="set-videos">
        <h2 id="set-videos">Your videos</h2>
        <div class="row" data-part="settings-row">
          <div class="what">
            <code class="name path" data-testid="settings-folder">{s.folder}</code>
            <span class="note"
              >{#if moving}Opening it…{:else if s.folder_editable}Retake opens this folder when it starts.{:else}Opened
                with <code>retake open</code>. The app and <code>retake start</code> open the one you chose in setup.{/if}</span
            >
          </div>
          {#if s.folder_editable && !picking}
            <button class="quiet" disabled={busy || moving} onclick={() => (picking = true)}>Change</button>
          {/if}
        </div>
        {#if picking}
          <FolderPicker
            start={s.folder}
            endpoint="api/settings/folders"
            onpick={moveTo}
            oncancel={() => (picking = false)}
          />
        {/if}
      </div>
    {/if}

    <div class="group" data-part="settings-section" aria-labelledby="set-agent">
      <h2 id="set-agent">Agent</h2>
      <div class="row" data-part="settings-row">
        <div class="what">
          <span class="name">Default agent</span>
          <span class="note"
            >{#if tried === 'busy'}Testing…{:else if tried}<span class:ok={tried.ok} class:bad={!tried.ok}
                >{tryLine(tried)}</span
              >{:else}Edits when you press Send, on every video.{/if}</span
          >
        </div>
        {#if s.agents.length && s.test}
          <button class="quiet" disabled={tried === 'busy'} onclick={() => test(effectiveAgent(s!))}>Test</button>
        {/if}
        {#if s.agents.length}
          <StyledSelect
            label="Default agent"
            value={effectiveAgent(s)}
            options={agentOptions(s)}
            disabled={busy}
            onValueChange={(v) => save(agentChange(s!, v))}
          />
        {:else}
          <span class="value dim"
            >{s.hosted ? 'Connect a computer first' : 'None found. Install Claude Code or Codex.'}</span
          >
        {/if}
      </div>
      <div class="row" data-part="settings-row">
        <div class="what">
          <span class="name">If its usage runs out</span>
          <span class="note">Pause and ask, or carry on with another agent.</span>
        </div>
        <StyledSelect
          label="If its usage runs out"
          value={s.fallback}
          options={fallbackOptions(s)}
          disabled={busy || s.agents.length < 2}
          onValueChange={(v) => save({ fallback: v })}
        />
      </div>
      <div class="row" data-part="settings-row">
        <div class="what">
          <span class="name">Where agents run</span>
          <span class="note"
            >{#if s.open_in_editable}{openNote} Applies the next time retake starts.{:else}Each computer’s own setting:
              <code>open_agents_in</code> in its config.toml.{/if}</span
          >
        </div>
        {#if s.open_in_editable}
          <StyledSelect
            label="Where agents run"
            value={s.open_in}
            options={openInOptions(s)}
            disabled={busy}
            onValueChange={(v) => save({ open_in: v as Settings['open_in'] })}
          />
        {/if}
      </div>
      <p class="note pad" data-testid="agent-choice">
        A video’s ⋯ can give it its own agent, and holding Send picks one for one send.
      </p>
    </div>

    {#if s.sharing && !s.hosted}
      {@const sh = s.sharing}
      <div class="group" data-part="settings-section" aria-labelledby="set-sharing">
        <h2 id="set-sharing">Sharing</h2>
        <div class="row" data-part="settings-row">
          <div class="what">
            <span class="name">Links reach people</span>
            <span class="note"
              >{#if sh.tunnel === 'off'}Only on your network (Wi-Fi), while Retake runs.{:else if sh.cloudflared}Anywhere,
                through a Cloudflare tunnel Retake starts when you share.{:else}On your network until cloudflared is
                installed.{/if}</span
            >
          </div>
          <StyledSelect
            label="Links reach people"
            value={sh.tunnel}
            options={SHARE_TUNNEL.map((o) => ({ value: o.value, label: o.label }))}
            disabled={busy}
            onValueChange={(v) => save({ share_tunnel: v as 'cloudflare' | 'off' })}
          />
        </div>
        {#if sh.tunnel !== 'off' && !sh.cloudflared && sh.install}
          <div class="row stack install" data-part="settings-row">
            <span class="note">cloudflared isn’t installed. This installs it:</span>
            <div class="cmd">
              <code>{sh.install}</code>
              <button
                class="quiet"
                onclick={async () => {
                  copiedInstall = await copyText(sh.install);
                  setTimeout(() => (copiedInstall = false), 1600);
                }}>{copiedInstall ? 'Copied' : 'Copy'}</button
              >
            </div>
          </div>
        {/if}
      </div>
    {/if}

    <div class="group" data-part="settings-section" aria-labelledby="set-computers">
      <h2 id="set-computers">{s.hosted ? 'Computers' : 'This computer'}</h2>
      {#if s.hosted && !online.length}
        <div class="row stack" data-part="settings-row">
          <ConnectBlock {command} title={s.machines.length ? 'Connect a computer' : 'Connect your computer'} />
        </div>
      {/if}
      {#each s.machines as m (m.id)}
        <div class="row" data-part="settings-row">
          <div class="what">
            <span class="name"><span class="dot" class:on={m.online} aria-hidden="true"></span>{m.name}</span>
            <span class="note">{m.agents.map((a) => a.name).join(', ') || 'No agents'}</span>
          </div>
          <span class="value dim">{m.online ? 'Online' : 'Offline'}</span>
        </div>
      {:else}
        {#if !s.hosted}
          <div class="row" data-part="settings-row">
            <div class="what">
              <span class="name">Edits here, in this retake</span>
              <span class="note">Shows up here once a video’s editing worker starts.</span>
            </div>
          </div>
        {/if}
      {/each}
    </div>

    <div class="group" data-part="settings-section" aria-labelledby="set-ext">
      <h2 id="set-ext">Extensions</h2>
      {#each s.extensions as e (e.id)}
        <div class="row toggle" data-part="settings-row" data-ext={e.id}>
          <div class="what">
            <span class="name">{e.name}</span>
            <span class="note"
              >{e.description ?? ''}{#if e.on !== e.loaded}{e.description ? ' ' : ''}<b
                  >Restart retake to {e.on ? 'turn it on' : 'turn it off'}.</b
                >{/if}</span
            >
          </div>
          {#if s.extensions_editable}
            <button
              class="switch"
              data-part="settings-switch"
              role="switch"
              aria-checked={e.on}
              aria-label={e.name}
              disabled={busy}
              onclick={() => save({ extensions: { [e.id]: !e.on } })}><span></span></button
            >
          {:else}
            <span class="value dim">On</span>
          {/if}
        </div>
      {:else}
        <p class="note pad">None installed.</p>
      {/each}
      {#if s.hosted}<p class="note pad">
          The server’s extensions are set where it runs (<code>--extensions</code>).
        </p>{/if}
      {#if restart.length > 1}<p class="note pad">Restart retake to apply the changes above.</p>{/if}
    </div>

    <div class="group" data-part="settings-section" aria-labelledby="set-theme">
      <h2 id="set-theme">Theme</h2>
      <div class="row" data-part="settings-row">
        <div class="what">
          <span class="name"
            >{s.theme.used
              ? (s.theme.name ?? 'Your theme')
              : s.theme.folder
                ? 'The built-in look'
                : 'Theming is off'}</span
          >
          {#if s.theme.folder}
            <span class="note"><code class="path">{s.theme.folder}</code></span>
            {#if !s.theme.used}<span class="note"><code>retake theme init</code> makes one there.</span>{/if}
          {:else}
            <span class="note">Turned off with <code>--theme none</code> or <code>RETAKE_THEME=none</code>.</span>
          {/if}
        </div>
      </div>
    </div>
  {:else if !error}
    <p class="sub">Loading…</p>
  {/if}
</section>

<style>
  @layer retake {
    .settings {
      max-width: 720px;
    }
    .sub code,
    .note code {
      font: 12px var(--font-mono);
      color: var(--text-3);
    }
    .saved {
      margin-left: 6px;
      color: var(--status-done);
    }
    .group {
      margin: 0 0 32px;
    }
    h2 {
      margin: 0 0 4px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 600;
      letter-spacing: 0.04em;
      text-transform: uppercase;
    }
    .row {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 20px;
      min-height: 56px;
      padding: 10px 0;
      border-bottom: 1px solid var(--line);
    }
    .row.stack {
      display: block;
    }
    .install {
      display: grid;
      gap: 8px;
    }
    .install .cmd {
      display: flex;
      align-items: center;
      gap: 10px;
    }
    .install code {
      flex: 1;
      min-width: 0;
      overflow-x: auto;
      white-space: nowrap;
      font: 12px var(--font-mono);
      color: var(--text-2);
    }
    .what {
      display: grid;
      gap: 2px;
      min-width: 0;
    }
    .name {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      color: var(--text);
      font-weight: 550;
    }
    .note {
      color: var(--muted);
      font-size: 12.5px;
      line-height: 1.5;
    }
    .note b {
      color: var(--warn);
      font-weight: 550;
    }
    .note.pad {
      margin: 10px 0 0;
    }
    .note .ok {
      color: var(--status-done);
    }
    .note .bad {
      color: var(--danger);
    }
    code.name {
      font: 13px var(--font-mono);
      overflow-wrap: anywhere;
    }
    .quiet {
      flex: none;
      min-height: 32px;
      margin-left: auto;
      padding: 0 12px;
      border: 1px solid var(--border);
      border-radius: var(--radius);
      background: transparent;
      color: var(--text-3);
      font: inherit;
      font-weight: 600;
      cursor: pointer;
    }
    .quiet:hover:not(:disabled) {
      border-color: var(--border-hover);
      color: var(--text);
    }
    .quiet:disabled {
      cursor: default;
      opacity: 0.6;
    }
    .quiet:focus-visible {
      outline: 2px solid var(--focus);
      outline-offset: 2px;
    }
    .value {
      flex: none;
      color: var(--text-3);
    }
    .dim {
      color: var(--muted);
    }
    .dot {
      width: 7px;
      height: 7px;
      border-radius: 50%;
      background: var(--status-off);
    }
    .dot.on {
      background: var(--status-done);
    }
    .row :global(.retake-select-trigger) {
      flex: none;
      min-width: 200px;
    }
    /* On/off: a track and a knob, the accent when on. */
    .switch {
      flex: none;
      position: relative;
      box-sizing: border-box;
      width: 40px;
      min-width: 0;
      height: 24px;
      min-height: 0;
      padding: 0;
      border: 0;
      border-radius: var(--radius-pill);
      background: var(--surface-control);
      cursor: pointer;
      transition: background 0.15s ease;
    }
    .switch span {
      position: absolute;
      top: 3px;
      left: 3px;
      width: 18px;
      height: 18px;
      border-radius: 50%;
      background: var(--text-3);
      transition:
        transform 0.15s ease,
        background 0.15s ease;
    }
    .switch[aria-checked='true'] {
      background: var(--accent);
    }
    .switch[aria-checked='true'] span {
      transform: translateX(16px);
      background: var(--on-accent);
    }
    /* A bigger place to tap than the track. */
    .switch::before {
      content: '';
      position: absolute;
      inset: -10px -6px;
    }
    .switch:focus-visible {
      outline: 2px solid var(--focus);
      outline-offset: 2px;
    }
    .switch:disabled {
      cursor: default;
      opacity: 0.6;
    }
    .path {
      overflow-wrap: anywhere;
    }
    @media (max-width: 760px) {
      .row {
        flex-wrap: wrap;
        gap: 10px;
      }
      .row.toggle {
        flex-wrap: nowrap;
      }
      .row :global(.retake-select-trigger) {
        min-width: 0;
        width: 100%;
        min-height: 44px;
      }
      .switch {
        width: 48px;
        height: 30px;
      }
      .quiet {
        min-height: 44px;
      }
      .switch span {
        width: 24px;
        height: 24px;
      }
      .switch[aria-checked='true'] span {
        transform: translateX(18px);
      }
    }
  }
</style>
