import { describe, expect, it, vi } from 'vitest';
import { CONNECT_FALLBACK, connectCommand, type EditingTask } from './automation.svelte';
import { inCurrentReview, markOf, toRow } from './notes/view';

describe('live comment tasks', () => {
  const task = (state: string): EditingTask => ({
    id: 'task',
    state,
    notes: [1],
    version: 'v6',
    agent: 'codex',
    worker: 'machine',
    attempt: 1,
    events: [],
  });
  it('keeps the original annotation and version while surfacing the task question', () => {
    const item = {
      id: 1,
      kind: 'note' as const,
      status: 'open',
      t0: 12,
      text: 'Change this title',
      version: 'v6',
      drawings: ['red'],
      work: { ...task('question'), question: 'Blue or teal?' },
    };
    expect(markOf(item)).toBe('question');
    const row = toRow(item, false, { versions: [{ label: 'v6' }, { label: 'v7' }], shown: 'v7' });
    expect(row.version).toBe('v6');
    expect(row.drawings).toEqual(['red']);
    expect(row.work?.question).toBe('Blue or teal?');
    expect(inCurrentReview(row, 'v7')).toBe(true);
    expect(inCurrentReview({ ...row, work: undefined }, 'v7')).toBe(false);
  });
});

describe('connect command (C5)', () => {
  const answer = (status: number, body: unknown) =>
    vi.fn(async () => new Response(JSON.stringify(body), { status })) as unknown as typeof fetch;
  it('uses the line the server minted', async () => {
    const f = answer(200, { command: 'retake connect https://review.example.com --token rk_abc' });
    expect(await connectCommand('api/connect', f)).toBe('retake connect https://review.example.com --token rk_abc');
    expect(f).toHaveBeenCalledWith('api/connect', { method: 'POST' });
  });
  it('falls back to the CLI line on an older server, a refusal or no network', async () => {
    expect(await connectCommand('api/connect', answer(404, { error: 'not found' }))).toBe(CONNECT_FALLBACK);
    expect(await connectCommand('api/connect', answer(403, { error: 'owner only' }))).toBe(CONNECT_FALLBACK);
    expect(await connectCommand('api/connect', answer(200, { command: '' }))).toBe(CONNECT_FALLBACK);
    const down = vi.fn(async () => {
      throw new TypeError('network');
    }) as unknown as typeof fetch;
    expect(await connectCommand('api/connect', down)).toBe(CONNECT_FALLBACK);
  });
});
