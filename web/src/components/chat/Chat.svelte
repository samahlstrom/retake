<script lang="ts">
  // The chat with the agent (spec/chat.md), in the comments column of a kind's version that has one (a script): the
  // thread, then one quiet status line, then the composer. The person's messages on the right; the agent's words on
  // the left in the agent's colours (as its questions on a video), its tool use as one quiet line each, its
  // questions with choices to tap (pictures side by side, stacked on a phone). The composer: one line "Ask the
  // agent…", a quiet attach icon, the mic (a voice message: its words are sent) and the send arrow, which is a stop
  // square while the agent replies and nothing is typed. Enter sends, Shift+Enter is a new line. Images pasted,
  // dropped or picked are saved in the video's v0/images/ and go with the message. Nothing shifts: the status line
  // always has its room.
  import { tick } from 'svelte';
  import type { Review } from '../../lib/review.svelte';
  import { renderMarkdown } from '../../lib/chat/markdown';
  import { openQuestion, statusLine, type ChatMessage } from '../../lib/chat/thread';

  let { review }: { review: Review } = $props();

  const chat = $derived(review.chat);
  const view = $derived(chat.view);
  const turn = $derived(view.turn);
  const replying = $derived(!!turn && turn.state !== 'queued');
  const status = $derived(
    statusLine(view, review.automation.state, {
      cuts: review.cuts.map((v) => v.label),
      online: review.agent.online,
    }),
  );
  const asking = $derived(openQuestion(view));
  const canWrite = $derived(review.canRecord);

  let draft = $state('');
  /** Images saved for the next message: their paths in the video's folder. */
  let attached = $state<string[]>([]);
  let saving = $state(0);
  let box = $state<HTMLTextAreaElement | null>(null);
  let thread = $state<HTMLDivElement | null>(null);
  let picker = $state<HTMLInputElement | null>(null);
  let dragging = $state(false);

  $effect(() => {
    review.el.chatBox = box;
  });
  /** The box grows with what's typed, up to a few lines. */
  $effect(() => {
    void draft;
    if (!box) return;
    box.style.height = 'auto';
    box.style.height = `${Math.min(box.scrollHeight, 140)}px`;
  });

  // A phone's chat sheet stays above the on-screen keyboard: where the visible part of the page is (phone.css).
  $effect(() => {
    if (!review.kindDrawer || typeof window === 'undefined' || !window.visualViewport) return;
    const vv = window.visualViewport,
      root = document.documentElement;
    const place = () => {
      root.style.setProperty('--chat-top', `${Math.round(vv.offsetTop)}px`);
      root.style.setProperty('--chat-kb', `${Math.max(0, Math.round(innerHeight - (vv.offsetTop + vv.height)))}px`);
    };
    place();
    vv.addEventListener('resize', place);
    vv.addEventListener('scroll', place);
    return () => {
      vv.removeEventListener('resize', place);
      vv.removeEventListener('scroll', place);
      root.style.removeProperty('--chat-top');
      root.style.removeProperty('--chat-kb');
    };
  });

  // Follow the thread to its end while the reader is there.
  let atEnd = true;
  function scrolled() {
    if (!thread) return;
    atEnd = thread.scrollHeight - thread.scrollTop - thread.clientHeight < 48;
  }
  $effect(() => {
    void view.messages;
    void chat.voices;
    void status;
    if (!thread || !atEnd) return;
    void tick().then(() => thread?.scrollTo({ top: thread.scrollHeight }));
  });

  /** A file of the video's folder, or a link, as the page can show it. */
  const fileUrl = (path: string): string =>
    `api/ext/${encodeURIComponent(review.chatExt?.id ?? '')}/files/${path.split('/').map(encodeURIComponent).join('/')}`;
  /** An image in the agent's Markdown: relative to v0/ (as the document has them), the video's files, or https. */
  function image(src: string): string | null {
    if (/^https:\/\//i.test(src)) return src;
    const p = src.replace(/^\.\//, '');
    if (!p || p.includes('..') || /^[a-z]+:/i.test(p) || p.startsWith('/')) return null;
    return fileUrl(p.startsWith('v0/') ? p : `v0/${p}`);
  }

  /** The agent's messages start a group with its chip; the rest of the group follows it quietly. */
  const groupStart = (i: number): boolean => {
    const m = view.messages[i]!,
      prev = view.messages[i - 1];
    const agentish = (x?: ChatMessage) => !!x && (x.role === 'agent' || x.role === 'tool' || x.role === 'question');
    return agentish(m) && !agentish(prev);
  };

  async function attach(files: Iterable<File>) {
    for (const f of files) {
      if (!f.type.startsWith('image/')) continue;
      saving++;
      const path = await chat.saveImage(f);
      saving--;
      if (path) attached = [...attached, path];
    }
  }

  async function submit() {
    const text = draft.trim();
    if (!text || saving || chat.posting) return;
    const images = attached;
    if (await chat.post(text, { images, agent: review.chatAgent })) {
      draft = '';
      attached = [];
      atEnd = true;
    }
  }

  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      box?.blur();
      return;
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      void submit();
    }
  }

  function paste(e: ClipboardEvent) {
    const files = [...(e.clipboardData?.files ?? [])].filter((f) => f.type.startsWith('image/'));
    if (!files.length) return;
    e.preventDefault();
    void attach(files);
  }

  function drop(e: DragEvent) {
    dragging = false;
    const files = [...(e.dataTransfer?.files ?? [])].filter((f) => f.type.startsWith('image/'));
    if (!files.length || !canWrite) return;
    e.preventDefault();
    void attach(files);
  }

  async function choose(m: ChatMessage, text: string) {
    await chat.post(text, { answer: m.id, agent: review.chatAgent });
    atEnd = true;
  }
