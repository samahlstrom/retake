<script lang="ts">
  // The version menu's last entry (spec/api.md, "Download"): `Download v5 (MP4)` for the version on screen, a real
  // link to the file. Ready: the browser downloads it as an attachment. Not yet: the server gets it ready while the
  // entry shows how far (`Checking…`, `42%`), and the download starts by itself. A failure or the license note is one
  // quiet line under it.
  import { untrack } from 'svelte';
  import type { Review } from '../lib/review.svelte';
  import { downloadHref, noteText, progressText } from '../lib/download.svelte';
  import { draftOf } from '../lib/versions';

  let { review }: { review: Review } = $props();

  const label = $derived(review.shown ?? '');
  const dl = $derived(review.downloads.byLabel[label]);
  const shownName = $derived(draftOf(label) ? `${draftOf(label)} draft` : review.nameOf(label));
  const right = $derived(progressText(dl));
  const note = $derived(noteText(dl));

  // Opening the menu (or another version) looks where the file is, so a ready one downloads straight from the link.
  // Only the menu and the label count: the answer it gets doesn't ask again.
  $effect(() => {
    const open = review.verOpen;
    const l = label;
    if (open && l) untrack(() => void review.downloads.refresh(l));
  });

  function choose(e: MouseEvent) {
    if (dl?.state === 'ready') {
      // The link downloads it; the menu closes after the click is done (a link that's gone can't follow itself).
      setTimeout(() => (review.verOpen = false), 0);
      return;
    }
    e.preventDefault();
    void review.downloads.get(label);
  }
</script>

<a
  id="download"
  data-part="download"
  class="download"
  href={downloadHref(label)}
  download={dl?.name || undefined}
  onclick={choose}><span>Download {shownName} (MP4)</span><span class="d">{right}</span></a
>
{#if note}<p class="download-note" data-part="download-note">{note}</p>{/if}
