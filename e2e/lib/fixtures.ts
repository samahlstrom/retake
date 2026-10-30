// Generates every test input with ffmpeg: a v5/v6/v7 version family (v7 a 10 s preview, plus a v5 draft decoy), an
// HLS (.m3u8, fMP4 segments) v8, and the fake-mic WAV. Nothing here is real footage or anyone's voice.
//
//   node lib/fixtures.ts <dir>      (writes <dir>/media/..., prints the layout as JSON)
import { execFileSync } from 'node:child_process';
import { linkSync, mkdirSync, readdirSync, readFileSync, utimesSync, statSync, copyFileSync } from 'node:fs';
import { join } from 'node:path';
import { pool, run } from './proc.ts';

export const FPS = 30;
export const SIZE = '1280x720';
export const FULL = 40; // seconds, v5 / v6 / v8
export const PREVIEW = 10; // seconds, the v7 preview
export const MIC_RATE = 48000;
export const MIC_LENGTH = 40;

/**
 * The fake mic: one tone burst per thing said, timed from the first R (the legacy selftest schedule). Each burst has
 * its own frequency so a segment's audio shows exactly what it captured. `seg` is the segment the scenario records it
 * in; null means it is said while NOT recording and must never be captured.
 */
export interface Burst { at: number; len: number; hz: number; seg: string | null }
export const MIC_BURSTS: Burst[] = [
  { at: 1.0, len: 1.4, hz: 420, seg: 'S1' },
  { at: 3.3, len: 4.2, hz: 530, seg: 'S1' },
  { at: 9.5, len: 1.5, hz: 1170, seg: null }, // during the unrecorded loop
  { at: 13.0, len: 1.8, hz: 640, seg: 'S2' },
  { at: 19.0, len: 1.5, hz: 750, seg: 'S3' },
  { at: 22.5, len: 1.9, hz: 860, seg: 'S4' },
];

export interface Media {
  dir: string;
  v5: string;
  v5draft: string;
  v6: string;
  v7preview: string;
  /** Folder holding clip_v8.m3u8, init.mp4 and the .m4s segments. */
  v8dir: string;
  mic: string;
  labels: boolean;
}

