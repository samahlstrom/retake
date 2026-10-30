// Reads a take folder from disk and checks its audio against the fake mic's schedule.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { MIC_BURSTS } from './fixtures.ts';
import { ANALYSIS_RATE, decodeAudio, findTones } from './media.ts';

export interface Segment {
  id: string; file: string; mime: string; version: string; flip: boolean; t_start: number; t_end: number;
  recording: boolean; video_start: number; video_end: number; video_min: number; video_max: number;
}
export interface Stroke {
  id: number; seg: string; version: string; flip: boolean; color: string; session_t: number; session_t_end: number;
  video_t: number; video_t_end: number; paused: boolean; points: [number, number][];
}
export interface TakeEvent { session_t: number; video_t: number; event: string; [k: string]: unknown }
export interface Session {
  format: number; version: string; session: string; recorded: number;
  versions: { label: string; path: string; duration: number | null }[];
  video: { path: string; name: string; duration: number | null; fps: number; width: number | null; height: number | null };
  author?: { name: string };
  [k: string]: unknown;
}
export interface Voice { bin: number; bins: [number, number][] }
export interface Take {
  dir: string; session: Session; segments: Segment[]; events: TakeEvent[]; strokes: Stroke[]; voice: Voice;
  /** Audio files on disk (seg_NN.*). */
  audio: string[];
}

const readJson = <T>(p: string): T => JSON.parse(readFileSync(p, 'utf8')) as T;

export function readTake(dir: string): Take {
  return {
    dir,
    session: readJson(join(dir, 'session.json')),
    segments: readJson(join(dir, 'segments.json')),
    events: readJson(join(dir, 'events.json')),
    strokes: readJson(join(dir, 'strokes.json')),
    voice: readJson(join(dir, 'voice.json')),
    audio: existsSync(dir) ? readdirSync(dir).filter((f) => /^seg_\d{2,3}\.(webm|ogg|m4a)$/.test(f)).sort() : [],
  };
}

export interface SegmentAudio {
  id: string;
  /** Decoded length in seconds. */
  duration: number;
  /** Page clock length: t_end - t_start. */
  expected: number;
  /** For each fake-mic burst (by frequency): seconds of audio where its tone was found, and where it first shows. */
  tones: { hz: number; seg: string | null; at: number; seconds: number; first: number | null }[];
}

/** Decodes one segment's audio and finds every fake-mic tone in it. */
export async function analyzeSegment(dir: string, s: Segment): Promise<SegmentAudio> {
  const x = await decodeAudio(join(dir, s.file));
  const found = findTones(x, MIC_BURSTS.map((b) => b.hz));
  return {
    id: s.id,
    duration: x.length / ANALYSIS_RATE,
    expected: s.t_end - s.t_start,
    tones: MIC_BURSTS.map((b) => {
      const hits = found.hits.get(b.hz)!;
      return { hz: b.hz, seg: b.seg, at: b.at, seconds: hits.length * found.win, first: hits[0] ?? null };
    }),
  };
}
