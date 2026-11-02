import { describe, expect, it } from 'vitest';
import type { Card } from '../types';
import {
  createIdea,
  defaultProject,
  handOff,
  ideaFile,
  ideasOf,
  NEW_KEY,
  offline,
  projectsOf,
  rememberedSelection,
  TALK_KEY,
  takeHandOff,
} from './ideas';

const card = (name: string, o: Partial<Card> = {}): Card =>
  ({ name, title: name, path: name, project: '', ...o }) as Card;

class Mem {
  m = new Map<string, string>();
  getItem(k: string) {
    return this.m.get(k) ?? null;
  }
  setItem(k: string, v: string) {
    this.m.set(k, v);
  }
  removeItem(k: string) {
    this.m.delete(k);
  }
}

describe('the ideas', () => {
  it('are the videos with only v0, most recently edited first', () => {
    const home = {
      projects: [
        card('cut', { status: 'to_review', kinds: ['ignition.doc'], changed: 9 }),
        card('old', { status: 'no_cut', kind: 'ignition.doc', kinds: ['ignition.doc'], changed: 1 }),
        card('new', { status: 'no_cut', kind: 'ignition.doc', changed: 5 }),
        card('plain', { status: 'to_review' }),
      ],
    };
    expect(ideasOf(home, 'ignition.doc').map((c) => c.name)).toEqual(['new', 'old']);
  });
});

describe('New idea', () => {
  const home = {
    title: 'Videos',
    projects: [],
    tree: {
      title: 'Videos',
      path: '',
      project: false,
      children: [
        {
          title: 'games',
          path: 'games',
          project: false,
          children: [
            { title: 'deadlock', path: 'games/deadlock', project: true },
            { title: 'chess', path: 'games/chess', project: true },
          ],
        },
        { title: 'vlog', path: 'vlog', project: true },
      ],
    },
  };
  const projects = projectsOf(home);

  it("offers the tree's projects in its order", () => {
    expect(projects.map((p) => p.path)).toEqual(['games/deadlock', 'games/chess', 'vlog']);
    expect(projectsOf({ projects: [card('a', { project: 'x' })] }).map((p) => p.label)).toEqual(['x']);
    expect(projectsOf({ title: 'Home', projects: [] })).toEqual([{ path: '', label: 'Home' }]);
  });

  it('starts on the project selected in the tree, else the top one', () => {
    expect(defaultProject(projects, 'vlog')).toBe('vlog');
    expect(defaultProject(projects, 'games/chess/opening-traps')).toBe('games/chess');
    expect(defaultProject(projects, 'games')).toBe('games/deadlock');
    expect(defaultProject(projects, null)).toBe('games/deadlock');
    expect(defaultProject(projects, 'elsewhere')).toBe('games/deadlock');
    expect(defaultProject([], null)).toBe('');
  });

  it("reads the tree's selection the way the home keeps it", () => {
    const s = new Mem();
    s.setItem('retake.nav:/', JSON.stringify({ selected: 'vlog', expanded: [] }));
    expect(rememberedSelection(s, '/')).toBe('vlog');
    expect(rememberedSelection(s, '/index.html')).toBe('vlog');
    expect(rememberedSelection(s, '/other/')).toBeNull();
    expect(rememberedSelection(null, '/')).toBeNull();
  });

  it('is off while a hosted server has no editing computer', () => {
    expect(offline({ user: { name: 'a' }, machines: [{ id: '1', name: 'pc', online: false }] })).toBe(true);
    expect(offline({ user: { name: 'a' }, machines: [{ id: '1', name: 'pc', online: true }] })).toBe(false);
    expect(offline({ machines: [] })).toBe(false);
  });

  it('makes <project>/<slug>/v0/idea.md with the title and a blank line', async () => {
    const calls: unknown[] = [];
    const ctx = {
      createVideo: async (v: { project: string; name: string; files: Record<string, string> }) => {
        calls.push(v);
        return { name: `vlog/${v.name}`, url: `p/vlog%2F${v.name}/` };
      },
    };
    expect(await createIdea(ctx, { project: 'vlog', title: '  Jump  souls! ' })).toEqual({
      name: 'vlog/jump-souls',
      url: 'p/vlog%2Fjump-souls/',
    });
    expect(calls).toEqual([{ project: 'vlog', name: 'jump-souls', files: { 'v0/idea.md': '# Jump souls!\n\n' } }]);
    expect(ideaFile('A')).toBe('# A\n\n');
  });

  it('tries -2, -3, … while the folder exists', async () => {
    const names: string[] = [];
    const ctx = {
      createVideo: async (v: { name: string }) => {
        names.push(v.name);
        if (names.length < 3) throw Object.assign(new Error('exists'), { status: 409 });
        return { name: v.name, url: '' };
      },
    };
    expect((await createIdea(ctx, { project: '', title: 'Jump' })).name).toBe('jump-3');
    expect(names).toEqual(['jump', 'jump-2', 'jump-3']);
  });

  it('gives up on anything else (an offline computer)', async () => {
    const ctx = {
      createVideo: async () => {
        throw Object.assign(new Error('editing machine is offline'), { status: 503 });
      },
    };
    await expect(createIdea(ctx, { project: '', title: 'Jump' })).rejects.toThrow('offline');
  });

  it("hands Create and talk over to the new video's page once", () => {
    const s = new Mem();
    handOff(s, 'vlog/jump', true, 1000);
    expect(takeHandOff(s, NEW_KEY, 'vlog/jump', 1000)).toBe(false);
    expect(takeHandOff(s, TALK_KEY, 'other', 1000)).toBe(false);
    expect(takeHandOff(s, TALK_KEY, 'vlog/jump', 2000)).toBe(true);
    expect(takeHandOff(s, TALK_KEY, 'vlog/jump', 2000)).toBe(false);
    handOff(s, 'vlog/jump', false, 0);
    expect(takeHandOff(s, NEW_KEY, 'vlog/jump', 600_000)).toBe(false);
  });
});
