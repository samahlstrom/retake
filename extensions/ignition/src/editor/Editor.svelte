<script lang="ts">
  // The version slot (spec/ignition.md, "The editor"): the one document, v0/<video>_v0.md, and nothing else. One plain
  // text, the full height of the area, in a readable column. One quiet ⋯ at the top right: Start v1 (while there's no
  // cut) and Undo the agent's edit. Core keeps Record, Send, the agent and the comments column (where comments are
  // typed: ctx.onAnchor says where they point; a comment's place comes back as ctx.on('reveal')).
  import { onMount, untrack } from 'svelte';
  import type { Ctx, Edit } from '../types';
  import { Doc } from './doc.svelte';
  import TextPane, { type PaneApi } from './TextPane.svelte';
  import { resume, talkOut } from './talkout';
  import { anchorAt, findAnchor } from '../lib/doc';
  import { docPathFor, folderOf, isDoc, migrate, OLD, pickDoc } from '../lib/v0';
  import { isChanged, sayError } from '../lib/errors';
  import { NEW_KEY, storageOf, takeHandOff, TALK_KEY } from '../lib/ideas';
  import { ago } from '../lib/stamp';
  import { hasCut as cutIn, START_V1 } from '../lib/versions';

  let { ctx }: { ctx: Ctx } = $props();

  const c = untrack(() => ctx);
  const files = c.files();
  const comments = c.comments();
  const video = c.video?.name ?? '';
  /** The video's folder name, for `<folder>_v0.md`: its path in the home (asked for when the page didn't know it yet). */
  let folder = folderOf(c.video);
  async function knowFolder(): Promise<void> {
    if (c.video?.path) return;
    try {
      const card = (await c.home()).projects.find((p) => p.name === video);
      if (card?.path) folder = folderOf(card);
    } catch {
      /* a single video's page: its name is its folder's */
    }
  }
  const canWrite = c.role !== 'viewer';
  /** Hosted: the video's editing computer doesn't have Ignition, so its files can't be read there. */
  const noMachine = c.ext.machine === false;
  const session = storageOf('session');
  const talkOnOpen = takeHandOff(session, TALK_KEY, video);
  const fresh = talkOnOpen || takeHandOff(session, NEW_KEY, video);
  const touch = typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches;

  let doc = $state<Doc | null>(null);
  let openError = $state('');
  let pane = $state<PaneApi>();
  let root = $state<HTMLDivElement>();
  let scroller = $state<HTMLDivElement>();
  let col = $state<HTMLDivElement>();

  // ---------------------------------------------------------------- the document: find it (or migrate an old v0)
  /** A v0 from before (idea.md, outline.md, script.md) becomes the one document; the old files stay. */
  async function migrateOld(list: string[]): Promise<string> {
    const read = async (p: string) => (list.includes(p) ? await (await files.read(p)).text() : null);
    const text = migrate({
      idea: await read(OLD.idea),
      outline: await read(OLD.outline),
      script: await read(OLD.script),
    });
    const path = docPathFor(folder);
    try {
      await files.write(path, text, { ifMatch: '*' });
    } catch (e) {
      if (!isChanged(e)) throw e; // another page made it first: it's there
    }
    return path;
  }

  async function open(): Promise<void> {
    openError = '';
    try {
      const list = (await files.list('v0/*')).filter((f) => !f.deleted).map((f) => f.path);
      if (list.filter(isDoc).length !== 1) await knowFolder(); // the name matters: making one, or choosing
      let path = pickDoc(list, folder);
      if (!path && list.includes(OLD.idea)) path = canWrite ? await migrateOld(list) : OLD.idea;
      const d = new Doc(files, path ?? docPathFor(folder));
      await d.ready;
      doc = d;
      void loadHistory();
      if (canWrite && !d.loadError) void resume({ files, comments, doc: d, toast: (t) => c.toast(t) }).catch(() => {});
    } catch (e) {
      openError = sayError(e);
    }
  }

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

  async function startV1(): Promise<void> {
    menu = false;
    try {
      await doc?.flush();
      const it = await comments.add({ text: START_V1, wants: 'version' });
      await comments.send([it.id]);
      c.toast('Sent. The agent builds v1 from the script.');
    } catch (e) {
      c.toast(sayError(e));
    }
  }

  // ---------------------------------------------------------------- the agent's edits: Undo
  let edit = $state<Edit | null>(null);
  let now = $state(Date.now());
  const ms = (at: number): number => (at < 1e12 ? at * 1000 : at);

  async function loadHistory(): Promise<void> {
    const p = doc?.path;
    if (!p || !canWrite) return;
    try {
      edit = (await files.history(p)).find((e) => e.path === p) ?? null;
    } catch {
      edit = null;
    }
  }

  async function undo(): Promise<void> {
    menu = false;
    const d = doc,
      e = edit;
    if (!d || !e) return;
    await d.flush();
    try {
      await files.undo(e.id, e.path);
      await d.reload(true);
    } catch (err) {
      c.toast(isChanged(err) ? 'Changed since the agent’s edit' : sayError(err));
    }
    void loadHistory();
  }

  // ---------------------------------------------------------------- ⋯
  let menu = $state(false);
  let moreEl = $state<HTMLButtonElement>();
  let menuEl = $state<HTMLDivElement>();
  const canStart = $derived(canWrite && hasCut === false && !!doc);
  const canUndo = $derived(canWrite && !!edit);
  /** How far the ⋯ sits from the slot's right edge: at the column's right edge, clear of core's comments pill. */
  let right = $state(16);
  /** The menu's: under the ⋯, moved left no further than the screen allows. */
  let menuRight = $state(16);
  $effect(() => {
    if (!menu || !menuEl || !root) return;
    const w = menuEl.offsetWidth,
      box = root.clientWidth;
    menuRight = Math.max(8, Math.min(right, box - w - 8));
  });

  function place(): void {
    if (!root || !col) return;
    const box = root.getBoundingClientRect(),
      c2 = col.getBoundingClientRect();
    let r = Math.max(8, box.right - c2.right);
    const pill = document.querySelector<HTMLElement>('[data-part~="sidebar-toggle"]');
    const p = pill?.getBoundingClientRect();
    if (p && p.width && p.top < box.top + 64) {
      const left = box.right - r - 44;
      if (box.right - r > p.left - 8 && left < p.right) r = box.right - p.left + 8;
    }
    right = Math.round(r);
  }

  function closeOutside(e: PointerEvent): void {
    const t = e.target as Node | null;
    if (menu && t && !menuEl?.contains(t) && !moreEl?.contains(t)) menu = false;
  }

  // ---------------------------------------------------------------- talking, typing, revealing
  function startTalk(): void {
    if (c.recording()) return;
    // Core's R: the page's key handler takes it when no text field has the focus.
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    document.body.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'r', code: 'KeyR', bubbles: true, cancelable: true }),
    );
  }

  onMount(() => {
    void open();
    void loadInfo();
    const offs: (() => void)[] = [];
    offs.push(
      files.watch((changed) => {
        for (const f of changed) {
          if (doc && f.path === doc.path) {
            doc.outside(f);
            void loadHistory();
          }
        }
      }),
    );
    offs.push(c.on('version', () => void loadInfo()));
    offs.push(c.on('job', () => void loadHistory()));
    offs.push(c.on('automation', () => void loadHistory()));
    offs.push(
      c.onAnchor(() => {
        const d = doc;
        if (!d?.loaded || d.loadError) return null;
        return anchorAt(d.path, d.text, pane?.caret() ?? d.text.length);
      }),
    );
    offs.push(
      c.on('reveal', (data) => {
        const a = (data as { anchor?: { line: number; heading?: string; quote?: string } } | null)?.anchor;
        const d = doc;
        if (!a || !d?.loaded) return;
        const line = findAnchor(d.text, a);
        if (line >= 0) pane?.reveal(line);
      }),
    );
    offs.push(
      c.onRecorded((a) => {
        const d = doc;
        if (!d || !canWrite || d.loadError) return;
        const p = pane?.caret() ?? null;
        void talkOut(
          a,
          {
            files,
            comments,
            transcribe: (path) => c.transcribe(path),
            toast: (t) => c.toast(t),
            doc: d,
            follow: (pos) => pane?.caret() === pos,
          },
          p === null ? null : { pos: p, was: d.text },
        );
      }),
    );
    const clock = setInterval(() => (now = Date.now()), 30_000);
    const unsaved = (e: BeforeUnloadEvent) => {
      if (doc && (doc.pending || (doc.dirty && !doc.loadError))) {
        void doc.flush();
        e.preventDefault();
      }
    };
    window.addEventListener('beforeunload', unsaved);
    const ro = new ResizeObserver(place);
    if (root) ro.observe(root);
    let watched: Element | null = null;
    const pillTimer = setInterval(() => {
      const pill = document.querySelector('[data-part~="sidebar-toggle"]');
      if (pill !== watched) {
        if (watched) ro.unobserve(watched);
        if (pill) ro.observe(pill);
        watched = pill;
        place();
      }
    }, 500);
    document.addEventListener('pointerdown', closeOutside, true);
    if (talkOnOpen) setTimeout(startTalk, 400);
    return () => {
      for (const off of offs) off();
      clearInterval(clock);
      clearInterval(pillTimer);
      ro.disconnect();
      document.removeEventListener('pointerdown', closeOutside, true);
      window.removeEventListener('beforeunload', unsaved);
      doc?.dispose();
    };
  });

  $effect(() => {
    void col;
    untrack(place);
  });
