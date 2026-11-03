import { describe, expect, it } from 'vitest';
import {
  agentChoices,
  anyItem,
  creatingWord,
  fmtSize,
  linkLine,
  menuItems,
  normalizeShares,
  shortDate,
  trashLine,
  type MenuContext,
} from './video-menu';

const ctx = (c: Partial<MenuContext>): MenuContext => ({ owner: true, local: true, agents: true, page: 'card', ...c });

describe('which items a video’s ⋯ shows', () => {
  it('gives a local owner’s card all of them but Download', () => {
    expect(menuItems(ctx({}))).toEqual({ share: true, agent: true, download: false, reveal: true, trash: true });
  });
  it('keeps Show in folder and Move to Trash to a local home', () => {
    const hosted = menuItems(ctx({ local: false }));
    expect(hosted).toMatchObject({ share: true, agent: true, reveal: false, trash: false });
  });
  it('has Share, Agent and Download on the review page', () => {
    expect(menuItems(ctx({ page: 'review', download: { label: 'Download v5 (MP4)' } }))).toEqual({
      share: true,
      agent: true,
      download: true,
      reveal: false,
      trash: false,
    });
  });
  it('gives an approver Share… only', () => {
    expect(menuItems(ctx({ owner: false, approver: true }))).toEqual({
      share: true,
      agent: false,
      download: false,
      reveal: false,
      trash: false,
    });
  });
  it('shows nothing to someone who isn’t the owner (no ⋯ at all) until Download is there', () => {
    const m = menuItems(ctx({ owner: false }));
    expect(anyItem(m)).toBe(false);
    expect(anyItem(menuItems(ctx({ owner: false, download: { label: 'Download' } })))).toBe(true);
  });
  it('leaves out the agent list when no computer offers an agent, and keeps it while that isn’t known', () => {
    expect(menuItems(ctx({ agents: false })).agent).toBe(false);
    expect(menuItems(ctx({ agents: null })).agent).toBe(true);
  });
});

describe('the share sheet', () => {
  const now = new Date('2026-11-02T12:00:00Z');
  it('names a link by who it’s for, its access and until when', () => {
    expect(
      linkLine({ id: 1, role: 'reviewer', label: 'Alex', created: '', expires: '2026-11-09T12:00:00Z' }, now),
    ).toEqual({ who: 'Alex', detail: 'Can comment · until Nov 9' });
    expect(linkLine({ id: 2, role: 'viewer', label: '', created: '', expires: '' }, now)).toEqual({
      who: 'Anyone with the link',
      detail: 'Can watch · never expires',
    });
  });
  it('adds the year only when it isn’t this one', () => {
    expect(shortDate('2027-01-05T12:00:00Z', now)).toBe('Jan 5, 2027');
    expect(shortDate('nonsense', now)).toBe('');
  });
  it('says a tunnel is starting only when one will be', () => {
    expect(creatingWord({ kind: 'tunnel', host: '', note: '' })).toBe('Starting a tunnel…');
    expect(creatingWord({ kind: 'tunnel', host: 'x.trycloudflare.com', note: '' })).toBe('Creating…');
    expect(creatingWord({ kind: 'lan', host: '192.168.1.2:8777', note: '…' })).toBe('Creating…');
  });
  it('reads GET api/shares, whatever is missing', () => {
    expect(normalizeShares({ links: [{ id: 3, role: 'viewer' }, { role: 'x' }], reach: { kind: 'hosted' } })).toEqual({
      links: [{ id: 3, role: 'viewer', label: '', created: '', expires: '' }],
      reach: { kind: 'hosted', host: '', note: '' },
    });
    expect(normalizeShares(null)).toEqual({ links: [], reach: { kind: '', host: '', note: '' } });
  });
});

describe('Move to Trash', () => {
  it('says the size in plain units', () => {
    expect(fmtSize(0)).toBe('0 KB');
    expect(fmtSize(12 * 1024)).toBe('12 KB');
    expect(fmtSize(820 * 1024 * 1024)).toBe('820 MB');
    expect(fmtSize(2.4 * 1024 ** 3)).toBe('2.4 GB');
    expect(fmtSize(1024 ** 3)).toBe('1 GB');
  });
  it('counts the versions', () => {
    expect(trashLine({ versions: 6, size: 2.4 * 1024 ** 3 })).toBe('6 versions · 2.4 GB');
    expect(trashLine({ versions: 1, size: 5 * 1024 * 1024 })).toBe('1 version · 5 MB');
  });
});

describe('the agent for this video', () => {
  const state = {
    config: { worker: 'w1', agent: 'claude' },
    workers: [
      {
        id: 'w1',
        agents: [
          { id: 'claude', name: 'Claude Code' },
          { id: 'codex', name: 'Codex' },
        ],
      },
      { id: 'w2', agents: [{ id: 'gemini', name: 'Gemini' }] },
    ],
  };
  it('lists the selected computer’s agents, the default checked through “Use my default”', () => {
    expect(agentChoices(state)).toEqual({
      agents: [
        { id: 'claude', name: 'Claude Code', checked: false },
        { id: 'codex', name: 'Codex', checked: false },
      ],
      defaultName: 'Claude Code',
      useDefault: true,
    });
  });
  it('checks the video’s own agent', () => {
    const c = agentChoices({ ...state, video_agent: 'codex' });
    expect(c.agents.find((a) => a.checked)?.id).toBe('codex');
    expect(c.useDefault).toBe(false);
  });
  it('falls back to the default when the computer doesn’t offer the video’s agent any more', () => {
    expect(agentChoices({ ...state, video_agent: 'gemini' }).useDefault).toBe(true);
  });
  it('takes every computer’s agents before one is selected, and the first as the default', () => {
    const c = agentChoices({ config: { worker: '', agent: '' }, workers: state.workers });
    expect(c.agents.map((a) => a.id)).toEqual(['claude', 'codex', 'gemini']);
    expect(c.defaultName).toBe('Claude Code');
    expect(agentChoices(null)).toEqual({ agents: [], defaultName: '', useDefault: true });
  });
});
