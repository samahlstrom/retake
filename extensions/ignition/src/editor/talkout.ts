// A talk-out (spec/ignition.md, "Talking it out"): R on v0 records audio; when it stops, Ignition keeps it as
// v0/voice/<stamp>.webm, transcribes it, puts the words in the document at the caret as a plain paragraph, writes the
// sidecar v0/voice/<stamp>.md, and adds an Open comment on that paragraph. Words that came back while no page waited
// for them go in the next time the document opens (resume).
import type { Comments, Ctx, Files, Recorded } from '../types';
import { anchorAt } from '../lib/doc';
import { hintOf, isChanged, sayError } from '../lib/errors';
import { stamp } from '../lib/stamp';
import { parseSidecar, sidecarText, waiting, wordsOf } from '../lib/v0';
import type { Doc } from './doc.svelte';

export const TALK_OUT_TODO = 'Work this talk-out into the document.';

export interface TalkOutDeps {
  files: Files;
  comments: Comments;
  transcribe: Ctx['transcribe'];
  toast(text: string): void;
  doc: Doc;
  /** Whether the caret is still at `pos`, where the words go (then it moves past them). */
  follow?(pos: number): boolean;
}

/** Where the words go: the caret when the talk-out stopped, in the text it was in; null for the end. */
export type At = { pos: number; was: string } | null;

/** The audio's file extension for its type (Opus in WebM, as the page records takes). */
export function audioExt(type: string): string {
  if (/ogg/i.test(type)) return 'ogg';
  if (/mp4|aac|m4a/i.test(type)) return 'm4a';
  return 'webm';
}

/** Keeps the audio as v0/voice/<stamp>.<ext> (or <stamp>-2, … in the same second). Returns its stamp and name. */
async function keepAudio(files: Files, a: Recorded, base: string): Promise<{ st: string; name: string }> {
  const ext = audioExt(a.type);
  for (let n = 1; ; n++) {
    const st = n === 1 ? base : `${base}-${n}`;
    try {
      await files.write(`v0/voice/${st}.${ext}`, a.blob, { ifMatch: '*' });
      return { st, name: `${st}.${ext}` };
    } catch (e) {
      if (!isChanged(e) || n >= 9) throw e;
    }
  }
}

/**
 * Puts a talk-out's words in the document and says so in its sidecar: claims the sidecar first ("Inserted: no", so
 * two pages never both put it in), then the paragraph and its To do, then "Inserted: yes". `sha` is the sidecar's
 * when there already is one. Words the document already holds aren't put in twice.
 */
async function place(
  d: Pick<TalkOutDeps, 'files' | 'comments' | 'doc' | 'toast' | 'follow'>,
  t: { st: string; audio: string; text: string; sha: string | null },
  at: At,
): Promise<void> {
  const path = `v0/voice/${t.st}.md`;
  let sha: string;
  try {
    sha = (
      await d.files.write(path, sidecarText({ audio: t.audio, inserted: false, text: t.text }), {
        ifMatch: t.sha ?? '*',
      })
    ).sha256;
  } catch (e) {
    if (!isChanged(e)) d.toast(`The talk-out's words weren't saved: ${sayError(e)}`);
    return; // another page has it
  }
  const doc = d.doc;
  await doc.ready;
  if (t.text && !doc.text.includes(t.text)) {
    const start = await doc.insert(t.text, at, d.follow);
    try {
      await d.comments.add({ text: TALK_OUT_TODO, anchor: anchorAt(doc.path, doc.text, start) });
    } catch (e) {
      d.toast(`The to-do wasn't added: ${sayError(e)}`);
    }
  }
  try {
    await d.files.write(path, sidecarText({ audio: t.audio, inserted: true, text: t.text }), { ifMatch: sha });
  } catch {
    /* the words are in: the next open sees them there and doesn't put them in again */
  }
}

export async function talkOut(a: Recorded, d: TalkOutDeps, at: At, now = new Date()): Promise<void> {
  const started = new Date(a.started);
  let kept: { st: string; name: string };
  try {
    kept = await keepAudio(d.files, a, stamp(Number.isNaN(started.getTime()) ? now : started));
  } catch (e) {
    d.toast(`The talk-out wasn't saved: ${sayError(e)}`);
    return;
  }
  let text: string;
  try {
    text = (await d.transcribe(`v0/voice/${kept.name}`)).text.replace(/\s+/g, ' ').trim();
  } catch (e) {
    // No transcription: the audio stays, nothing goes in the document, and the job's one-line fix shows.
    d.toast(hintOf(e).split('\n')[0]!.trim());
    return;
  }
  if (!text) d.toast('No words heard in that talk-out');
  await place(d, { st: kept.st, audio: kept.name, text, sha: null }, at);
}

/**
 * The talk-outs whose words came back after their page was gone (spec/ignition.md: "the next time the document opens,
 * Ignition puts in the ones it hasn't yet"): each goes in at the end, oldest first. Returns how many went in.
 */
export async function resume(d: Pick<TalkOutDeps, 'files' | 'comments' | 'doc' | 'toast'>): Promise<number> {
  const list = (await d.files.list('v0/voice/*')).filter((f) => !f.deleted);
  const paths = list.map((f) => f.path);
  const has = new Set(paths);
  const sidecars = new Map<string, { text: string; sha: string }>();
  for (const p of paths) {
    if (!p.endsWith('.md') || !has.has(p.replace(/\.md$/, '.words.json'))) continue;
    try {
      const r = await d.files.read(p);
      sidecars.set(p, { text: await r.text(), sha: r.sha256 });
    } catch {
      /* gone since the list: leave it */
    }
  }
  let n = 0;
  const texts = new Map([...sidecars].map(([p, s]) => [p, s.text]));
  for (const w of waiting(paths, texts)) {
    let text = '';
    try {
      text = wordsOf(await (await d.files.read(w.words)).text());
    } catch {
      continue;
    }
    const side = w.sidecar ? sidecars.get(w.sidecar) : undefined;
    if (side && !text) text = parseSidecar(side.text).text;
    await place(d, { st: w.stamp, audio: w.audio.split('/').pop()!, text, sha: side?.sha ?? null }, null);
    n++;
  }
  return n;
}
