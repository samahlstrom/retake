// The comments column's line on an extension's version (spec/extensions.md, "Typing one"): "Comment for the agent…".
// Enter adds a typed comment anchored where the version slot says (ctx.onAnchor); Ctrl+Enter (⌘+Enter) adds it and
// sends it. Pure parts, so they're unit-tested; the page wires them up (lib/review.svelte.ts).
import type { Anchor } from '../../ext/retake-ext';

/** What a key in the line does: add, add and send, or nothing (a plain key of the text; Shift+Enter is a new line). */
export function composeKey(e: {
  key: string;
  shiftKey?: boolean;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  isComposing?: boolean;
}): 'add' | 'send' | null {
  if (e.key !== 'Enter' || e.isComposing || e.shiftKey || e.altKey) return null;
  return e.ctrlKey || e.metaKey ? 'send' : 'add';
}

/** POST api/notes: a typed comment. On a kind's version `{version, text, anchor?}`; on a video `{version, t, text,
 * strokes, frame?, snapshot?}` (spec/notes.md, "Typed comments on a video"). Resolves to the new item. */
export async function addTyped(o: {
  fetch: typeof fetch;
  version: string;
  text: string;
  anchor: Anchor | null;
  /** On a video: its time, its lines, the video's size and a still of its moment (a data: URL). */
  t?: number;
  strokes?: { color: string; points: [number, number][]; t: number }[];
  frame?: [number, number];
  snapshot?: string;
}): Promise<{ id: number; [k: string]: unknown }> {
  const body: Record<string, unknown> = { version: o.version, text: o.text };
  if (o.anchor) body.anchor = o.anchor;
  if (typeof o.t === 'number') {
    body.t = Math.round(o.t * 1000) / 1000;
    body.strokes = (o.strokes ?? []).map((st) => ({
      color: st.color,
      points: st.points.slice(0, 2000).map(([x, y]) => [round4(x), round4(y)]),
      t: Math.round(st.t * 1000) / 1000,
    }));
    if (o.frame) body.frame = o.frame;
    if (o.snapshot) body.snapshot = o.snapshot;
  }
  let r: Response;
  try {
    r = await o.fetch('api/notes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  } catch {
    throw new Error('Could not add the comment: the server isn’t answering');
  }
  const out = (await r.json().catch(() => ({}))) as { id?: unknown; error?: unknown };
  if (!r.ok || typeof out.id !== 'number') {
    throw new Error(typeof out.error === 'string' && out.error ? out.error : 'Could not add the comment');
  }
  return out as { id: number };
}

const round4 = (n: number): number => Math.round(Math.min(1, Math.max(0, n)) * 1e4) / 1e4;
