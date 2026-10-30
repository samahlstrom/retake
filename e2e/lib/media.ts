// Media inspection with ffprobe/ffmpeg: durations, decoded audio, and where each fake-mic tone shows up.
import { run } from './proc.ts';

export const ANALYSIS_RATE = 16000;

/** Container duration from ffprobe, or 0 when there is none (MediaRecorder webm has no duration header). */
export async function probeDuration(file: string): Promise<number> {
  const out = await run('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', file]);
  const d = Number.parseFloat(out.toString().trim());
  return Number.isFinite(d) ? d : 0;
}

/** Decodes the first audio stream to mono float samples at ANALYSIS_RATE. */
export async function decodeAudio(file: string): Promise<Float32Array> {
  const raw = await run('ffmpeg', ['-v', 'error', '-nostdin', '-i', file, '-map', '0:a:0', '-ac', '1', '-ar',
    String(ANALYSIS_RATE), '-f', 'f32le', '-']);
  // Copy into an aligned buffer: Buffer slices may start at any byte offset.
  const aligned = new Uint8Array(raw.byteLength - (raw.byteLength % 4));
  aligned.set(raw.subarray(0, aligned.byteLength));
  return new Float32Array(aligned.buffer);
}

/** Decoded audio duration in seconds: the real length, whatever the container header says. */
export async function audioDuration(file: string): Promise<number> {
  return (await decodeAudio(file)).length / ANALYSIS_RATE;
}

/** Power of one frequency in a window (Goertzel), normalized so a full-scale sine gives about 0.25 * n^2. */
function goertzel(x: Float32Array, start: number, n: number, hz: number): number {
  const w = (2 * Math.PI * hz) / ANALYSIS_RATE;
  const c = 2 * Math.cos(w);
  let s1 = 0;
  let s2 = 0;
  for (let i = 0; i < n; i++) {
    const s0 = x[start + i]! + c * s1 - s2;
    s2 = s1;
    s1 = s0;
  }
  return s1 * s1 + s2 * s2 - c * s1 * s2;
}

export interface ToneWindows {
  /** Window length in seconds. */
  win: number;
  /** For each tone frequency: start times (s, from the start of the audio) of the windows where it dominates. */
  hits: Map<number, number[]>;
}

/**
 * Finds where each tone is present: a window counts for a frequency when the window is louder than about -45 dBFS and
 * that frequency carries a clear share of its energy. Windows are 100 ms with a Hann taper.
 */
export function findTones(x: Float32Array, freqs: number[], win = 0.1): ToneWindows {
  const n = Math.round(win * ANALYSIS_RATE);
  const hann = Float32Array.from({ length: n }, (_, i) => 0.5 - 0.5 * Math.cos((2 * Math.PI * i) / (n - 1)));
  const buf = new Float32Array(n);
  const hits = new Map<number, number[]>(freqs.map((f) => [f, []]));
  for (let start = 0; start + n <= x.length; start += n) {
    let energy = 0;
    for (let i = 0; i < n; i++) {
      const v = x[start + i]! * hann[i]!;
      buf[i] = v;
      energy += v * v;
    }
    const rms = Math.sqrt(energy / n);
    if (rms < 0.005) continue; // about -46 dBFS: silence
    for (const f of freqs) {
      // Parseval: a pure tone in the window puts about n/2 * energy into its Goertzel bin.
      const share = goertzel(buf, 0, n, f) / ((n / 2) * energy);
      if (share > 0.25) hits.get(f)!.push(start / ANALYSIS_RATE);
    }
  }
  return { win, hits };
}
