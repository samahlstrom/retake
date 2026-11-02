// v0's folders as lists: the talk-outs in voice/ and the files in refs/.
import type { FileInfo, Word } from '../types';
import { parseStamp } from './stamp';

export interface TalkOut {
  /** Its stamp: "20261101-190211". */
  st: string;
  at: Date | null;
  /** Its audio, as the talk-out block names it: "voice/20261101-190211.webm". */
  rel: string;
  audio: FileInfo;
  /** Its transcript (voice/<stamp>.md) and words (voice/<stamp>.words.json), when there. */
  md: FileInfo | null;
  words: FileInfo | null;
}

const AUDIO = /^(webm|ogg|opus|m4a|mp3|wav)$/i;

/** voice/'s files grouped by stamp, the ones with audio, newest first. */
export function talkOuts(list: FileInfo[]): TalkOut[] {
  const by = new Map<string, { audio?: FileInfo; md?: FileInfo; words?: FileInfo }>();
  for (const f of list) {
    if (f.deleted) continue;
    const m = /^v0\/voice\/([^/.]+)\.(.+)$/.exec(f.path);
    if (!m) continue;
    const g = by.get(m[1]!) ?? {};
    if (AUDIO.test(m[2]!)) g.audio = f;
    else if (m[2] === 'md') g.md = f;
    else if (m[2] === 'words.json') g.words = f;
    by.set(m[1]!, g);
  }
  const out: TalkOut[] = [];
  for (const [st, g] of by) {
    if (!g.audio) continue;
    out.push({
      st,
      at: parseStamp(st),
      rel: g.audio.path.slice('v0/'.length),
      audio: g.audio,
      md: g.md ?? null,
      words: g.words ?? null,
    });
  }
  return out.sort((a, b) => (a.st < b.st ? 1 : a.st > b.st ? -1 : 0));
}

/** How long a talk-out is, from its words (the end of the last one), or null. */
export function wordsLength(body: unknown): number | null {
  const words = (body as { words?: unknown } | null)?.words;
  if (!Array.isArray(words) || !words.length) return null;
  let end = 0;
  for (const w of words as Partial<Word>[]) if (typeof w?.e === 'number' && w.e > end) end = w.e;
  return end > 0 ? end : null;
}

/** 25 MiB: the most one write takes. */
export const MAX_REF = 25 * 1024 * 1024;

/** A dropped file's name as a ref: no folders, no hidden name, nothing a path can't hold. "" when nothing is left. */
export function refName(name: string): string {
  return name
    .split(/[/\\]/)
    .pop()!
    .replace(/[\u0000-\u001f\u007f]+/g, '')
    .replace(/^[\s.]+/, '')
    .trim()
    .slice(0, 200);
}

/** The nth try's name for a ref that exists: "shot.png", "shot-2.png", … */
export function nthName(name: string, n: number): string {
  if (n <= 1) return name;
  const dot = name.lastIndexOf('.');
  return dot > 0 ? `${name.slice(0, dot)}-${n}${name.slice(dot)}` : `${name}-${n}`;
}

export const isImage = (path: string): boolean => /\.(png|jpe?g|gif|webp|avif|svg)$/i.test(path);
export const isDoc = (path: string): boolean => /^v0\/refs\/[^/]+\.md$/.test(path);
