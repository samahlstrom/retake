<script lang="ts">
  // The version slot: v0's editor (spec/ignition.md, "The editor"). Tabs for the documents, Refs and Voice; the text;
  // the ask box under it. Core keeps the bar (Record, Send, the agent) and the comments column.
  import { onMount, tick, untrack } from 'svelte';
  import type { Ctx, Edit, Item } from '../types';
  import { Doc } from './doc.svelte';
  import TextPane, { type Mark } from './TextPane.svelte';
  import Refs from './Refs.svelte';
  import Voice from './Voice.svelte';
  import { talkOut } from './talkout';
  import {
    anchorAt,
    findAnchor,
    headingAbove,
    headings,
    insertBlock,
    talkOutBlock,
    waitingTalkOuts,
    wordsText,
    type Heading,
  } from '../lib/doc';
  import { chips as chipsOf, labelOfCutFile, parseCut, type Chip, type Cut } from '../lib/cuts';
  import { isChanged, sayError } from '../lib/errors';
  import { NEW_KEY, TALK_KEY, takeHandOff } from '../lib/ideas';
  import { DOCS, docName, loadPrefs, savePrefs, storageOf, type Tab } from '../lib/prefs';
  import { ago } from '../lib/stamp';
  import { hasCut as cutIn, START_V1 } from '../lib/versions';

  let { ctx }: { ctx: Ctx } = $props();

  const c = untrack(() => ctx);
  const files = c.files();
  const comments = c.comments();
  const video = c.video?.name ?? '';
  const canWrite = c.role !== 'viewer';
  /** Hosted: the video's editing computer doesn't have Ignition, so its files can't be read there. */
  const noMachine = c.ext.machine === false;
  const local = storageOf('local');
  const prefs = loadPrefs(local, video);
  const session = storageOf('session');
  const talkOnOpen = takeHandOff(session, TALK_KEY, video);
  const fresh = talkOnOpen || takeHandOff(session, NEW_KEY, video);
  const mac = typeof navigator !== 'undefined' && /Mac|iP(hone|ad)/.test(navigator.platform);

  const TABS: { id: Tab; label: string }[] = [
    { id: 'idea', label: 'Idea' },
    { id: 'outline', label: 'Outline' },
    { id: 'script', label: 'Script' },
    { id: 'refs', label: 'Refs' },
    { id: 'voice', label: 'Voice' },
  ];

  // ---------------------------------------------------------------- the documents
  const docs = new Map<string, Doc>();
  function docAt(path: string): Doc {
    let d = docs.get(path);
    if (!d) {
      d = new Doc(files, path);
      docs.set(path, d);
    }
    return d;
  }
  for (const p of Object.values(DOCS)) docAt(p);
  if (prefs.ref) docAt(prefs.ref);

  let tab = $state<Tab>(fresh ? 'idea' : prefs.tab);
  let ref = $state<string | null>(fresh ? null : prefs.ref);
  const carets = prefs.carets;
  if (fresh) carets[DOCS.idea] = Number.MAX_SAFE_INTEGER;

  const docPath = $derived(tab === 'refs' ? ref : tab === 'voice' ? null : DOCS[tab]);
  const doc = $derived(docPath ? (docs.get(docPath) ?? null) : null);
  /** The last document shown: where Insert at caret puts a talk-out. */
  let lastDoc = $state<string>(DOCS.idea);
  let caret = $state(0);
  /** Focus the text when it shows: on open (typing goes to the text), not when a talk-out starts it. */
  let focusText = $state(!talkOnOpen);

  $effect(() => {
    const p = docPath;
    if (!p) return;
    untrack(() => {
      lastDoc = p;
      caret = carets[p] ?? 0;
    });
  });

  let prefsTimer: ReturnType<typeof setTimeout> | null = null;
  function keepPrefs(): void {
    if (prefsTimer) clearTimeout(prefsTimer);
    prefsTimer = setTimeout(() => {
      prefsTimer = null;
      savePrefs(local, video, { tab, ref, carets });
    }, 400);
  }
  $effect(() => {
    void tab;
    void ref;
    untrack(keepPrefs);
  });

  function show(t: Tab): void {
    if (t === tab && t === 'refs') ref = null;
    tab = t;
    focusText = true;
  }

  function openRef(path: string): void {
    docAt(path);
    ref = path;
    tab = 'refs';
    focusText = true;
  }

  function oncaret(pos: number): void {
    caret = pos;
    if (docPath) carets[docPath] = pos;
    keepPrefs();
  }

  // ---------------------------------------------------------------- comments and questions
  let items = $state.raw<Item[]>([]);
  let selected = $state<Mark | null>(null);

  async function loadItems(): Promise<void> {
    try {
      items = await comments.list(c.version?.label);
      if (selected && !items.some((i) => i.id === selected!.item && (i.ask || i.kind === 'question'))) selected = null;
    } catch {
      /* the column says what's wrong */
    }
  }

  const marks = $derived.by(() => {
    const out = new Map<number, Mark[]>();
    if (!doc?.loaded || !docPath) return out;
    for (const it of items) {
      const open = it.ask ?? (it.kind === 'question' && it.status === 'question' ? it : null);
      const a = it.ask?.anchor ?? (open ? it.anchor : null);
      if (!open || !a || a.file !== docPath) continue;
      const line = findAnchor(doc.text, a);
      if (line < 0) continue;
      const m: Mark = { item: it.id, thread: it.ask?.id ?? it.id, text: open.text };
      out.set(line, [...(out.get(line) ?? []), m]);
    }
    return out;
  });

  function pick(m: Mark): void {
    selected = selected?.item === m.item ? null : m;
    if (selected) c.selectComment?.(m.item);
  }

  // ---------------------------------------------------------------- cuts: the script's chips
  let cuts = $state.raw<Cut[]>([]);
  async function loadCuts(): Promise<void> {
    try {
      const list = (await files.list('v0/cuts/*.json')).filter((f) => !f.deleted);
      const read = await Promise.all(
        list.map(async (f) => {
          try {
            return parseCut(JSON.parse(await (await files.read(f.path)).text()), labelOfCutFile(f.path) ?? '');
          } catch {
            return null;
          }
        }),
      );
      cuts = read.filter((x): x is Cut => x !== null);
    } catch {
      cuts = [];
    }
  }

  const chips = $derived.by(() => {
    const out = new Map<number, Chip>();
    if (docPath !== DOCS.script || !doc?.loaded || !cuts.length) return out;
    const hs = headings(doc.text).filter((h) => h.level === 2 && h.text);
    const by = chipsOf(
      hs.map((h) => h.text),
      cuts,
    );
    for (const h of hs) {
      const chip = by.get(h.text);
      if (chip) out.set(h.line, chip);
    }
    return out;
  });

  // ---------------------------------------------------------------- Start v1: while the video has no cut
  let hasCut = $state<boolean | null>(null);
  async function loadInfo(): Promise<void> {
    try {
      const r = await fetch('api/info', { credentials: 'same-origin' });
      if (r.ok) hasCut = cutIn(await r.json());
    } catch {
      /* unknown: Start v1 stays hidden */
    }
  }

  // ---------------------------------------------------------------- the agent's edits: Undo
  let edit = $state<Edit | null>(null);
  let undoNote = $state('');
  let now = $state(Date.now());
  const ms = (at: number): number => (at < 1e12 ? at * 1000 : at);

  async function loadHistory(): Promise<void> {
    const p = docPath;
    if (!p || !files.history) {
      edit = null;
      return;
    }
    try {
      const list = await files.history(p);
      if (p === docPath) edit = list.find((e) => e.path === p) ?? null;
    } catch {
      edit = null;
    }
  }
  $effect(() => {
    void docPath;
    untrack(() => {
      undoNote = '';
      void loadHistory();
    });
  });

  async function undo(): Promise<void> {
    const d = doc,
      e = edit;
    if (!d || !e || !files.undo) return;
    await d.flush();
    try {
      await files.undo(e.id, e.path);
      undoNote = '';
      await d.reload(true);
    } catch (err) {
      undoNote = isChanged(err) ? "Changed since the agent's edit" : sayError(err);
    }
    void loadHistory();
  }

  // ---------------------------------------------------------------- refs and voice: reload when they change
  let refsRev = $state(0);
  let voiceRev = $state(0);

  // ---------------------------------------------------------------- recording: talk-outs
  let recording = $state(untrack(() => c.recording?.() ?? false));

  function target(): { doc: Doc; pos: number } {
    if (docPath) return { doc: docAt(docPath), pos: carets[docPath] ?? 0 };
    return { doc: docAt(DOCS.idea), pos: Number.MAX_SAFE_INTEGER };
  }

  function startTalk(): void {
    if (c.recording?.()) return;
    // Core's R: the page's key handler takes it when no text field has the focus.
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    document.body.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'r', code: 'KeyR', bubbles: true, cancelable: true }),
    );
  }

  /** Insert at caret (the Voice tab): into the last document shown, at its caret, and show it. */
  async function insertTalk(rel: string, text: string): Promise<void> {
    const d = docAt(lastDoc);
    await d.ready;
    const pos = Math.min(carets[lastDoc] ?? d.text.length, d.text.length);
    const r = insertBlock(d.text, pos, talkOutBlock(rel, text));
    carets[lastDoc] = r.caret;
    d.input(r.text);
    await d.flush();
    if (lastDoc.startsWith('v0/refs/')) openRef(lastDoc);
    else tab = (Object.entries(DOCS).find(([, p]) => p === lastDoc)?.[0] as Tab) ?? 'idea';
  }

  /**
   * Talk-outs whose words came while no page waited for them (the page was left or reloaded): fill them in from
   * voice/<stamp>.md, or from the words file the transcription left (and write the .md). Runs on open and whenever
   * voice/ changes.
   */
  let resuming = false;
  let resumeAgain = false;
  async function resumeTalkOuts(): Promise<void> {
    if (!canWrite) return;
    if (resuming) {
      resumeAgain = true;
      return;
    }
    resuming = true;
    try {
      for (const p of Object.values(DOCS)) {
        const d = docAt(p);
        await d.ready;
        for (const rel of waitingTalkOuts(d.text)) {
          const base = `v0/${rel.replace(/\.[^./]+$/, '')}`;
          let text = '';
          try {
            text = (await (await files.read(`${base}.md`)).text()).trim();
          } catch {
            try {
              text = wordsText(JSON.parse(await (await files.read(`${base}.words.json`)).text()));
              if (text) await files.write(`${base}.md`, `${text}\n`, { ifMatch: '*' });
            } catch {
              /* still on its way: the watch brings it */
            }
          }
          if (text) await d.fillTalkOut(rel, text);
        }
      }
    } finally {
      resuming = false;
    }
    if (resumeAgain) {
      resumeAgain = false;
      await resumeTalkOuts();
    }
  }

  // ---------------------------------------------------------------- the ask box
  let askText = $state('');
  let whole = $state(false);
  let wants = $state(false);
  let asking = $state(false);
  let askEl = $state<HTMLTextAreaElement>();

  const heading = $derived<Heading | null>(doc?.loaded ? headingAbove(doc.text, caret) : null);
  $effect(() => {
    void docPath;
    untrack(() => (whole = false));
  });

  const about = $derived.by(() => {
    if (wants) return 'Start v1: the agent builds the first cut from the script';
    if (!docPath || !doc) return 'About: v0';
    if (whole || !doc.loaded) return `About: ${docName(docPath)}`;
    if (heading?.text) return `About: ${heading.text}`;
    const q = anchorAt(docPath, doc.text, caret).quote;
    return q ? `About: “${q.length > 48 ? `${q.slice(0, 47)}…` : q}”` : `About: ${docName(docPath)}`;
  });
  const canNarrow = $derived(!wants && !!docPath && !whole);

  function startV1(): void {
    wants = true;
    askText = START_V1;
    void tick().then(() => {
      askEl?.focus();
      askEl?.setSelectionRange(askText.length, askText.length);
    });
  }

  async function submit(send: boolean): Promise<void> {
    const text = askText.trim();
    if (!text || asking) return;
    asking = true;
    try {
      const anchor =
        wants || !docPath || !doc?.loaded
          ? undefined
          : whole
            ? { file: docPath, line: 1 }
            : anchorAt(docPath, doc.text, caret);
      const it = await comments.add({ text, ...(anchor ? { anchor } : {}), ...(wants ? { wants: 'version' } : {}) });
      askText = '';
      wants = false;
      whole = false;
      if (send) await comments.send([it.id]);
      void loadItems();
    } catch (e) {
      c.toast(sayError(e));
    } finally {
      asking = false;
    }
  }

  function askKey(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      askEl?.blur();
    } else if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      void submit(e.ctrlKey || e.metaKey);
    }
  }

  // ---------------------------------------------------------------- wiring
  onMount(() => {
    void loadItems();
    void loadCuts();
    void loadInfo();
    void resumeTalkOuts();
    const offs: (() => void)[] = [];
    offs.push(
      files.watch((changed) => {
        let cutsChanged = false;
        for (const f of changed) {
          docs.get(f.path)?.outside(f);
          if (f.path.startsWith('v0/cuts/')) cutsChanged = true;
          if (f.path.startsWith('v0/refs/')) refsRev++;
          if (f.path.startsWith('v0/voice/')) {
            voiceRev++;
            if (/\.(md|words\.json)$/.test(f.path)) void resumeTalkOuts();
          }
          if (f.path === docPath) void loadHistory();
        }
        if (cutsChanged) void loadCuts();
      }),
    );
    offs.push(c.on('notes', () => void loadItems()));
    offs.push(c.on('version', () => void loadInfo()));
    offs.push(c.on('job', () => void loadHistory()));
    if (c.onRecording) offs.push(c.onRecording((on) => (recording = on)));
    offs.push(
      c.onRecorded((a) => {
        void talkOut(a, {
          files,
          comments,
          transcribe: (path) => c.transcribe(path),
          toast: (t) => c.toast(t),
          target,
          question: () => selected?.thread ?? null,
          answered: () => {
            selected = null;
            void loadItems();
          },
        }).then(() => void loadItems());
      }),
    );
    const clock = setInterval(() => (now = Date.now()), 30_000);
    const unsaved = (e: BeforeUnloadEvent) => {
      for (const d of docs.values()) {
        if (d.pending || (d.dirty && !d.loadError)) {
          void d.flush();
          e.preventDefault();
        }
      }
    };
    const leaving = () => {
      if (prefsTimer) clearTimeout(prefsTimer);
      prefsTimer = null;
      savePrefs(local, video, { tab, ref, carets });
    };
    window.addEventListener('beforeunload', unsaved);
    window.addEventListener('pagehide', leaving);
    if (talkOnOpen) setTimeout(startTalk, 400);
    return () => {
      for (const off of offs) off();
      clearInterval(clock);
      window.removeEventListener('beforeunload', unsaved);
      window.removeEventListener('pagehide', leaving);
      leaving();
      for (const d of docs.values()) d.dispose();
    };
  });

  const word = $derived(doc ? doc.word : '');
  const label = $derived(docPath ? docName(docPath).replace(/^the /, '') : '');
