<script lang="ts">
  // The version slot (spec/scripts.md, "The editor"): the one document, v0/<video>_v0.md, and nothing else. Its
  // Markdown as a live preview, the full height of the area, in a readable column. One quiet ⋯ at the top right: Undo
  // the agent's edit, and on a phone "Type in the script" (there the agent is the main editor: reading or scrolling
  // never raises the keyboard until you choose to type). Core keeps Record, Send (which makes the video from the
  // script), the agent and the chat beside it (spec/chat.md). The agent's open questions about the document also show
  // in it, under their places, with their choices to tap.
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
  import { imageName, imageUrl, MAX_IMAGE } from '../lib/links';
  import { stamp } from '../lib/stamp';
  import type { Question } from './live';

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
  /** Hosted: the video's editing computer doesn't have this extension, so its files can't be read there. */
  const noMachine = c.ext.machine === false;
  const session = storageOf('session');
  const talkOnOpen = takeHandOff(session, TALK_KEY, video);
  const fresh = talkOnOpen || takeHandOff(session, NEW_KEY, video);
  const touch = typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches;
  /** The page's phone layout (web/src/lib/layout.ts): core leaves the slot's top right for its ⋯. */
  const phone =
    typeof matchMedia === 'function'
      ? matchMedia(
          '(max-width: 799.98px), (max-height: 419.98px), (pointer: coarse) and (max-width: 900px), ' +
            '(pointer: coarse) and (max-height: 600px)',
        )
      : null;

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
      void loadQuestions();
      if (canWrite && !d.loadError) void resume({ files, comments, doc: d, toast: (t) => c.toast(t) }).catch(() => {});
    } catch (e) {
      openError = sayError(e);
    }
  }

  // ---------------------------------------------------------------- a phone: reading, until you choose to type
  /** On a phone the document is read-only to the keyboard until ⋯ → Type in the script. */
  const phoneLayout = !!phone?.matches;
  let typing = $state(false);

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

  // ---------------------------------------------------------------- the agent's questions, in the document
  let questions = $state.raw<Question[]>([]);
  let asking = 0;
  async function loadQuestions(): Promise<void> {
    const p = doc?.path;
    if (!p) return;
    const n = ++asking;
    try {
      const items = await comments.list();
      if (n !== asking) return;
      const out: Question[] = [];
      for (const it of items) {
        const a = it.ask;
        if (!a?.anchor || a.anchor.file !== p) continue;
        const pics = (a.visuals?.options ?? []) as { text: string; image?: { url?: string } | null }[];
        const words = a.options?.length ? a.options : pics.map((o) => o.text);
        out.push({
          id: a.id,
          note: it.id,
          text: a.text,
          anchor: a.anchor,
          options: words.map((text) => {
            const url = pics.find((o) => o.text === text)?.image?.url;
            return url ? { text, image: url } : { text };
          }),
        });
      }
      questions = out;
    } catch {
      /* the comments column still has them */
    }
  }

  async function answerQuestion(q: Question, text: string): Promise<void> {
    try {
      await comments.answer(q.id, text);
      questions = questions.filter((x) => x.id !== q.id);
    } catch (e) {
      c.toast(sayError(e));
    }
    void loadQuestions();
  }

  /** An image's address for the preview: an owned file of v0/, or a https address. */
  const imageSrc = (src: string): string | null => imageUrl(src, (p) => files.url(p));

  /** Keeps a pasted or dropped image as v0/images/<name> (a new name, never over another file). */
  async function saveImage(f: File): Promise<string> {
    if (f.size > MAX_IMAGE) {
      c.toast(`${f.name || 'The image'} is over 25 MB`);
      throw new Error('too large');
    }
    const st = stamp(new Date());
    for (let n = 1; ; n++) {
      const name = imageName(f, st, n);
      try {
        await files.write(`v0/images/${name}`, f, { ifMatch: '*' });
        return name;
      } catch (e) {
        if (isChanged(e) && n < 9) continue;
        c.toast(`The image wasn’t saved: ${sayError(e)}`);
        throw e;
      }
    }
  }

  // ---------------------------------------------------------------- ⋯
  let menu = $state(false);
  let moreEl = $state<HTMLButtonElement>();
  let menuEl = $state<HTMLDivElement>();
  const canType = $derived(canWrite && phoneLayout && !!doc && !doc.loadError);
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
    if (phone?.matches) {
      right = 8; // the corner core leaves for it, right of the comments toggle
      return;
    }
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
    offs.push(
      c.on('job', () => {
        void loadHistory();
        void loadQuestions();
      }),
    );
    offs.push(
      c.on('automation', () => {
        void loadHistory();
        void loadQuestions();
      }),
    );
    offs.push(c.on('notes', () => void loadQuestions()));
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

<div class="ig-ed" data-part="scripts-editor" bind:this={root}>
  <div class="ig-scroll" bind:this={scroller}>
    <div class="ig-col" bind:this={col}>
      {#if noMachine}
        <p class="ig-note" data-part="scripts-no-machine">
          The editing computer doesn't have {c.ext.name}. Run <code>retake ext enable {c.ext.id}</code> there.
        </p>
      {:else if openError}
        <p class="ig-note" data-part="scripts-offline">
          {openError} · <button class="ig-link" onclick={() => void open()}>Try again</button>
        </p>
      {:else if doc?.loadError}
        <p class="ig-note" data-part="scripts-offline">
          {doc.loadError} · <button class="ig-link" onclick={() => void doc?.retry()}>Try again</button>
        </p>
      {:else if doc}
        {#key typing}<TextPane
            {doc}
            {scroller}
            readonly={!canWrite || (phoneLayout && !typing)}
            caret={fresh ? doc.text.length : null}
            focus={canWrite && !talkOnOpen && (!touch || typing)}
            {questions}
            image={imageSrc}
            preview={(url) => c.linkPreview(url)}
            answer={(q, t) => void answerQuestion(q, t)}
            openQuestion={(q) => c.selectComment(q.id)}
            {saveImage}
            bind:api={pane}
          />{/key}
      {/if}
    </div>
  </div>

  {#if doc?.conflict}
    <div class="ig-status" data-part="scripts-outside" role="status">
      Changed outside Retake · <button class="ig-link" onclick={() => void doc?.reload(true)}>Reload</button> ·
      <button class="ig-link" onclick={() => void doc?.keepMine()}>Keep mine</button>
    </div>
  {:else if doc?.error}
    <div class="ig-status" data-part="scripts-not-saved" role="status" title={doc.error}>
      Not saved · <button class="ig-link" onclick={() => void doc?.save()}>Retry</button>
    </div>
  {/if}

  {#if canType || canUndo}
    <button
      bind:this={moreEl}
      class={['ig-more', menu && 'ig-on']}
      data-part="more scripts-more"
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
  {#if menu && (canType || canUndo)}
    <div
      bind:this={menuEl}
      class="ig-menu"
      data-part="menu scripts-menu"
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
      {#if canType}
        <button
          role="menuitem"
          class="ig-item"
          data-part="scripts-type"
          onclick={() => {
            menu = false;
            typing = !typing;
          }}
        >
          <span>{typing ? 'Done typing' : 'Type in the script'}</span><span class="ig-item-sub"
            >{typing ? 'Back to reading' : 'Or ask the agent'}</span
          >
        </button>
      {/if}
      {#if canUndo && edit}
        <button role="menuitem" class="ig-item" data-part="scripts-undo" onclick={undo}>
          <span>Undo the agent’s edit</span><span class="ig-item-sub">{ago(ms(edit.at), now)}</span>
        </button>
      {/if}
    </div>
  {/if}
</div>
