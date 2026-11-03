<script lang="ts">
  import { onMount } from 'svelte';
  import { Feedback as FeedbackState } from '../lib/feedback.svelte';

  const feedback = new FeedbackState();
  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let title = $state<HTMLInputElement | null>(null);

  onMount(() => {
    void feedback.load();
  });

  function show() {
    open = true;
    requestAnimationFrame(() => title?.focus());
  }

  function close() {
    feedback.resetSuccess();
    open = false;
    requestAnimationFrame(() => trigger?.focus());
  }

  function keydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    void feedback.submit();
  }
</script>

<button
  bind:this={trigger}
  class="feedback-tab"
  class:open
  data-part="feedback feedback-tab"
  aria-haspopup="dialog"
  aria-expanded={open}
  onclick={show}>Feedback</button
>

{#if open}
  <dialog
    open
    class="feedback-panel"
    data-part="feedback feedback-panel"
    aria-labelledby="feedback-title"
    onkeydown={keydown}
  >
    <header>
      <h2 id="feedback-title">Feedback</h2>
      <button class="feedback-close" type="button" aria-label="Close feedback" onclick={close}>Close</button>
    </header>
    {#if feedback.url}
      <div class="feedback-success" role="status">
        <p>Thanks. Your feedback was sent.</p>
        <a href={feedback.url} target="_blank" rel="noreferrer">View issue</a>
        <button type="button" class="feedback-primary" onclick={close}>Done</button>
      </div>
    {:else}
      <form onsubmit={submit}>
        <fieldset disabled={feedback.pending}>
          <legend>Type</legend>
          <label><input type="radio" name="feedback-type" value="bug" bind:group={feedback.type} /> Bug</label>
          <label
            ><input type="radio" name="feedback-type" value="feature" bind:group={feedback.type} /> Feature request</label
          >
        </fieldset>
        <label class="feedback-field">
          <span>Title</span>
          <input bind:this={title} bind:value={feedback.title} autocomplete="off" disabled={feedback.pending} />
        </label>
        <label class="feedback-field">
          <span>Details</span>
          <textarea bind:value={feedback.description} rows="5" disabled={feedback.pending}></textarea>
        </label>
        <p class="feedback-note">Reports are public on GitHub. Do not include private information.</p>
        {#if feedback.error}<p class="feedback-error" role="alert">{feedback.error}</p>{/if}
        {#if feedback.enabled === false}
          <p class="feedback-note">GitHub sign-in is required to continue.</p>
          <a class="feedback-primary" href={feedback.githubURL()} target="_blank" rel="noreferrer">Continue on GitHub</a
          >
        {:else}
          <button class="feedback-primary" type="submit" disabled={feedback.pending}
            >{feedback.pending ? 'Sending…' : 'Send feedback'}</button
          >
        {/if}
      </form>
    {/if}
  </dialog>
{/if}

<style>
  @layer retake {
    .feedback-tab {
      position: fixed;
      z-index: 30;
      top: calc(50% - 48px);
      right: 0;
      width: 32px;
      height: 96px;
      padding: 6px 0;
      border: 1px solid var(--line-strong);
      border-right: 0;
      border-radius: var(--radius-sm) 0 0 var(--radius-sm);
      background: var(--surface-raised);
      color: var(--muted);
      font: 12px/1 var(--font-ui);
      writing-mode: vertical-rl;
      cursor: pointer;
    }
    .feedback-tab:hover,
    .feedback-tab:focus-visible {
      color: var(--text);
      border-color: var(--border-hover);
      outline: 2px solid var(--focus);
      outline-offset: -2px;
    }
    .feedback-tab.open {
      display: none;
    }
    .feedback-panel {
      position: fixed;
      z-index: 30;
      top: auto;
      left: auto;
      right: 16px;
      bottom: 80px;
      box-sizing: border-box;
      display: grid;
      gap: 12px;
      margin: 0;
      width: min(360px, calc(100% - 32px));
      max-height: calc(100dvh - 96px);
      overflow: auto;
      padding: 16px;
      border: 1px solid var(--line-strong);
      border-radius: var(--radius-lg);
      background: var(--surface-popover);
      color: var(--text-2);
      box-shadow: 0 16px 60px color-mix(in srgb, var(--shadow) 60%, transparent);
      font: 13px/1.45 var(--font-ui);
    }
    .feedback-panel header {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 12px;
    }
    .feedback-panel h2 {
      margin: 0;
      color: var(--text);
      font-size: 14px;
    }
    .feedback-close {
      padding: 0;
      border: 0;
      background: none;
      color: var(--muted);
      font: inherit;
    }
    .feedback-panel form,
    .feedback-success {
      display: grid;
      gap: 12px;
    }
    .feedback-panel fieldset {
      display: flex;
      flex-wrap: wrap;
      gap: 8px 14px;
      margin: 0;
      padding: 0;
      border: 0;
    }
    .feedback-panel legend,
    .feedback-field > span {
      margin-bottom: 4px;
      color: var(--text-6);
      font-size: 12px;
    }
    .feedback-panel fieldset label {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      min-height: 32px;
    }
    .feedback-panel input[type='radio'] {
      box-sizing: border-box;
      flex: none;
      width: 16px;
      height: 16px;
      min-width: 16px;
      min-height: 16px;
      margin: 0;
      padding: 0;
      border: 0;
      border-radius: 50%;
      background: transparent;
      accent-color: var(--accent);
    }
    .feedback-field {
      display: grid;
      gap: 4px;
    }
    .feedback-panel input:not([type='radio']),
    .feedback-panel textarea {
      width: 100%;
      min-width: 0;
      border: 1px solid var(--border);
      border-radius: var(--radius);
      background: var(--surface-control);
      color: var(--text);
      font: inherit;
    }
    .feedback-panel input:not([type='radio']) {
      height: 34px;
      padding: 0 9px;
    }
    .feedback-panel textarea {
      resize: vertical;
      padding: 7px 9px;
    }
    .feedback-panel input:focus-visible,
    .feedback-panel textarea:focus-visible,
    .feedback-close:focus-visible {
      outline: 2px solid var(--focus);
      outline-offset: 1px;
    }
    .feedback-note,
    .feedback-error,
    .feedback-success p {
      margin: 0;
      color: var(--text-6);
      font-size: 12px;
    }
    .feedback-error {
      color: var(--danger);
    }
    .feedback-primary {
      justify-self: start;
      min-height: 34px;
      padding: 7px 11px;
      border: 0;
      border-radius: var(--radius);
      background: var(--accent);
      color: var(--on-accent);
      font: inherit;
      font-weight: 600;
      text-decoration: none;
    }
    .feedback-primary:hover:not(:disabled),
    .feedback-primary:focus-visible {
      background: var(--accent-hover);
    }
    .feedback-primary:disabled {
      opacity: 0.5;
      cursor: default;
    }
    :global(:fullscreen) .feedback-tab,
    :global(:fullscreen) .feedback-panel,
    :global(html[data-compose]) .feedback-tab,
    :global(html[data-compose]) .feedback-panel {
      display: none;
    }
    @media (max-width: 760px) {
      .feedback-panel {
        left: 16px;
        right: 16px;
        bottom: calc(72px + env(safe-area-inset-bottom));
        width: auto;
        max-height: calc(100dvh - 88px - env(safe-area-inset-bottom));
      }
      .feedback-panel input:not([type='radio']),
      .feedback-panel textarea {
        font-size: 16px;
      }
    }
    :global(html[data-phone]) .feedback-panel input:not([type='radio']),
    :global(html[data-phone]) .feedback-panel textarea {
      font-size: 16px;
    }
    :global(html[data-phone]) .feedback-panel fieldset label {
      min-height: 44px;
    }
  }
</style>
