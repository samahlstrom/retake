import { describe, expect, it, vi } from 'vitest';
import {
  agentChange,
  agentOptions,
  computersText,
  effectiveAgent,
  fallbackOptions,
  loadSettings,
  normalizeSettings,
  pendingRestart,
  saveSettings,
} from './settings';

const base = {
  hosted: false,
  agents: [
    { id: 'claude', name: 'Claude Code' },
    { id: 'codex', name: 'Codex' },
  ],
  agent: '',
  fallback: '',
  open_in: 'background',
  open_in_editable: true,
  extensions: [{ id: 'ignition', name: 'Ignition', on: true, loaded: false }],
  extensions_editable: true,
  theme: { folder: '~/.config/retake/theme', used: false },
  config: '~/.config/retake/config.toml',
  machines: [{ id: 'w1', name: 'sam-pc', online: true, agents: [{ id: 'claude', name: 'Claude Code' }] }],
};

describe('settings', () => {
  it('normalizes the answer, filling in what is missing or wrong', () => {
    const s = normalizeSettings({ ...base, open_in: 'screen', agents: [{ id: 'x' }, 3], extensions: null });
    expect(s.open_in).toBe('background');
    expect(s.agents).toEqual([{ id: 'x', name: 'x' }]);
    expect(s.extensions).toEqual([]);
    expect(normalizeSettings(null)).toMatchObject({ hosted: false, agents: [], machines: [], theme: { folder: '' } });
  });

  it('shows the agent tasks run with, and only other agents as the fallback', () => {
    const s = normalizeSettings(base);
    expect(effectiveAgent(s)).toBe('claude'); // none chosen: the first found
    expect(agentOptions(s).map((o) => o.value)).toEqual(['claude', 'codex']);
    expect(fallbackOptions(s)).toEqual([
      { value: '', label: 'Pause and ask me' },
      { value: 'codex', label: 'Switch to Codex' },
    ]);
    const gone = normalizeSettings({ ...base, agent: 'gemini' });
    expect(effectiveAgent(gone)).toBe('claude');
    expect(agentOptions(gone).at(-1)).toEqual({ value: 'gemini', label: 'gemini (not found)', disabled: true });
  });

  it('a new default that was the fallback clears the fallback', () => {
    const s = normalizeSettings({ ...base, agent: 'claude', fallback: 'codex' });
    expect(agentChange(s, 'codex')).toEqual({ agent: 'codex', fallback: '' });
    expect(agentChange(s, 'claude')).toEqual({ agent: 'claude' });
  });

  it('says what waits for a restart, and which computers are online', () => {
    const s = normalizeSettings(base);
    expect(pendingRestart(s).map((e) => e.id)).toEqual(['ignition']);
    expect(computersText(s)).toBe('sam-pc');
    expect(computersText(normalizeSettings({ ...base, machines: [] }))).toBe('None connected yet');
  });

  it('saves a change and says why one was refused', async () => {
    const ok = vi.fn(async () => new Response(JSON.stringify({ ...base, agent: 'codex' }), { status: 200 }));
    expect((await saveSettings({ agent: 'codex' }, ok as unknown as typeof fetch)).agent).toBe('codex');
    expect(ok).toHaveBeenCalledWith(
      'api/settings',
      expect.objectContaining({ method: 'POST', body: '{"agent":"codex"}' }),
    );
    const bad = async () => new Response(JSON.stringify({ error: 'no agent "x" here' }), { status: 400 });
    await expect(saveSettings({ agent: 'x' }, bad as unknown as typeof fetch)).rejects.toThrow('no agent "x" here');
    const down = async () => {
      throw new TypeError('offline');
    };
    await expect(saveSettings({}, down as unknown as typeof fetch)).rejects.toThrow('isn’t answering');
  });

  it('loads, or says why not', async () => {
    const forbidden = async () => new Response('{}', { status: 403 });
    expect((await loadSettings(forbidden as unknown as typeof fetch)).error).toMatch(/owner/);
    const ok = async () => new Response(JSON.stringify(base), { status: 200 });
    expect((await loadSettings(ok as unknown as typeof fetch)).settings?.config).toBe('~/.config/retake/config.toml');
  });
});