const ENC = ['-c:v', 'libx264', '-preset', 'veryfast', '-crf', '23', '-g', String(FPS), '-keyint_min', String(FPS),
  '-sc_threshold', '0', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-b:a', '96k', '-ar', '48000'];

/** A drawtext label maker if ffmpeg has drawtext and fontconfig finds a font; null otherwise (labels are cosmetic). */
async function labelMaker(): Promise<((text: string) => string) | null> {
  let font = '';
  try {
    font = execFileSync('fc-match', ['-f', '%{file}', 'sans:bold'], { stdio: ['ignore', 'pipe', 'ignore'] })
      .toString().trim();
  } catch { /* no fc-match: let drawtext ask fontconfig itself */ }
  const face = font && !/['\\:]/.test(font) ? `fontfile='${font}'` : `font='Sans'`;
  const make = (text: string): string => `drawtext=${face}:text='${text}':fontsize=56:fontcolor=white:box=1:`
    + `boxcolor=black@0.55:boxborderw=14:x=w-tw-40:y=40`;
  try {
    await run('ffmpeg', ['-v', 'error', '-nostdin', '-f', 'lavfi', '-i', 'color=s=160x90:d=0.1', '-vf', make('v1'),
      '-frames:v', '1', '-f', 'null', '-']);
    return make;
  } catch {
    return null;
  }
}

function lavfiInputs(seconds: number, toneHz: number, video = `testsrc2=s=${SIZE}:r=${FPS}`): string[] {
  return ['-f', 'lavfi', '-i', `${video}:d=${seconds}`,
    '-f', 'lavfi', '-i', `sine=f=${toneHz}:sample_rate=48000:d=${seconds}`];
}

function filters(label: ((t: string) => string) | null, text: string, extra: string[] = []): string[] {
  const chain = [...extra, ...(label ? [label(text)] : []), 'format=yuv420p'];
  return ['-vf', chain.join(','), '-af', 'volume=0.2'];
}

/** The fake mic as an aevalsrc expression: every burst is a tone with soft edges and a 4 Hz syllable-like wobble. */
function micExpression(): string {
  const terms = MIC_BURSTS.map(({ at, len, hz }) => {
    const end = at + len;
    const edge = `min(min((t-${at})/0.02,(${end}-t)/0.02),1)`;
    return `between(t,${at},${end})*${edge}*0.45*sin(2*PI*${hz}*t)*(0.7+0.3*sin(2*PI*4*(t-${at})))`;
  });
  return terms.join('+');
}

/** Where generateMedia(root) puts each file. */
export function mediaLayout(root: string): Media {
  const dir = join(root, 'media');
  return {
    dir,
    v5: join(dir, 'clip_v5.mp4'),
    v5draft: join(dir, 'clip_v5_draft.mp4'),
    v6: join(dir, 'clip_v6.mp4'),
    v7preview: join(dir, 'clip_v7_0-010_preview.mp4'),
    v8dir: join(dir, 'clip_v8'),
    mic: join(dir, 'mic.wav'),
    labels: false,
  };
}

export async function generateMedia(root: string): Promise<Media> {
  const label = await labelMaker();
  const m: Media = { ...mediaLayout(root), labels: !!label };
  const v8dir = m.v8dir;
  mkdirSync(v8dir, { recursive: true });
  const ff = (args: string[], cwd?: string) => () => run('ffmpeg', ['-v', 'error', '-nostdin', '-y', ...args],
    { nice: true, cwd });
  await pool(6, [
    ff([...lavfiInputs(FULL, 330), ...filters(label, 'v6'), ...ENC, '-movflags', '+faststart', '-t', String(FULL),
      m.v6]),
    ff([...lavfiInputs(FULL, 290), ...filters(label, 'v5', ['hue=h=40:s=0.6']), ...ENC, '-movflags', '+faststart',
      '-t', String(FULL), m.v5]),
    ff([...lavfiInputs(6, 250, `color=c=0x2a2a30:s=${SIZE}:r=${FPS}`), ...filters(label, 'v5 draft'), ...ENC,
      '-movflags', '+faststart', '-t', '6', m.v5draft]),
    ff([...lavfiInputs(PREVIEW, 370), ...filters(label, 'v7 preview', ['hue=h=200']), ...ENC, '-movflags',
      '+faststart', '-t', String(PREVIEW), m.v7preview]),
    ff([...lavfiInputs(FULL, 410), ...filters(label, 'v8 hls', ['hue=h=120']), ...ENC, '-t', String(FULL), '-f', 'hls',
      '-hls_time', '4', '-hls_playlist_type', 'vod', '-hls_segment_type', 'fmp4', '-hls_fmp4_init_filename',
      'init.mp4', '-hls_segment_filename', 'seg_%03d.m4s', 'clip_v8.m3u8'], v8dir),
    ff(['-f', 'lavfi', '-i', `aevalsrc=exprs='${micExpression()}':s=${MIC_RATE}:d=${MIC_LENGTH}`, '-ac', '1',
      '-c:a', 'pcm_s16le', m.mic]),
  ]);
  // The decoy is newer than the plain v5, so the "plain name wins over _draft" rule is what keeps it out.
  const plain = statSync(m.v5).mtime;
  const newer = new Date(plain.getTime() + 60_000);
  utimesSync(m.v5draft, newer, newer);
  const playlist = readFileSync(join(v8dir, 'clip_v8.m3u8'), 'utf8');
  if (!playlist.includes('#EXT-X-MAP:URI="init.mp4"') || !/^seg_000\.m4s$/m.test(playlist)) {
    throw new Error(`unexpected HLS playlist (segment URIs must be plain names beside it):\n${playlist}`);
  }
  return m;
}

/** Hardlinks (or copies) the media into a fresh version family under `famDir`, the way projects lay versions out. */
export function linkFamily(m: Media, famDir: string, versions: ('v5' | 'v6' | 'v7' | 'v8')[]): Record<string, string> {
  const put = (src: string, sub: string): string => {
    const d = join(famDir, sub);
    mkdirSync(d, { recursive: true });
    const dst = join(d, src.split('/').pop()!);
    try { linkSync(src, dst); } catch { copyFileSync(src, dst); }
    return dst;
  };
  const out: Record<string, string> = {};
  for (const v of versions) {
    if (v === 'v5') { out.v5 = put(m.v5, 'v5'); out.v5draft = put(m.v5draft, 'v5'); }
    if (v === 'v6') out.v6 = put(m.v6, 'v6');
    if (v === 'v7') out.v7 = put(m.v7preview, 'v7');
    if (v === 'v8') {
      for (const f of readdirSync(m.v8dir)) put(join(m.v8dir, f), 'clip_v8');
      out.v8 = join(famDir, 'clip_v8', 'clip_v8.m3u8');
    }
  }
  return out;
}

if (import.meta.main) {
  const root = process.argv[2];
  if (!root) {
    console.error('usage: node lib/fixtures.ts <dir>');
    process.exit(2);
  }
  console.log(JSON.stringify(await generateMedia(root), null, 1));
}
