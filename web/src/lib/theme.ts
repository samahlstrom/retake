// The look's colours for what is drawn on a canvas (the timeline and the live waveform), where CSS can't reach. They
// come from the theme's tokens (theme/tokens.css, spec/theme.md "Tokens"): each is resolved through a hidden element's
// computed colour, once, and again when the page has loaded (a theme's stylesheet may arrive late) or the computer
// switches between light and dark. Without a document (unit tests) or for a value a canvas can't take, the default is
// used. DEFAULT_PALETTE is the built-in look; theme.test.ts keeps it equal to tokens.css.

/** One colour per thing drawn. */
export interface Palette {
  /** Your voice above the track, and the waveform's voiced bars. */
  voice: string;
  /** The track, the part of a streamed version not rendered yet, and the played part. */
  track: string;
  trackPending: string;
  trackPlayed: string;
  /** The playhead, and the playhead while recording. */
  playhead: string;
  playheadRecording: string;
  /** The waveform's quiet bars, and its flat line before you record. */
  wave: string;
  waveIdle: string;
}

export const DEFAULT_PALETTE: Readonly<Palette> = Object.freeze({
  voice: '#5ac8fa',
  track: 'rgba(255,255,255,.14)',
  trackPending: 'rgba(255,255,255,.05)',
  trackPlayed: 'rgba(255,255,255,.82)',
  playhead: '#fff',
  playheadRecording: '#ff6a60',
  wave: 'rgba(255,255,255,.35)',
  waveIdle: 'rgba(255,255,255,.12)',
});

/** The token behind each colour. */
export const PALETTE_TOKENS: Readonly<Record<keyof Palette, string>> = Object.freeze({
  voice: '--voice',
  track: '--track',
  trackPending: '--track-pending',
  trackPlayed: '--track-played',
  playhead: '--playhead',
  playheadRecording: '--playhead-recording',
  wave: '--wave',
  waveIdle: '--wave-idle',
});

let cached: Palette | null = null;
let watching = false;

/** The canvas colours of the current theme. */
export function palette(): Readonly<Palette> {
  if (cached) return cached;
  if (typeof document === 'undefined' || !document.documentElement) return DEFAULT_PALETTE;
  cached = resolve(document);
  if (!watching) {
    watching = true;
    const again = () => (cached = null);
    globalThis.addEventListener?.('load', again);
    globalThis.matchMedia?.('(prefers-color-scheme: dark)').addEventListener?.('change', again);
  }
  return cached;
}

/** Each token's computed colour, checked by a 2D context: a value the canvas refuses falls back to the default. */
export function resolve(doc: Document): Palette {
  const probe = doc.createElement('span');
  probe.style.display = 'none';
  doc.documentElement.append(probe);
  const g = doc.createElement('canvas').getContext('2d');
  const out = { ...DEFAULT_PALETTE };
  try {
    for (const key of Object.keys(PALETTE_TOKENS) as (keyof Palette)[]) {
      probe.style.color = '';
      probe.style.color = `var(${PALETTE_TOKENS[key]})`;
      const value = getComputedStyle(probe).color;
      if (value && canvasTakes(g, value)) out[key] = value;
    }
  } finally {
    probe.remove();
  }
  return out;
}

/** Whether a 2D context accepts the colour (an assignment it can't parse leaves fillStyle as it was). */
function canvasTakes(g: CanvasRenderingContext2D | null, value: string): boolean {
  if (!g) return true;
  g.fillStyle = '#010203';
  g.fillStyle = value;
  return g.fillStyle !== '#010203' || /^#010203$|^rgba?\(1, 2, 3(, 1)?\)$/i.test(value);
}