</script>

<div class="ig-ed" data-part="ignition-editor" bind:this={root}>
  <div class="ig-scroll" bind:this={scroller}>
    <div class="ig-col" bind:this={col}>
      {#if noMachine}
        <p class="ig-note" data-part="ignition-no-machine">
          The editing computer doesn't have Ignition. Run <code>retake ext enable ignition</code> there.
        </p>
      {:else if openError}
        <p class="ig-note" data-part="ignition-offline">
          {openError} · <button class="ig-link" onclick={() => void open()}>Try again</button>
        </p>
      {:else if doc?.loadError}
        <p class="ig-note" data-part="ignition-offline">
          {doc.loadError} · <button class="ig-link" onclick={() => void doc?.retry()}>Try again</button>
        </p>
      {:else if doc}
        <TextPane
          {doc}
          {scroller}
          readonly={!canWrite}
          caret={fresh ? doc.text.length : null}
          focus={canWrite && !talkOnOpen && !touch}
          bind:api={pane}
        />
      {/if}
    </div>
  </div>

  {#if doc?.conflict}
    <div class="ig-status" data-part="ignition-outside" role="status">
      Changed outside Retake · <button class="ig-link" onclick={() => void doc?.reload(true)}>Reload</button> ·
      <button class="ig-link" onclick={() => void doc?.keepMine()}>Keep mine</button>
    </div>
  {:else if doc?.error}
    <div class="ig-status" data-part="ignition-not-saved" role="status" title={doc.error}>
      Not saved · <button class="ig-link" onclick={() => void doc?.save()}>Retry</button>
    </div>
  {/if}

  {#if canStart || canUndo}
    <button
      bind:this={moreEl}
      class={['ig-more', menu && 'ig-on']}
      data-part="more ignition-more"
      style:right="{right}px"
      aria-label="More"
      aria-haspopup="menu"
      aria-expanded={menu}
      onclick={() => (menu = !menu)}
      ><svg viewBox="0 0 24 24" aria-hidden="true"
        ><circle cx="5.5" cy="12" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="18.5" cy="12" r="1.6" /></svg
      ></button
    >
  {/if}
  {#if menu && (canStart || canUndo)}
    <div
      bind:this={menuEl}
      class="ig-menu"
      data-part="menu ignition-menu"
      role="menu"
      style:right="{menuRight}px"
      tabindex="-1"
      onkeydown={(e) => {
        if (e.key === 'Escape') {
          e.stopPropagation();
          menu = false;
        }
      }}
    >
      {#if canStart}
        <button role="menuitem" class="ig-item" data-part="ignition-start-v1" onclick={startV1}>
          <span>Start v1</span><span class="ig-item-sub">The agent builds the first cut from the script</span>
        </button>
      {/if}
      {#if canUndo && edit}
        <button role="menuitem" class="ig-item" data-part="ignition-undo" onclick={undo}>
          <span>Undo the agent’s edit</span><span class="ig-item-sub">{ago(ms(edit.at), now)}</span>
        </button>
      {/if}
    </div>
  {/if}
</div>
