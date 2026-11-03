// Downloading the version on screen as one MP4 that YouTube takes as it is (spec/api.md, "Download"). The version
// menu's last entry is a real link to `video/<label>/download` (an attachment, so iOS Safari offers its own download
// sheet); when the file isn't ready yet, the page asks the server to get it ready, shows how far it is, and clicks the
// same link once it is. Closing the menu doesn't stop it.

/** GET/POST /api/download's answer. */
export interface DownloadState {
  label: string;
  name: string;
  state: 'none' | 'checking' | 'making' | 'ready' | 'failed' | 'unavailable';
  progress: number | null;
  size: number;
  as_is: boolean;
  error: string;
  license: string;
}

/** The download's URL, relative like every page request (it works under /p/<name>/ too). */
export function downloadHref(label: string): string {
  return `video/${encodeURIComponent(label)}/download`;
}

/** What the entry says at its right while the file gets ready, or its size once it is. */
export function progressText(s: DownloadState | undefined): string {
  switch (s?.state) {
    case 'checking':
      return 'Checking…';
    case 'making':
      return s.progress == null ? 'Preparing…' : `${Math.floor(s.progress * 100)}%`;
    case 'ready':
      return sizeText(s.size);
  }
  return '';
}

/** A file size in a few characters: 820 KB, 41 MB, 1.2 GB. */
export function sizeText(bytes: number): string {
  if (!bytes) return '';
  if (bytes < 1e6) return `${Math.max(1, Math.round(bytes / 1e3))} KB`;
  if (bytes < 1e9) return `${Math.round(bytes / 1e6)} MB`;
  return `${(bytes / 1e9).toFixed(1)} GB`;
}

/** The quiet line under the entry: why it failed, else the license note, else nothing. */
export function noteText(s: DownloadState | undefined): string {
  if (!s) return '';
  if (s.state === 'failed' || s.state === 'unavailable') return s.error;
  return s.license;
}

/** Clicks a real link to the file (never a blob from fetch: Safari on a phone hands an attachment to its own sheet). */
export function clickLink(href: string, name: string): void {
  const a = document.createElement('a');
  a.href = href;
  a.download = name;
  a.hidden = true;
  document.body.append(a);
  a.click();
  a.remove();
}

const busy = (s: DownloadState | undefined) => s?.state === 'checking' || s?.state === 'making';

/** The downloads the page knows about, by label. */
export class Downloads {
  byLabel = $state<Record<string, DownloadState>>({});
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  #want = new Set<string>(); // asked for: downloads by itself once ready
  // eslint-disable-next-line svelte/prefer-svelte-reactivity -- bookkeeping, nothing renders it
  #timers = new Map<string, ReturnType<typeof setTimeout>>();
  #fetch: typeof fetch;
  #click: (href: string, name: string) => void;
  #every: number;

  constructor(opts: { fetch?: typeof fetch; click?: (href: string, name: string) => void; every?: number } = {}) {
    this.#fetch = opts.fetch ?? ((...a) => fetch(...a));
    this.#click = opts.click ?? clickLink;
    this.#every = opts.every ?? 1000;
  }

  /** Where a version's download is (the menu opened): starts nothing. */
  async refresh(label: string): Promise<void> {
    if (busy(this.byLabel[label])) return; // already being followed
    await this.#ask(label, 'GET');
  }

  /** The entry was chosen while the file isn't ready: get it ready, follow it, and download it once it is. */
  async get(label: string): Promise<void> {
    this.#want.add(label);
    await this.#ask(label, 'POST');
  }

  /** Stops following every download (the page is going away). */
  stop(): void {
    for (const t of this.#timers.values()) clearTimeout(t);
    this.#timers.clear();
    this.#want.clear();
  }

  async #ask(label: string, method: 'GET' | 'POST'): Promise<void> {
    let s: DownloadState;
    try {
      const r = await this.#fetch(`api/download?v=${encodeURIComponent(label)}`, { method });
      const body = (await r.json()) as Partial<DownloadState> & { error?: string };
      if (!r.ok) {
        s = { ...this.#placeholder(label), state: 'failed', error: body.error ?? `Couldn't get the MP4 (${r.status})` };
      } else {
        s = { ...this.#placeholder(label), ...body } as DownloadState;
      }
    } catch {
      s = { ...this.#placeholder(label), state: 'failed', error: "Couldn't reach the review server" };
    }
    this.byLabel = { ...this.byLabel, [label]: s };
    this.#next(label, s);
  }

  #placeholder(label: string): DownloadState {
    return { label, name: '', state: 'none', progress: null, size: 0, as_is: false, error: '', license: '' };
  }

  #next(label: string, s: DownloadState): void {
    const t = this.#timers.get(label);
    if (t) clearTimeout(t);
    this.#timers.delete(label);
    if (busy(s)) {
      this.#timers.set(
        label,
        setTimeout(() => void this.#ask(label, 'GET'), this.#every),
      );
      return;
    }
    if (!this.#want.has(label)) return;
    this.#want.delete(label);
    if (s.state === 'ready') this.#click(downloadHref(label), s.name);
  }
}
