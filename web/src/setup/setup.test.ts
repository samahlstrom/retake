import { describe, expect, it, vi } from 'vitest';
import {
  ffmpegLine,
  installSteps,
  normalizeState,
  placeText,
  placesShown,
  post,
  tryLine,
  videosLine,
  waitForHome,
} from './setup';
import { join, listFolders } from '../lib/folders';

const base = {
  folder: '~/Movies',
  folder_path: '/Users/ana/Movies',
  folder_exists: true,
  videos: 3,
  agents: [
    { id: 'claude', name: 'Claude Code' },
    { id: 'codex', name: 'Codex' },
  ],
  agent: 'codex',
  open_in: 'herdr',
  places: [
    { id: 'herdr', available: true, bundled: true },
    { id: 'tmux', available: false },
    { id: 'background', available: true },
  ],
  ffmpeg: true,
  os: 'darwin',
  config: '~/.config/retake/config.toml',
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

describe('the setup state', () => {
  it('normalizes the answer, filling in what is missing or wrong', () => {
    const s = normalizeState(base);
    expect(s.agent).toBe('codex');
    expect(s.open_in).toBe('herdr');
    expect(s.places.find((p) => p.id === 'herdr')?.bundled).toBe(true);
    const bad = normalizeState({ ...base, agent: 'gone', open_in: 'tmux', places: [{ id: 'screen' }], videos: '3' });
    expect(bad.agent).toBe('claude'); // not offered: the first
    expect(bad.open_in).toBe('background'); // tmux isn't available
    expect(bad.places).toEqual([{ id: 'background', available: true, bundled: false }]);
    expect(bad.videos).toBeNull();
    expect(normalizeState(null)).toMatchObject({ agents: [], agent: '', open_in: 'background', os: 'linux' });
  });

  it('says how many videos, plainly', () => {
    expect(videosLine({ videos: 1, folder_exists: true })).toBe('1 video');
    expect(videosLine({ videos: 3, folder_exists: true })).toBe('3 videos');
    expect(videosLine({ videos: 0, folder_exists: true })).toMatch(/No videos in it yet/);
    expect(videosLine({ videos: 99, folder_exists: true })).toBe('99+ videos');
    expect(videosLine({ videos: null, folder_exists: false })).toMatch(/makes it/);
  });

  it('gives the makers’ own install lines per OS', () => {
    const mac = installSteps('darwin');
    expect(mac.map((s) => s.agent)).toEqual(['Claude Code', 'Codex']);
    expect(mac[0]!.install).toBe('curl -fsSL https://claude.ai/install.sh | bash');
    expect(mac[1]!.install).toBe('curl -fsSL https://chatgpt.com/codex/install.sh | sh');
    expect(installSteps('linux')).toEqual(mac);
    expect(installSteps('windows')[0]!.install).toMatch(/install\.ps1/);
    expect(ffmpegLine('darwin')).toBe('brew install ffmpeg');
  });

  it('offers herdr and the background, and tmux only when it is there', () => {
    const s = normalizeState(base);
    expect(placesShown(s).map((p) => p.id)).toEqual(['herdr', 'background']);
    const tmux = normalizeState({ ...base, places: [...base.places.slice(0, 1), { id: 'tmux', available: true }] });
    expect(placesShown(tmux).map((p) => p.id)).toEqual(['herdr', 'tmux', 'background']);
    expect(placeText({ id: 'herdr', available: true, bundled: true }).note).toMatch(/Comes with Retake/);
    expect(placeText({ id: 'background', available: true, bundled: false }).label).toBe('In the background');
  });

  it('says a test’s result in one line', () => {
    expect(tryLine({ ok: true, reply: 'ready', seconds: 4.2 })).toBe('Works · 4 s');
    expect(tryLine({ ok: true, seconds: 0.3 })).toBe('Works · <1 s');
    expect(tryLine({ ok: false, error: 'Not signed in: run claude once', seconds: 1 })).toBe(
      'Not signed in: run claude once',
    );
    expect(tryLine({ ok: false, seconds: 1 })).toBe('It didn’t answer.');
  });

  it('posts and throws the server’s reason', async () => {
    const f = vi.fn(() => Promise.resolve(json({ error: 'no agent "x" here' }, 400)));
    await expect(post('api/setup', { agent: 'x' }, f as unknown as typeof fetch)).rejects.toThrow('no agent "x" here');
    const down = vi.fn(() => Promise.reject(new Error('refused')));
    await expect(post('api/setup', {}, down as unknown as typeof fetch)).rejects.toThrow(/isn’t answering/);
    const ok = vi.fn(() => Promise.resolve(json({ ok: true })));
    await expect(post('api/setup/done', {}, ok as unknown as typeof fetch)).resolves.toEqual({ ok: true });
  });

  it('waits for the home after Done', async () => {
    let n = 0;
    const f = vi.fn(() => {
      n++;
      if (n === 1) return Promise.reject(new Error('switching'));
      return Promise.resolve(n < 3 ? json({ error: 'setting up' }, 503) : json({ projects: [] }));
    });
    await expect(waitForHome(f as unknown as typeof fetch, 5000, 1)).resolves.toBe(true);
    expect(n).toBe(3);
    const never = vi.fn(() => Promise.resolve(json({}, 503)));
    await expect(waitForHome(never as unknown as typeof fetch, 20, 5)).resolves.toBe(false);
  });
});

describe('the folder list', () => {
  it('joins names the way the server reads them', () => {
    expect(join('~', 'Movies')).toBe('~/Movies');
    expect(join('/', 'Volumes')).toBe('/Volumes');
    expect(join('~/Movies', 'Clips')).toBe('~/Movies/Clips');
  });

  it('lists a folder and says when it is gone', async () => {
    const f = vi.fn((url: string) =>
      Promise.resolve(
        url.includes('nope')
          ? json({ error: 'no such folder' }, 404)
          : json({ path: '~', parent: '/home', folders: ['A', 2] }),
      ),
    );
    const l = await listFolders('api/setup/folders', '~', f as unknown as typeof fetch);
    expect(f).toHaveBeenCalledWith('api/setup/folders?path=~', { cache: 'no-store' });
    expect(l).toEqual({ path: '~', parent: '/home', folders: ['A'] });
    await expect(listFolders('api/setup/folders', '~/nope', f as unknown as typeof fetch)).rejects.toThrow(
      /isn’t there/,
    );
  });
});