</script>

<section
  class="chat"
  class:dragging
  data-part="chat"
  aria-label="Chat with the agent"
  ondragover={(e) => {
    if (canWrite && e.dataTransfer?.types.includes('Files')) {
      e.preventDefault();
      dragging = true;
    }
  }}
  ondragleave={() => (dragging = false)}
  ondrop={drop}
>
  <div class="thread" data-part="chat-thread" bind:this={thread} onscroll={scrolled}>
    {#if chat.loaded && !view.messages.length && !chat.voices.length}
      <p class="empty">
        Ask the agent<span
          >It shapes the script while you watch: pull in a link, find an image, tighten the hook. When you like it,
          press Send to make the video.</span
        >
      </p>
    {/if}
    {#each view.messages as m, i (m.id)}
      {#if m.role === 'person'}
        <div class="msg person" data-part="chat-person" data-id={m.id}>
          {#if m.images?.length}
            <div class="pics">
              {#each m.images as p (p)}<a href={fileUrl(p)} target="_blank" rel="noopener"
                  ><img src={fileUrl(p)} alt="" loading="lazy" /></a
                >{/each}
            </div>
          {/if}
          <p class="words">
            {#if m.voice}<svg class="voice" viewBox="0 0 24 24" aria-label="Voice message"
                ><rect x="8.6" y="3" width="6.8" height="11.4" rx="3.4" /><path
                  d="M5.4 11.2a6.6 6.6 0 0 0 13.2 0M12 17.8V21"
                /></svg
              >{/if}{m.text}
          </p>
          {#if m.waiting}<span class="note">Next</span>{/if}
        </div>
      {:else if m.role === 'agent'}
        <div class="msg agent" class:start={groupStart(i)} data-part="chat-agent" data-id={m.id}>
          {#if groupStart(i)}<span class="agent-chip">Agent</span>{/if}
          <!-- eslint-disable-next-line svelte/no-at-html-tags -- renderMarkdown escapes everything it doesn't make -->
          <div class="md">{@html renderMarkdown(m.text, image)}</div>
        </div>
      {:else if m.role === 'tool'}
        <div class="msg tool" class:start={groupStart(i)} data-part="chat-tool">
          {#if groupStart(i)}<span class="agent-chip">Agent</span>{/if}
          <span class="line"><span class="tick" aria-hidden="true"></span>{m.text}</span>
        </div>
      {:else if m.role === 'question'}
        <div class="msg agent question" class:start={groupStart(i)} data-part="chat-question" data-id={m.id}>
          {#if groupStart(i)}<span class="agent-chip">Agent</span>{/if}
          <p class="ask">{m.text}</p>
          {#if m.images?.length}
            <div class="pics agent-pics">
              {#each m.images as u (u)}<a href={u} target="_blank" rel="noopener"><img src={u} alt="" /></a>{/each}
            </div>
          {/if}
          {#if m.options?.length}
            <div class="choices" class:pictures={m.options.some((o) => o.image?.url)} role="group" aria-label="Choices">
              {#each m.options as o (o.text)}
                <button
                  class="choice"
                  class:picked={m.answer === o.text}
                  disabled={!canWrite || asking !== m.id || chat.posting}
                  onclick={() => void choose(m, o.text)}
                >
                  {#if o.image?.url}<span class="pic"><img src={o.image.url} alt="" /></span>{/if}
                  <span class="choice-text">{o.text}</span>
                </button>
              {/each}
            </div>
          {/if}
        </div>
      {:else}
        <p class="msg info" data-part="chat-info">{m.text}</p>
      {/if}
    {/each}
    {#each chat.voices as v (v.id)}
      <div class="msg person pending" data-part="chat-voice">
        <p class="words">
          {v.state === 'saving' ? 'Saving your voice message…' : v.state === 'transcribing' ? 'Transcribing…' : v.text}
        </p>
        {#if v.state === 'failed'}<button class="link" onclick={() => chat.dismiss(v.id)}>Dismiss</button>{/if}
      </div>
    {/each}
  </div>

  <div class="status" data-part="chat-status" aria-live="polite">
    {#if status}<span class="pulse" class:still={!!view.busy || view.offline || !!view.blocked}></span>{status}{/if}
  </div>

  {#if canWrite}
    <form
      class="composer"
      data-part="chat-composer"
      onsubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      {#if attached.length || saving}
        <div class="attached">
          {#each attached as p (p)}
            <span class="thumb"
              ><img src={fileUrl(p)} alt="" /><button
                type="button"
                aria-label="Remove the image"
                onclick={() => (attached = attached.filter((x) => x !== p))}>×</button
              ></span
            >
          {/each}
          {#if saving}<span class="thumb wait" aria-label="Saving the image"></span>{/if}
        </div>
      {/if}
      <div class="row">
        <button
          type="button"
          class="icon"
          aria-label="Attach images"
          title="Attach images"
          onclick={() => picker?.click()}
          ><svg viewBox="0 0 24 24" aria-hidden="true"
            ><path
              d="M20 11.5 12.3 19.2a5 5 0 0 1-7.1-7.1l8.1-8.1a3.4 3.4 0 0 1 4.8 4.8l-8 8a1.7 1.7 0 0 1-2.4-2.4l7.3-7.3"
            /></svg
          ></button
        >
        <input
          bind:this={picker}
          type="file"
          accept="image/*"
          multiple
          hidden
          onchange={(e) => {
            const input = e.currentTarget;
            void attach([...(input.files ?? [])]).then(() => (input.value = ''));
          }}
        />
        <textarea
          bind:this={box}
          bind:value={draft}
          rows="1"
          placeholder="Ask the agent…"
          aria-label="Ask the agent"
          enterkeyhint="send"
          onkeydown={key}
          onpaste={paste}></textarea>
        <button
          type="button"
          class="icon mic"
          class:on={!!review.seg}
          aria-label={review.seg ? 'Stop and send the voice message' : 'Record a voice message (R)'}
          title={review.seg ? `Stop · ${review.recElapsed}` : 'Voice message (R)'}
          onclick={review.toggleRec}
          ><svg viewBox="0 0 24 24" aria-hidden="true"
            >{#if review.seg}<rect x="7" y="7" width="10" height="10" rx="2" class="fill" />{:else}<rect
                x="8.6"
                y="3"
                width="6.8"
                height="11.4"
                rx="3.4"
              /><path d="M5.4 11.2a6.6 6.6 0 0 0 13.2 0M12 17.8V21" />{/if}</svg
          ></button
        >
        {#if replying && !draft.trim() && !attached.length}
          <button
            type="button"
            class="icon go stop"
            data-part="chat-stop"
            aria-label="Stop the agent"
            title="Stop"
            disabled={turn?.state === 'stopping'}
            onclick={() => void chat.stop()}
            ><svg viewBox="0 0 24 24" aria-hidden="true"
              ><rect x="7" y="7" width="10" height="10" rx="2" class="fill" /></svg
            ></button
          >
        {:else}
          <button
            type="submit"
            class="icon go"
            data-part="chat-send"
            aria-label="Send to the agent"
            title="Send (Enter)"
            disabled={!draft.trim() || !!saving || chat.posting}
            ><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M5.5 11.5 12 5l6.5 6.5" /></svg></button
          >
        {/if}
      </div>
    </form>
  {/if}
</section>

<style>
  @layer retake {
    .chat {
      display: flex;
      flex-direction: column;
      flex: 1;
      min-height: 0;
      position: relative;
    }
    .chat.dragging::after {
      content: '';
      position: absolute;
      inset: 6px;
      border: 1.5px dashed color-mix(in srgb, var(--agent) 70%, transparent);
      border-radius: var(--radius-md);
      pointer-events: none;
    }
    .thread {
      flex: 1;
      min-height: 0;
      overflow-y: auto;
      overscroll-behavior: contain;
      padding: 14px 14px 6px;
      display: flex;
      flex-direction: column;
      gap: 10px;
    }
    .empty {
      margin: auto 8px;
      color: var(--text-6);
      font-size: 14px;
      text-align: center;
    }
    .empty span {
      display: block;
      margin-top: 6px;
      color: var(--faint);
      font-size: 12.5px;
      line-height: 1.5;
    }
    .msg {
      max-width: 100%;
      font-size: 13.5px;
      line-height: 1.5;
      color: var(--text);
      overflow-wrap: anywhere;
    }
    .person {
      align-self: flex-end;
      max-width: 86%;
      padding: 8px 12px;
      border-radius: 14px 14px 4px 14px;
      background: color-mix(in srgb, var(--overlay) 8%, transparent);
    }
    .person.pending {
      color: var(--muted);
    }
    .person .words {
      margin: 0;
      white-space: pre-wrap;
    }
    .person .note {
      display: block;
      margin-top: 2px;
      color: var(--faint);
      font-size: 11px;
      text-align: right;
    }
    .voice {
      width: 13px;
      height: 13px;
      margin-right: 5px;
      vertical-align: -2px;
      fill: none;
      stroke: currentColor;
      stroke-width: 1.9;
      stroke-linecap: round;
      color: var(--muted);
    }
    .pics {
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
      margin-bottom: 6px;
    }
    .pics a {
      display: block;
      width: 96px;
      border-radius: 8px;
      overflow: hidden;
    }
    .pics img {
      display: block;
      width: 100%;
      height: 72px;
      object-fit: cover;
    }
    .agent,
    .tool {
      align-self: stretch;
      padding-left: 12px;
      border-left: 2px solid color-mix(in srgb, var(--agent) 45%, transparent);
    }
    .agent.start,
    .tool.start {
      margin-top: 4px;
    }
    /* One agent reply is one group: its rule runs on unbroken. */
    .agent:not(.start),
    .tool:not(.start) {
      margin-top: -10px;
      padding-top: 6px;
    }
    .agent-chip {
      display: inline-block;
      margin: 0 0 4px;
      padding: 1px 7px;
      border-radius: 999px;
      background: var(--agent);
      color: var(--agent-ink);
      font-size: 10.5px;
      font-weight: 700;
      letter-spacing: 0.02em;
    }
    .start .agent-chip {
      display: table;
    }
    .md :global(p) {
      margin: 0 0 8px;
    }
    .md :global(p:last-child) {
      margin-bottom: 0;
    }
    .md :global(ul),
    .md :global(ol) {
      margin: 0 0 8px;
      padding-left: 20px;
    }
    .md :global(code) {
      padding: 1px 4px;
      border-radius: 4px;
      background: color-mix(in srgb, var(--overlay) 8%, transparent);
      font-family: var(--font-mono);
      font-size: 0.92em;
    }
    .md :global(pre) {
      margin: 0 0 8px;
      padding: 8px 10px;
      overflow-x: auto;
      border-radius: 8px;
      background: color-mix(in srgb, var(--overlay) 6%, transparent);
    }
    .md :global(pre code) {
      padding: 0;
      background: none;
    }
    .md :global(blockquote) {
      margin: 0 0 8px;
      padding-left: 10px;
      border-left: 2px solid var(--line-strong);
      color: var(--muted);
    }
    .md :global(a) {
      color: var(--agent-text);
      text-decoration: underline;
      text-underline-offset: 2px;
    }
    .md :global(img) {
      display: block;
      max-width: 100%;
      max-height: 220px;
      margin: 6px 0;
      border-radius: 8px;
    }
    .md :global(hr) {
      border: 0;
      border-top: 1px solid var(--line);
    }
    .tool .line {
      display: flex;
      align-items: center;
      gap: 7px;
      color: var(--faint);
      font-size: 12px;
    }
    .tool .tick {
      flex: none;
      width: 5px;
      height: 5px;
      border-radius: 50%;
      background: color-mix(in srgb, var(--agent) 60%, transparent);
    }
    .question .ask {
      margin: 0;
    }
    .agent-pics img {
      height: auto;
      max-height: 140px;
      object-fit: contain;
    }
    .choices {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-top: 8px;
    }
    .choices.pictures {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
    }
    :global(html[data-phone]) .choices.pictures {
      grid-template-columns: minmax(0, 1fr);
    }
    .choice {
      display: flex;
      flex-direction: column;
      align-items: stretch;
      gap: 6px;
      min-width: 0;
      padding: 7px 12px;
      border: 1px solid color-mix(in srgb, var(--agent) 45%, transparent);
      border-radius: 9px;
      background: color-mix(in srgb, var(--overlay) 3%, transparent);
      color: var(--text);
      font: inherit;
      font-size: 13px;
      cursor: pointer;
    }
    .pictures .choice {
      padding: 6px;
    }
    .choice:hover:not(:disabled),
    .choice:focus-visible {
      border-color: var(--agent);
      background: color-mix(in srgb, var(--agent) 14%, transparent);
    }
    .choice:disabled {
      cursor: default;
      opacity: 0.55;
    }
    .choice.picked {
      opacity: 1;
      border-color: var(--agent);
      background: color-mix(in srgb, var(--agent) 18%, transparent);
    }
    .choice .pic {
      display: block;
      border-radius: 6px;
      overflow: hidden;
      background: var(--surface-sunken);
    }
    .choice .pic img {
      display: block;
      width: 100%;
      height: auto;
      max-height: 160px;
      object-fit: contain;
    }
    .choice-text {
      text-align: center;
    }
    :global(html[data-phone]) .choice {
      min-height: 44px;
    }
    .info {
      align-self: center;
      margin: 0;
      color: var(--faint);
      font-size: 12px;
    }
    .link {
      margin-top: 4px;
      padding: 0;
      border: 0;
      background: none;
      color: var(--muted);
      font: inherit;
      font-size: 12px;
      text-decoration: underline;
      cursor: pointer;
    }
    .status {
      flex: none;
      display: flex;
      align-items: center;
      gap: 8px;
      height: 24px;
      padding: 0 16px;
      overflow: hidden;
      color: var(--faint);
      font-size: 12px;
      white-space: nowrap;
      text-overflow: ellipsis;
    }
    .pulse {
      flex: none;
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: var(--agent);
      animation: chat-pulse 1.4s ease-in-out infinite;
    }
    .pulse.still {
      animation: none;
      background: var(--faint);
    }
    @keyframes chat-pulse {
      50% {
        opacity: 0.35;
      }
    }
    @media (prefers-reduced-motion: reduce) {
      .pulse {
        animation: none;
      }
    }
    .composer {
      flex: none;
      padding: 6px 10px max(10px, env(safe-area-inset-bottom));
      border-top: 1px solid var(--line);
    }
    .row {
      display: flex;
      align-items: flex-end;
      gap: 2px;
      padding: 3px;
      border: 1px solid color-mix(in srgb, var(--overlay) 12%, transparent);
      border-radius: 20px;
      background: color-mix(in srgb, var(--overlay) 4%, transparent);
    }
    .row:focus-within {
      border-color: color-mix(in srgb, var(--focus) 55%, transparent);
    }
    textarea {
      flex: 1;
      min-width: 0;
      min-height: 34px;
      max-height: 140px;
      margin: 0;
      padding: 7px 4px;
      border: 0;
      outline: 0;
      resize: none;
      background: none;
      color: var(--text);
      font: inherit;
      font-size: 14px;
      line-height: 20px;
    }
    textarea::placeholder {
      color: var(--faint);
    }
    :global(html[data-phone]) textarea {
      font-size: 16px; /* iOS doesn't zoom into a 16 px field */
    }
    .icon {
      flex: none;
      display: grid;
      place-items: center;
      width: 34px;
      height: 34px;
      padding: 0;
      border: 0;
      border-radius: 50%;
      background: none;
      color: var(--muted);
      cursor: pointer;
    }
    :global(html[data-phone]) .icon {
      width: 40px;
      height: 40px;
    }
    .icon:hover:not(:disabled) {
      color: var(--text);
    }
    .icon svg {
      width: 19px;
      height: 19px;
      fill: none;
      stroke: currentColor;
      stroke-width: 1.8;
      stroke-linecap: round;
      stroke-linejoin: round;
    }
    .icon svg .fill {
      fill: currentColor;
      stroke: none;
    }
    .mic.on {
      color: var(--record);
    }
    .go {
      background: var(--text);
      color: var(--surface);
    }
    .go:hover:not(:disabled) {
      color: var(--surface);
    }
    .go:disabled {
      background: color-mix(in srgb, var(--overlay) 14%, transparent);
      color: var(--faint);
      cursor: default;
    }
    .go svg {
      width: 17px;
      height: 17px;
      stroke-width: 2.2;
    }
    .attached {
      display: flex;
      gap: 6px;
      padding: 2px 2px 6px;
      overflow-x: auto;
    }
    .thumb {
      position: relative;
      flex: none;
      width: 52px;
      height: 52px;
      border-radius: 8px;
      overflow: hidden;
      background: color-mix(in srgb, var(--overlay) 8%, transparent);
    }
    .thumb img {
      width: 100%;
      height: 100%;
      object-fit: cover;
    }
    .thumb button {
      position: absolute;
      top: 2px;
      right: 2px;
      width: 20px;
      height: 20px;
      padding: 0;
      border: 0;
      border-radius: 50%;
      background: color-mix(in srgb, var(--surface) 80%, transparent);
      color: var(--text);
      font-size: 14px;
      line-height: 20px;
      cursor: pointer;
    }
    .thumb.wait {
      animation: chat-pulse 1.4s ease-in-out infinite;
    }
  }
</style>
