<script module lang="ts">
  /** A question anchored in this document: a mark in the left margin. */
  export interface Mark {
    /** The comment it's under (what the column selects). */
    item: number;
    /** The thread an answer goes to. */
    thread: number;
    text: string;
  }
</script>

<script lang="ts">
  // The text: a plain textarea, with a mirror of it on top that draws the same lines (headings stronger), the
  // questions' marks in the margin and the cuts' chips after the script's headings. The mirror lets every click through
  // but the marks' and chips'; the textarea's text is transparent under it, so the caret, the selection and the
  // spell-check underlines are the textarea's own, and wrapping is the same in both.
  import { onMount, untrack } from 'svelte';
  import type { Chip } from '../lib/cuts';
  import { headingOf, keepLine, lineOf, mapCaret } from '../lib/doc';
  import type { Doc } from './doc.svelte';

  let {
    doc,
    readonly,
    caret,
    focus,
    label,
    marks,
    chips,
    selected,
    oncaret,
    onmark,
    onchip,
  }: {
    doc: Doc;
    readonly: boolean;
    /** Where the caret starts. */
    caret: number;
    /** Put the focus in the text when it shows. */
    focus: boolean;
    label: string;
    marks: Map<number, Mark[]>;
    chips: Map<number, Chip>;
    /** The selected question's comment, or null. */
    selected: number | null;
    oncaret: (pos: number) => void;
    onmark: (m: Mark) => void;
    onchip: (c: Chip) => void;
  } = $props();

  /** An empty line's content in the mirror: it gives the line its height and no width. */
  const ZWSP = '\u200b';

  let ta: HTMLTextAreaElement;
  let mirror: HTMLDivElement;
  let ready = false;

  interface Line {
    text: string;
    level: number;
    hashes: string;
    rest: string;
  }

  const lines = $derived(
    doc.text.split('\n').map((text): Line => {
      const h = headingOf(text);
      if (!h) return { text, level: 0, hashes: '', rest: '' };
      const m = /^(#{1,6})(.*)$/.exec(text)!;
      return { text, level: h.level, hashes: m[1]!, rest: m[2]! };
    }),
  );

  function report(): void {
    if (ta) oncaret(ta.selectionStart);
  }

  /** Scrolls the caret's line into view in the pane. */
  function reveal(pos: number): void {
    const el = mirror?.children[lineOf(doc.text, pos)] as HTMLElement | undefined;
    el?.scrollIntoView?.({ block: 'nearest' });
  }

  onMount(() => {
    ta.value = doc.text;
    const c = Math.min(Math.max(0, caret), doc.text.length);
    ta.setSelectionRange(c, c);
    if (focus && !readonly) ta.focus({ preventScroll: true });
    requestAnimationFrame(() => reveal(c));
    ready = true;
    const sel = () => {
      if (document.activeElement === ta) report();
    };
    document.addEventListener('selectionchange', sel);
    return () => document.removeEventListener('selectionchange', sel);
  });

  // A change from outside the textarea (a reload, a talk-out): show it and keep the caret in its place.
  $effect(() => {
    const x = doc.external;
    untrack(() => {
      if (!ready || !x || ta.value === doc.text) return;
      const s = ta.selectionStart,
        e = ta.selectionEnd;
      let ns: number, ne: number;
      if (x.kind === 'splice') {
        ns = x.caret ?? mapCaret(s, x.splice);
        ne = x.caret ?? mapCaret(e, x.splice);
      } else {
        ns = ne = keepLine(ta.value, s, doc.text);
      }
      ta.value = doc.text;
      ta.setSelectionRange(ns, ne);
      oncaret(ns);
      if (x.kind === 'splice' && x.caret !== undefined) requestAnimationFrame(() => reveal(ns));
    });
  });

  function keydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      // Esc leaves the text, so R records again.
      e.preventDefault();
      ta.blur();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      void doc.flush();
    }
  }

  function markClick(e: MouseEvent, list: Mark[]): void {
    e.preventDefault();
    const i = list.findIndex((m) => m.item === selected);
    onmark(list[(i + 1) % list.length]!);
  }
</script>

<div class="ig-paper">
  <textarea
    bind:this={ta}
    class="ig-text"
    data-part="ignition-text"
    aria-label={label}
    rows="1"
    spellcheck="true"
    {readonly}
    oninput={() => {
      doc.input(ta.value);
      report();
    }}
    onkeydown={keydown}
    onkeyup={report}
    onclick={report}
    onfocus={report}
    onblur={() => void doc.flush()}></textarea>
  <div class="ig-mirror" bind:this={mirror} aria-hidden="true">
    {#each lines as l, i}
      {@const ms = marks.get(i)}
      {@const chip = chips.get(i)}
      <div class={['ig-ln', l.level && `ig-h ig-lv${l.level}`, ms?.some((m) => m.item === selected) && 'ig-ln-on']}>
        {#if ms}<button
            class={['ig-mark', ms.some((m) => m.item === selected) && 'ig-on']}
            data-part="ignition-question-mark"
            tabindex="-1"
            title={ms.map((m) => m.text).join('\n')}
            onmousedown={(e) => e.preventDefault()}
            onclick={(e) => markClick(e, ms)}>?</button
          >{/if}{#if l.level}<span class="ig-hm">{l.hashes}</span><span class="ig-ht">{l.rest}</span>{:else}{l.text ||
            ZWSP}{/if}{#if chip}<button
            class="ig-chip"
            data-part="ignition-chip"
            tabindex="-1"
            title="Open {chip.version} here"
            onmousedown={(e) => e.preventDefault()}
            onclick={() => onchip(chip)}>{chip.text}</button
          >{/if}
      </div>
    {/each}
  </div>
</div>
