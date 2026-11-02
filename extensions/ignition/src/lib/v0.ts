// v0 is one document, v0/<video>_v0.md (spec/ignition.md, "v0: one document"): which file it is, the one a v0 from
// before (idea.md, outline.md, script.md) becomes, and the talk-outs' sidecars in v0/voice/.
import { wordsText } from './doc';

/** A v0 document's path: `v0/<name>_v0.md`, nothing deeper. */
const DOC = /^v0\/([^/]+)_v0\.md$/;

export const isDoc = (path: string): boolean => DOC.test(path);

/** Where the document of a video folder goes: `v0/<folder>_v0.md`. */
export const docPathFor = (folder: string): string => `v0/${folder}_v0.md`;

/** A video's folder name from its path in the home ("deadlock/jump-souls" is "jump-souls"), else its name. */
export const folderOf = (video: { name: string; path?: string } | undefined): string =>
  video?.path?.split('/').filter(Boolean).pop() || video?.name || 'video';

/** The document among v0's files: its one `*_v0.md`; with more than one, `<folder>_v0.md`, else the first by name.
 * null when there is none. */
export function pickDoc(paths: readonly string[], folder: string): string | null {
  const docs = paths.filter(isDoc).sort();
  if (!docs.length) return null;
  return docs.find((p) => p === docPathFor(folder)) ?? docs[0]!;
}

// ---------------------------------------------------------------- a v0 from before it was one file

/** The files of a v0 from before. */
export const OLD = { idea: 'v0/idea.md', outline: 'v0/outline.md', script: 'v0/script.md' } as const;

/** A talk-out's marker line or placeholder in the old files: Ignition's own, never the person's words. */
const OLD_MARKS = /^\s*(?:<!--\s*talk-out\s+\S+\s*-->|\(transcribing…\))\s*$/;

/** A file's text for the one document: without the old talk-out marks, without blank lines at either end. */
function body(text: string): string {
  return text
    .replace(/\r\n?/g, '\n')
    .split('\n')
    .filter((l) => !OLD_MARKS.test(l))
    .join('\n')
    .replace(/^\s*\n/, '')
    .replace(/\s+$/, '');
}

/** Whether a text starts with a `# ` heading of its own (its first line that isn't blank). */
const titled = (text: string): boolean => /^#[ \t]/.test(text.trimStart());

/**
 * The one document a v0 from before becomes: idea.md, then outline.md, then script.md, each as it is, the outline and
 * the script under `## Outline` and `## Script` when they don't start with a `# ` heading of their own, with a blank
 * line between. Ignition's own talk-out marks (`<!-- talk-out … -->`, "(transcribing…)") are left out.
 */
export function migrate(o: { idea?: string | null; outline?: string | null; script?: string | null }): string {
  const parts: string[] = [];
  const idea = body(o.idea ?? '');
  if (idea) parts.push(idea);
  for (const [text, title] of [
    [o.outline, 'Outline'],
    [o.script, 'Script'],
  ] as const) {
    const b = body(text ?? '');
    if (!b) continue;
    parts.push(titled(b) ? b : `## ${title}\n\n${b}`);
  }
  return parts.length ? `${parts.join('\n\n')}\n` : '';
}

// ---------------------------------------------------------------- talk-outs: v0/voice/<stamp>.{webm,words.json,md}

export interface Sidecar {
  /** The audio's file name: "20261101-190211.webm". */
  audio: string;
  /** Its words are in the document (or answered a question): nothing left to put in. */
  inserted: boolean;
  text: string;
}

/** voice/<stamp>.md: the audio's name, whether its words are in the document, and the words. */
export const sidecarText = (s: Sidecar): string =>
  `Audio: ${s.audio}\nInserted: ${s.inserted ? 'yes' : 'no'}\n\n${s.text.trim()}\n`;

/** A sidecar read back; one from before (just the words) counts as inserted. */
export function parseSidecar(md: string): Sidecar {
  const m = /^Audio:[ \t]*(.*)\n(?:Inserted:[ \t]*(yes|no)[ \t]*\n)?\n?([\s\S]*)$/.exec(md.replace(/\r\n?/g, '\n'));
  if (!m) return { audio: '', inserted: true, text: md.trim() };
  return { audio: m[1]!.trim(), inserted: m[2] !== 'no', text: m[3]!.trim() };
}

const AUDIO = /\.(webm|ogg|opus|m4a|mp3|wav)$/i;

export interface Waiting {
  /** "20261101-190211". */
  stamp: string;
  audio: string;
  words: string;
  /** The sidecar, when there is one (Inserted: no). */
  sidecar: string | null;
}

/**
 * The talk-outs whose words came while no page waited for them: a words file and audio in voice/, and no sidecar or
 * one that says "Inserted: no". `sidecars` gives the text of the sidecars there are. Oldest first.
 */
export function waiting(paths: readonly string[], sidecars: ReadonlyMap<string, string>): Waiting[] {
  const by = new Map<string, { audio?: string; words?: string; md?: string }>();
  for (const p of paths) {
    const m = /^v0\/voice\/([^/]+?)(\.words\.json|\.md|\.[^./]+)$/.exec(p);
    if (!m) continue;
    const g = by.get(m[1]!) ?? {};
    if (m[2] === '.words.json') g.words = p;
    else if (m[2] === '.md') g.md = p;
    else if (AUDIO.test(m[2]!)) g.audio = p;
    by.set(m[1]!, g);
  }
  const out: Waiting[] = [];
  for (const [stamp, g] of by) {
    if (!g.words || !g.audio) continue;
    if (g.md) {
      const md = sidecars.get(g.md);
      if (md === undefined || parseSidecar(md).inserted) continue;
    }
    out.push({ stamp, audio: g.audio, words: g.words, sidecar: g.md ?? null });
  }
  return out.sort((a, b) => (a.stamp < b.stamp ? -1 : a.stamp > b.stamp ? 1 : 0));
}

/** A words file's text, or "" when it isn't one. */
export function wordsOf(json: string): string {
  try {
    return wordsText(JSON.parse(json));
  } catch {
    return '';
  }
}
