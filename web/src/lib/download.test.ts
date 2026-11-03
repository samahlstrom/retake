import { describe, expect, it, vi } from 'vitest';
import { Downloads, downloadHref, noteText, progressText, sizeText, type DownloadState } from './download.svelte';

const st = (over: Partial<DownloadState>): DownloadState => ({
  label: 'v5',
  name: 'clip_v5.mp4',
  state: 'none',
  progress: null,
  size: 0,
  as_is: false,
  error: '',
  license: '',
  ...over,
});

describe('download entry text', () => {
  it('shows how far it is, then the size', () => {
    expect(progressText(undefined)).toBe('');
    expect(progressText(st({ state: 'none' }))).toBe('');
    expect(progressText(st({ state: 'checking' }))).toBe('Checking…');
    expect(progressText(st({ state: 'making', progress: 0.427 }))).toBe('42%');
    expect(progressText(st({ state: 'making' }))).toBe('Preparing…');
    expect(progressText(st({ state: 'ready', size: 81_234_567 }))).toBe('81 MB');
    expect(sizeText(2_400_000_000)).toBe('2.4 GB');
    expect(sizeText(1200)).toBe('1 KB');
  });
  it('has one quiet line: the failure, else the license note', () => {
    expect(noteText(st({ license: 'Has 2 assets marked review only' }))).toBe('Has 2 assets marked review only');
    expect(
      noteText(st({ state: 'failed', error: "Couldn't make the MP4: ffmpeg isn't installed", license: 'x' })),
    ).toBe("Couldn't make the MP4: ffmpeg isn't installed");
    expect(noteText(st({ state: 'unavailable', error: 'v8 is still rendering' }))).toBe('v8 is still rendering');
    expect(noteText(undefined)).toBe('');
  });
  it('links to the file', () => {
    expect(downloadHref('v5-draft2')).toBe('video/v5-draft2/download');
  });
});

describe('Downloads', () => {
  const server = (answers: Partial<DownloadState>[]) => {
    const calls: string[] = [];
    const f = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${String(url)}`);
      const a = answers.length > 1 ? answers.shift()! : answers[0];
      return new Response(JSON.stringify(st(a)), { status: 200 });
    }) as unknown as typeof fetch;
    return { f, calls };
  };
  const settle = () => new Promise((r) => setTimeout(r, 20));

  it('gets it ready, follows it, and clicks the link once it is', async () => {
    const { f, calls } = server([
      { state: 'checking' },
      { state: 'making', progress: 0.5 },
      { state: 'ready', size: 9 },
    ]);
    const click = vi.fn();
    const d = new Downloads({ fetch: f, click, every: 1 });
    await d.get('v5');
    for (let i = 0; i < 20 && d.byLabel.v5?.state !== 'ready'; i++) await settle();
    expect(d.byLabel.v5.state).toBe('ready');
    expect(calls[0]).toBe('POST api/download?v=v5');
    expect(calls.slice(1).every((c) => c === 'GET api/download?v=v5')).toBe(true);
    expect(click).toHaveBeenCalledOnce();
    expect(click).toHaveBeenCalledWith('video/v5/download', 'clip_v5.mp4');
    d.stop();
  });

  it('only looks when the menu opens, and never clicks for it', async () => {
    const { f, calls } = server([{ state: 'ready', size: 9 }]);
    const click = vi.fn();
    const d = new Downloads({ fetch: f, click, every: 1 });
    await d.refresh('v5');
    await settle();
    expect(calls).toEqual(['GET api/download?v=v5']);
    expect(click).not.toHaveBeenCalled();
  });

  it('says what went wrong in one line', async () => {
    const f = vi.fn(
      async () => new Response(JSON.stringify({ error: 'downloads are off' }), { status: 403 }),
    ) as unknown as typeof fetch;
    const click = vi.fn();
    const d = new Downloads({ fetch: f, click });
    await d.get('v5');
    expect(d.byLabel.v5.state).toBe('failed');
    expect(noteText(d.byLabel.v5)).toBe('downloads are off');
    expect(click).not.toHaveBeenCalled();
  });
});
