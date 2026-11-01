<script lang="ts">
  // The one way to answer the agent, under its question. A worker's question takes typed words (POST …/answer); an
  // agent that asked through the notes (MCP) listens to a recorded answer, exactly like R with the segment pointing at
  // its question; a question from inside a task (retake ask) takes both, so the field has a mic beside its arrow. Same
  // field either way; it offers what that question can take, never a control that would fail.
  let {
    onsend,
    onmic,
    recording = false,
    busy = false,
  }: {
    onsend?: (text: string) => Promise<boolean>;
    onmic?: () => void;
    recording?: boolean;
    busy?: boolean;
  } = $props();
  let text = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    const t = text.trim();
    if (!t || busy || !onsend) return;
    if (await onsend(t)) text = '';
  }
</script>

{#if onsend}
  <form class="reply" data-part="reply-box" onsubmit={submit}>
    <input aria-label="Answer the agent" placeholder="Type your answer…" bind:value={text} enterkeyhint="send" />
    {#if onmic}
      <button
        class="go mic"
        type="button"
        class:on={recording}
        aria-label={recording ? 'Stop answering' : 'Answer out loud'}
        title={recording ? 'Stop answering' : 'Answer out loud (you can draw while you talk)'}
        onclick={onmic}
        >{#if recording}<span class="stop" aria-hidden="true"></span>{:else}<svg viewBox="0 0 20 20" aria-hidden="true"
            ><path
              d="M10 2.5a2.5 2.5 0 0 0-2.5 2.5v5a2.5 2.5 0 0 0 5 0V5A2.5 2.5 0 0 0 10 2.5ZM5 9.5a5 5 0 0 0 10 0M10 14.5v3"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
            /></svg
          >{/if}</button
      >
    {/if}
    <button class="go" type="submit" aria-label="Send answer" title="Send answer (Enter)" class:ready={!!text.trim()}
      ><svg viewBox="0 0 20 20" aria-hidden="true"
        ><path
          d="M4 10h11M11 5.5 15.5 10 11 14.5"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
        /></svg
      ></button
    >
  </form>
{:else if onmic}
  <button class="reply answer" data-part="reply-box" class:on={recording} onclick={onmic}
    ><span>{recording ? 'Stop answering' : 'Answer out loud'}</span>{#if recording}<span class="stop" aria-hidden="true"
      ></span>{:else}<svg viewBox="0 0 20 20" aria-hidden="true"
        ><path
          d="M10 2.5a2.5 2.5 0 0 0-2.5 2.5v5a2.5 2.5 0 0 0 5 0V5A2.5 2.5 0 0 0 10 2.5ZM5 9.5a5 5 0 0 0 10 0M10 14.5v3"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        /></svg
      >{/if}</button
  >
{/if}

<style>
  @layer retake {
    .reply {
      box-sizing: border-box;
      display: flex;
      align-items: center;
      width: 100%;
      height: 38px;
      margin-top: 10px;
      padding: 0 6px 0 12px;
      border: 1px solid color-mix(in srgb, var(--overlay) 12%, transparent);
      border-radius: var(--radius-md);
      background: color-mix(in srgb, var(--overlay) 3%, transparent);
      color: var(--text);
      font: inherit;
      font-size: 13px;
      transition: border-color 0.15s ease;
    }
    .reply:focus-within,
    button.reply:hover {
      border-color: color-mix(in srgb, var(--focus) 55%, transparent);
    }
    input {
      flex: 1;
      min-width: 0;
      height: 100%;
      padding: 0;
      border: 0;
      outline: 0;
      background: none;
      color: inherit;
      font: inherit;
      user-select: text;
    }
    input::placeholder {
      color: var(--dim);
    }
    .go {
      flex: none;
      display: grid;
      place-items: center;
      width: 28px;
      height: 28px;
      padding: 0;
      border: 0;
      border-radius: 7px;
      background: transparent;
      color: var(--faint);
      cursor: pointer;
    }
    .go.ready {
      color: var(--agent-text);
    }
    .go.mic {
      color: color-mix(in srgb, var(--agent) 30%, var(--text-6));
    }
    .go.mic:hover {
      color: var(--agent-text);
    }
    .go.mic.on {
      background: color-mix(in srgb, var(--record) 14%, transparent);
    }
    .go svg,
    button.reply svg {
      width: 18px;
      height: 18px;
    }
    button.reply {
      justify-content: space-between;
      padding: 0 10px 0 12px;
      color: color-mix(in srgb, var(--agent) 30%, var(--text-3));
      cursor: pointer;
      text-align: left;
    }
    button.reply.on {
      border-color: color-mix(in srgb, var(--record) 55%, transparent);
      background: color-mix(in srgb, var(--record) 10%, transparent);
      color: var(--record-text);
    }
    .stop {
      width: 10px;
      height: 10px;
      margin-right: 4px;
      border-radius: 2px;
      background: var(--record);
    }
    :global(html[data-phone]) .reply {
      height: 44px;
    }
  }
</style>
