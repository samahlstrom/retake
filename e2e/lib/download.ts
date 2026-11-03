// Fixtures for the download specs (spec/api.md, "Download"): a family of two small versions, made with ffmpeg.
// - v1/talk_v1.mp4: what YouTube takes as it is (H.264 High, BT.709, AAC 48 kHz stereo at -14 LUFS, faststart);
// - v2/talk_v2.mkv: the same picture in an MKV with a loud tone, which a download makes into an MP4 once.
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, rmSync } from 'node:fs';
import { join } from 'node:path';

/** A 4 s clip at 320x180, 25 fps, with a 440 Hz tone at `gain` dBFS in both channels; `args` add tags and options. */
function clip(path: string, gain: number, args: string[]): void {
  execFileSync('nice', ['-n', '19', 'ffmpeg', '-nostdin', '-v', 'error', '-y', '-threads', '2',
    '-f', 'lavfi', '-i', 'testsrc2=s=320x180:r=25:d=4', '-f', 'lavfi', '-i', 'sine=f=440:sample_rate=48000:d=4',
    '-filter_complex', `[1:a]volume=${gain.toFixed(2)}dB,pan=stereo|c0=c0|c1=c0[a]`, '-map', '0:v', '-map', '[a]',
    '-c:v', 'libx264', '-preset', 'veryfast', '-profile:v', 'high', '-pix_fmt', 'yuv420p',
    '-c:a', 'aac', '-ar', '48000', '-ac', '2', ...args, '-shortest', path]);
}

/** ffmpeg's EBU R128 reading of a file: its integrated loudness in LUFS. */
export function lufs(path: string): number {
  const r = spawnSync('ffmpeg', ['-nostdin', '-nostats', '-i', path, '-af', 'ebur128', '-f', 'null', '-'],
    { encoding: 'utf8' });
  const sum = r.stderr.slice(r.stderr.lastIndexOf('Summary:'));
  return Number.parseFloat(sum.split('I:')[1]!.trim().split(/\s+/)[0]!);
}

/** ffprobe's streams of a file. */
export function streams(path: string): Record<string, string>[] {
  const out = execFileSync('ffprobe', ['-v', 'error', '-print_format', 'json', '-show_streams', path]).toString();
  return (JSON.parse(out) as { streams: Record<string, string>[] }).streams;
}

/** Makes the family in dir (wiped first) and returns the file to open: v2. */
export function downloadFamily(dir: string): string {
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(join(dir, 'v1'), { recursive: true });
  mkdirSync(join(dir, 'v2'), { recursive: true });
  const level = join(dir, 'level.mp4');
  clip(level, -20, []);
  const gain = -20 + (-14 - lufs(level));
  rmSync(level);
  clip(join(dir, 'v1', 'talk_v1.mp4'), gain, [
    '-vf', 'setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709:range=tv', '-movflags', '+faststart',
  ]);
  clip(join(dir, 'v2', 'talk_v2.mkv'), -3, []);
  return join(dir, 'v2', 'talk_v2.mkv');
}
