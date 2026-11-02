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

/** POST api/notes `{version, text, anchor?}`: a typed comment on a kind's version. Resolves to the new item. */
export async function addTyped(o: {
  fetch: typeof fetch;
  version: string;
  text: string;
  anchor: Anchor | null;
}): Promise<{ id: number; [k: string]: unknown }> {
  const body: Record<string, unknown> = { version: o.version, text: o.text };
  if (o.anchor) body.anchor = o.anchor;
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
