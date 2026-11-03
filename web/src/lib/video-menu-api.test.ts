import { afterEach, describe, expect, it, vi } from 'vitest';
import { downloadLine, downloadVideo } from './video-menu-api';

afterEach(() => vi.unstubAllGlobals());

describe('Download in a video’s ⋯', () => {
  it('says nothing once the file is on its way, else that it gets ready or why it can’t', () => {
    expect(downloadLine({ state: 'ready' } as never)).toBe('');
    expect(downloadLine({ state: 'making' } as never)).toBe('Getting the MP4 ready. It downloads by itself.');
    expect(downloadLine({ state: 'failed', error: 'downloads are off' } as never)).toBe('downloads are off');
    expect(downloadLine({ state: 'unavailable', error: '' } as never)).toBe('Couldn’t get the MP4');
  });
  it('asks the card’s video for its version, under its own address', async () => {
    const urls: string[] = [];
    vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
      urls.push(`${init?.method ?? 'GET'} ${url}`);
      return new Response(JSON.stringify({ error: 'not allowed' }), { status: 403 });
    });
    expect(await downloadVideo('p/alpha/', 'v6-draft2')).toBe('not allowed');
    expect(urls).toEqual(['POST p/alpha/api/download?v=v6-draft2']);
  });
});
