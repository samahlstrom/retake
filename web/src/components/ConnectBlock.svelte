<script lang="ts">
  // "Connect your computer": the one line to run where your footage is, with a copy button. The server mints the line
  // (POST api/connect); an older server gets the CLI line instead.
  let { command, title = 'Connect your computer' }: { command: string; title?: string } = $props();
  /** The space between two arguments: a text node of its own, so the line wraps there and never inside one. */
  const GAP = ' ';
  let copied = $state(false);
  let code = $state<HTMLElement | null>(null);

  async function copy() {
    try {
      await navigator.clipboard.writeText(command);
    } catch {
      // No clipboard API (plain http on a LAN address): copy the selected text the old way.
      const sel = getSelection();
      if (code && sel) {
        const range = document.createRange();
        range.selectNodeContents(code);
        sel.removeAllRanges();
        sel.addRange(range);
        document.execCommand('copy');
      }
    }
    copied = true;
    setTimeout(() => (copied = false), 1600);
  }
</script>

<div class="connect">
  <strong>{title}</strong>
  <p>
    Run this in your projects folder, on the computer with your footage. It connects out to this server; your footage
    stays where it is.
  </p>
  {#if command}
    <div class="cmd">
      <code bind:this={code}
        >{#each command.split(' ') as part, i (i)}{#if i}{GAP}{/if}<span class="arg">{part}</span>{/each}</code
      >
      <button onclick={copy} aria-label="Copy the command">{copied ? 'Copied' : 'Copy'}</button>
    </div>
  {:else}
    <div class="cmd loading"><code>Making your command…</code></div>
  {/if}
</div>

<style>
  .connect {
    display: grid;
    gap: 8px;
  }
  strong {
    font-size: 13.5px;
    font-weight: 600;
    color: var(--text, #ececef);
  }
  p {
    margin: 0;
    color: #a3a3ad;
    font-size: 12.5px;
    line-height: 1.5;
  }
  .cmd {
    display: flex;
    align-items: stretch;
    margin-top: 4px;
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 8px;
    background: #0c0c0f;
    overflow: hidden;
  }
  code {
    flex: 1;
    min-width: 0;
    padding: 9px 10px;
    font:
      12px/1.45 ui-monospace,
      'JetBrains Mono',
      Menlo,
      monospace;
    color: #e2e2e8;
    overflow-x: auto;
    user-select: all;
  }
  .arg {
    /* wrap between arguments only, never inside --token or the URL */
    white-space: nowrap;
  }
  .loading code {
    color: #7d7d88;
  }
  button {
    flex: none;
    padding: 0 12px;
    border: 0;
    border-left: 1px solid rgba(255, 255, 255, 0.1);
    background: rgba(255, 255, 255, 0.04);
    color: #e2e2e8;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }
  button:hover {
    background: rgba(255, 255, 255, 0.09);
  }
</style>
