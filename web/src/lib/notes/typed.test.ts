import { describe, expect, it, vi } from 'vitest';
import { addTyped, composeKey } from './typed';

describe("the comments column's line on an extension's version", () => {
  it('Enter adds, Ctrl+Enter or ⌘+Enter adds and sends, Shift+Enter is a new line', () => {
    expect(composeKey({ key: 'Enter' })).toBe('add');
    expect(composeKey({ key: 'Enter', ctrlKey: true })).toBe('send');
    expect(composeKey({ key: 'Enter', metaKey: true })).toBe('send');
    expect(composeKey({ key: 'Enter', shiftKey: true })).toBeNull();
    expect(composeKey({ key: 'Enter', isComposing: true })).toBeNull(); // an IME still composing
    expect(composeKey({ key: 'r' })).toBeNull();
  });

  it('adds a typed comment with POST api/notes, with the anchor the slot gave', async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ id: 12, key: 'doc/1' }), { status: 201 }));
    const anchor = { file: 'v0/jump-souls_v0.md', line: 9, heading: 'Cold open' };
    const item = await addTyped({
      fetch: fetch as unknown as typeof globalThis.fetch,
      version: 'v0',
      text: 'Tighter',
      anchor,
    });
    expect(item.id).toBe(12);
    expect(fetch).toHaveBeenCalledWith('api/notes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ version: 'v0', text: 'Tighter', anchor }),
    });
    await addTyped({
      fetch: fetch as unknown as typeof globalThis.fetch,
      version: 'v0',
      text: 'No place',
      anchor: null,
    });
    expect(JSON.parse((fetch.mock.calls[1] as unknown as [string, RequestInit])[1].body as string)).toEqual({
      version: 'v0',
      text: 'No place',
    });
  });

  it("says what went wrong: the server's error, or that it isn't answering", async () => {
    const refused = (async () =>
      new Response(JSON.stringify({ error: 'typed comments are only on an extension version' }), {
        status: 400,
      })) as unknown as typeof fetch;
    await expect(addTyped({ fetch: refused, version: 'v1', text: 'x', anchor: null })).rejects.toThrow(
      'typed comments are only on an extension version',
    );
    const down = (async () => {
      throw new TypeError('Failed to fetch');
    }) as unknown as typeof fetch;
    await expect(addTyped({ fetch: down, version: 'v0', text: 'x', anchor: null })).rejects.toThrow('isn’t answering');
  });
});
