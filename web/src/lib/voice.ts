// Voice activity (only while recording): level above an adaptive noise floor, with a short hangover. It feeds the live
// waveform next to REC and the voice bins above the timeline.
import { BIN, WAVE_STEP } from './constants';
import { DEFAULT_PALETTE, type Palette } from './theme';
import { clamp } from './util';

export interface Vad {
  floor: number;
  hang: number;
  active: boolean;
}
/** One waveform bar: [peak level 0..1, voice active]. */
export type WaveBar = [number, boolean];
export interface WaveAcc {
  t: number;
  peak: number;
  voice: boolean;
}

export const newVad = (): Vad => ({ floor: -60, hang: 0, active: false });
export const newAcc = (): WaveAcc => ({ t: 0, peak: 0, voice: false });

/** Mean power of a block of samples, in dB. */
export function levelDb(buf: Float32Array): number {
  let sum = 0;
  for (const v of buf) sum += v * v;
  return 10 * Math.log10(sum / buf.length + 1e-12);
}

/** Advance the VAD by `dt` seconds with a block at `db`. Returns the display level (0..1). */
export function vadStep(vad: Vad, db: number, dt: number): number {
  vad.floor = db < vad.floor ? db : Math.min(vad.floor + 1.5 * dt, db);
  vad.floor = Math.max(vad.floor, -90);
  const speaking = db > Math.max(vad.floor + 12, -52);
  vad.hang = speaking ? 0.3 : Math.max(0, vad.hang - dt);
  vad.active = speaking || vad.hang > 0;
  return clamp((db + 62) / 50, 0, 1);
}

/** Accumulate `dt` seconds at `level` into waveform bars (one per WAVE_STEP). Returns the (possibly trimmed) bars. */
export function pushBars(bars: WaveBar[], acc: WaveAcc, dt: number, level: number, active: boolean): WaveBar[] {
  acc.t += dt;
  acc.peak = Math.max(acc.peak, level);
  acc.voice = acc.voice || active;
  while (acc.t >= WAVE_STEP) {
    bars.push([acc.peak, acc.voice]);
    acc.t -= WAVE_STEP;
    acc.peak = level;
    acc.voice = active;
  }
  return bars.length > 400 ? bars.slice(-200) : bars;
}

/**
 * Credit `dt` seconds of voice to the bins under the playhead. While playing smoothly from `prevVT` to `vt` the time
 * is spread over every bin passed; otherwise (paused, seeking, a jump) it all goes to the bin at `vt`.
 */
export function addVoice(
  bins: Float32Array,
  dt: number,
  vt: number,
  prevVT: number | null,
  playing: boolean,
  seeking: boolean,
): void {
  if (playing && !seeking && prevVT != null && vt >= prevVT && vt - prevVT < 1) {
    const a = Math.floor(prevVT / BIN),
      b = Math.floor(vt / BIN),
      n = b - a + 1;
    for (let i = a; i <= b; i++) bins[i] += dt / n;
  } else bins[Math.floor(vt / BIN)] += dt;
}

/** The live waveform: newest bar at the right edge, scrolling left while recording; a flat grey line when idle. In the
 * theme's canvas colours (lib/theme.ts). */
export function drawWave(
  g: CanvasRenderingContext2D,
  w: number,
  h: number,
  bars: WaveBar[] | null,
  colors: Readonly<Palette> = DEFAULT_PALETTE,
): void {
  const pitch = 3,
    n = Math.floor(w / pitch),
    mid = h / 2;
  if (!bars) {
    g.fillStyle = colors.waveIdle;
    for (let i = 0; i < n; i++) g.fillRect(w - (n - i) * pitch, mid - 0.75, 2, 1.5);
    return;
  }
  const recent = bars.slice(-n);
  for (let i = 0; i < n; i++) {
    const b = recent[recent.length - n + i],
      x = w - (n - i) * pitch;
    const bh = b ? Math.max(1.5, b[0] * (h - 2)) : 1.5;
    g.fillStyle = b && b[1] ? colors.voice : colors.wave;
    g.fillRect(x, mid - bh / 2, 2, bh);
  }
}
