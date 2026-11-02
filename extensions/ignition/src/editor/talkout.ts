// A talk-out (spec/ignition.md, "Talking it out"): R on v0 records audio; when it stops, Ignition keeps it in
// v0/voice/, puts it in the open document at the caret, adds a To do for it, and fills in its words when they come.
// With a question selected, the talk-out answers it instead.
import type { Comments, Ctx, Files, Recorded } from '../types';
import { headingAbove, lineStart, marker, TRANSCRIBING } from '../lib/doc';
import { hintOf, isChanged, sayError } from '../lib/errors';
import { stamp } from '../lib/stamp';
import type { Doc } from './doc.svelte';

export const TALK_OUT_TODO = 'Work this talk-out into the outline and script.';

export interface TalkOutDeps {
  files: Files;
  comments: Comments;
  transcribe: Ctx['transcribe'];
  toast(text: string): void;
  /** The document and caret it goes in, read when it stops. */
  target(): { doc: Doc; pos: number };
  /** The question it answers (its thread), when one is selected. */
  question(): number | null;
  /** After a question was answered. */
  answered?(thread: number): void;
}

/** The audio's file extension for its type (Opus in WebM, as the page records takes). */
export function audioExt(type: string): string {
  if (/ogg/i.test(type)) return 'ogg';
  if (/mp4|aac|m4a/i.test(type)) return 'm4a';
  return 'webm';
}

/** Keeps the audio as v0/voice/<stamp>.<ext> (or <stamp>-2, … in the same second). Returns the stamp it got. */
async function keepAudio(files: Files, a: Recorded, base: string): Promise<{ st: string; rel: string }> {
  const ext = audioExt(a.type);
  for (let n = 1; ; n++) {
    const st = n === 1 ? base : `${base}-${n}`;
    const rel = `voice/${st}.${ext}`;
    try {
      await files.write(`v0/${rel}`, a.blob, { ifMatch: '*' });
      return { st, rel };
    } catch (e) {
      if (!isChanged(e) || n >= 9) throw e;
    }
  }
}

/** Its words, written to voice/<stamp>.md; or the reason there are none, shown as a toast. */
async function words(d: TalkOutDeps, rel: string, st: string): Promise<{ text: string; ok: boolean }> {
  try {
    const r = await d.transcribe(`v0/${rel}`);
    const text = r.text.replace(/\s+/g, ' ').trim() || '(no words heard)';
    try {
      await d.files.write(`v0/voice/${st}.md`, `${text}\n`);
    } catch {
      /* the words still go in the document */
    }
    return { text, ok: true };
  } catch (e) {
    const hint = hintOf(e).split('\n')[0]!.trim();
    d.toast(hint);
    return { text: `(not transcribed: ${hint})`, ok: false };
  }
}

export async function talkOut(a: Recorded, d: TalkOutDeps, now = new Date()): Promise<void> {
  const started = new Date(a.started);
  let kept: { st: string; rel: string };
  try {
    kept = await keepAudio(d.files, a, stamp(Number.isNaN(started.getTime()) ? now : started));
  } catch (e) {
    d.toast(`The talk-out wasn't saved: ${sayError(e)}`);
    return;
  }
  const { st, rel } = kept;

  const thread = d.question();
  if (thread !== null) {
    const w = await words(d, rel, st);
    if (!w.ok) return;
    try {
      await d.comments.answer(thread, w.text);
      d.answered?.(thread);
      d.toast('Answered');
    } catch (e) {
      d.toast(`The answer wasn't sent: ${sayError(e)}`);
    }
    return;
  }

  const { doc, pos } = d.target();
  const line = await doc.insertTalkOut(pos, rel, TRANSCRIBING);
  const heading = headingAbove(doc.text, lineStart(doc.text, line))?.text;
  try {
    await d.comments.add({
      text: TALK_OUT_TODO,
      anchor: { file: doc.path, line: line + 1, ...(heading ? { heading } : {}), quote: marker(rel) },
    });
  } catch (e) {
    d.toast(`The to-do wasn't added: ${sayError(e)}`);
  }
  const w = await words(d, rel, st);
  await doc.fillTalkOut(rel, w.text);
}