</script>

<div class={['ig-ed', recording && 'ig-recording']} data-part="ignition-editor">
  <div class="ig-bar" data-part="ignition-header">
    <div class="ig-tabs" role="tablist" aria-label="v0">
      {#each TABS as t (t.id)}
        <button
          role="tab"
          class={['ig-tab', tab === t.id && 'ig-on']}
          aria-selected={tab === t.id}
          data-part="ignition-tab"
          data-tab={t.id}
          onclick={() => show(t.id)}>{t.label}</button
        >
      {/each}
    </div>
    <span class="ig-grow"></span>
    {#if edit && docPath && canWrite}
      <span class="ig-agent-edit" data-part="ignition-agent-edit">
        <span>{undoNote || `Agent edited ${ago(ms(edit.at), now)}`}</span>
        {#if !undoNote}·<button class="ig-link" onclick={undo}>Undo</button>{/if}
      </span>
    {/if}
    {#if hasCut === false && canWrite}
      <button class="ig-start" data-part="ignition-start-v1" onclick={startV1}>Start v1</button>
    {/if}
    <span class="ig-word" data-part="ignition-saved" aria-live="polite">{word}</span>
  </div>

  <div class="ig-body">
    {#if noMachine}
      <div class="ig-scroll">
        <div class="ig-col">
          <p class="ig-offline" data-part="ignition-no-machine">
            The editing computer doesn't have Ignition. Run <code>retake ext enable ignition</code> there.
          </p>
        </div>
      </div>
    {:else if doc}
      {#if doc.conflict}
        <div class="ig-note" data-part="ignition-outside">
          Changed outside Retake ·
          <button class="ig-link" onclick={() => void doc.reload(true)}>Reload</button> ·
          <button class="ig-link" onclick={() => void doc.keepMine()}>Keep mine</button>
        </div>
      {:else if doc.error}
        <div class="ig-note ig-note-err" data-part="ignition-not-saved">
          Not saved: {doc.error} ·
          <button class="ig-link" onclick={() => void doc.save()}>Retry</button>
        </div>
      {/if}
      <div class="ig-scroll">
        <div class="ig-col">
          {#if tab === 'refs'}
            <p class="ig-crumb">
              <button class="ig-link" onclick={() => (ref = null)}>refs</button> / {docPath?.slice('v0/refs/'.length)}
            </p>
          {/if}
          {#if !doc.loaded}
            <p class="ig-quiet ig-loading">Opening…</p>
          {:else if doc.loadError}
            <p class="ig-offline" data-part="ignition-offline">
              {doc.loadError} ·
              <button class="ig-link" onclick={() => void doc.retry()}>Try again</button>
            </p>
          {:else}
            {#key doc.path}
              <TextPane
                {doc}
                readonly={!canWrite}
                caret={carets[doc.path] ?? 0}
                focus={focusText && canWrite}
                {label}
                {marks}
                {chips}
                selected={selected?.item ?? null}
                {oncaret}
                onmark={pick}
                onchip={(chip) => c.navigate({ version: chip.version, t: chip.t })}
              />
            {/key}
          {/if}
        </div>
      </div>
    {:else if tab === 'refs'}
      <div class="ig-scroll">
        <Refs {files} rev={refsRev} readonly={!canWrite} toast={(t) => c.toast(t)} onopen={openRef} />
      </div>
    {:else}
      <div class="ig-scroll">
        <Voice {files} rev={voiceRev} readonly={!canWrite} oninsert={(rel, text) => void insertTalk(rel, text)} />
      </div>
    {/if}
  </div>

  {#if canWrite}
    <div class="ig-ask" data-part="ignition-ask">
      <div class="ig-col">
        {#if selected}
          <!-- A selected question takes the About line's place, so nothing moves. -->
          <p class="ig-answering" data-part="ignition-answering">
            <span class="ig-qdot"></span>
            <span class="ig-answering-text">{recording ? 'Answering' : 'Talk to answer'}: {selected.text}</span>
            <button class="ig-x" aria-label="Don't answer it" title="Don't answer it" onclick={() => (selected = null)}
              >×</button
            >
          </p>
        {:else}
          <p class="ig-about" data-part="ignition-about">
            <span class="ig-about-text">{about}</span>
            {#if canNarrow}
              <button
                class="ig-x"
                aria-label="About the whole document"
                title="About the whole document"
                onclick={() => (whole = true)}>×</button
              >
            {:else if wants}
              <button
                class="ig-x"
                aria-label="Don't start v1"
                title="Don't start v1"
                onclick={() => {
                  wants = false;
                  askText = '';
                }}>×</button
              >
            {/if}
          </p>
        {/if}
        <div class="ig-ask-box">
          <textarea
            bind:this={askEl}
            bind:value={askText}
            class="ig-ask-input"
            data-part="ignition-ask-input"
            rows="1"
            placeholder="Ask the agent…"
            aria-label="Ask the agent"
            disabled={asking}
            onkeydown={askKey}></textarea>
          <span class="ig-keys">Enter adds · {mac ? '⌘' : 'Ctrl'}+Enter sends</span>
        </div>
      </div>
    </div>
  {/if}
</div>
