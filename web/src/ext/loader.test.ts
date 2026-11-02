import { describe, expect, it, vi } from 'vitest';
import { EXPORTS, ExtLoader, failedLine, mountSlot, normalizeExts, SLOTS, type ExtInfo } from './loader';
import type { Host } from './ctx';
import type { Ctx, Module } from './retake-ext';

/** A stand-in for the slot's element: what mountSlot touches of it. */
class FakeNode {
  className = '';
  textContent = '';
  attrs: Record<string, string> = {};
  constructor(readonly tag: string) {}
  setAttribute(k: string, v: string) {
    this.attrs[k] = v;
  }
}
class FakeEl {
  children: unknown[] = [];
  ownerDocument = { createElement: (tag: string) => new FakeNode(tag) };
  replaceChildren(...nodes: unknown[]) {
    this.children = nodes;
  }
  append(...nodes: unknown[]) {
    this.children.push(...nodes);
  }
  get line(): string | null {
    const p = this.children[0];
    return p instanceof FakeNode && p.className === 'ext-error' ? p.textContent : null;
  }
}
const el = () => new FakeEl() as FakeEl & HTMLElement;

const apiExt = {
  api: 1,
  extensions: [
    {
      id: 'hello',
      name: 'Hello',
      version: '0.1.0',
      base: 'ext/hello/',
      module: 'ui/hello.js',
      style: 'ui/hello.css',
      nav: { title: 'Hello' },
      kinds: [],
      slots: [{ slot: 'page' }, { slot: 'sidebar-tab', title: 'Hello' }, { slot: 'nowhere' }],
      machine: true,
    },
    {
      id: 'ignition',
      name: 'Ignition',
      version: '1.0.0',
      base: 'ext/ignition/',
      module: 'ui/ignition.js',
      nav: { title: 'Ignition', icon: 'ui/spark.svg', list: 'kind-only', action: { title: 'New idea', route: 'new' } },
      kinds: [
        {
          id: 'ignition.doc',
          kind: 'doc',
          folder: 'v0',
          marker: 'idea.md',
          label: 'Script',
          status: 'Idea',
          record: true,
        },
      ],
      slots: [{ slot: 'version', kind: 'ignition.doc' }],
      machine: false,
    },
    { id: 'Bad Id', module: 'x.js' },
    'nonsense',
  ],
};

const host = (over: Partial<Host> = {}): Host => ({
  where: 'video',
  homeRoot: '/',
  role: 'owner',
  viewer: 'abc',
  toast: () => {},
  fetch: (async () => new Response('{}')) as typeof fetch,
  ...over,
});

function setup(modules: Record<string, () => Promise<unknown>> = {}) {
  const imported: string[] = [];
  const logs: unknown[][] = [];
  const fetch = vi.fn(async () => new Response(JSON.stringify(apiExt)));
  const loader = new ExtLoader({
    fetch: fetch as unknown as typeof globalThis.fetch,
    base: () => 'http://h/p/clip/',
    importer: (url) => {
      imported.push(url);
      const id = /ext\/([^/]+)\//.exec(url)?.[1] ?? '';
      return modules[id]?.() ?? Promise.reject(new Error(`Failed to fetch dynamically imported module: ${url}`));
    },
    log: (...a) => logs.push(a),
  });
  return { loader, fetch, imported, logs };
}

describe('GET api/ext', () => {
  it('is read once per page, and made safe', async () => {
    const { loader, fetch } = setup();
    const [a, b] = await Promise.all([loader.list(), loader.list()]);
    expect(a).toBe(b);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledWith('api/ext');
    expect(a.map((e) => e.id)).toEqual(['hello', 'ignition']);
    expect(a[0]).toMatchObject({ nav: { title: 'Hello', icon: '', list: '', action: null }, machine: true });
    expect(a[0]!.slots.map((s) => s.slot)).toEqual(['page', 'sidebar-tab']); // an unknown slot is left out
    expect(a[0]!.slots[0]!.when).toBe('always');
    expect(a[1]!.kinds[0]).toEqual({
      id: 'ignition.doc',
      kind: 'doc',
      folder: 'v0',
      marker: 'idea.md',
      label: 'Script',
      status: 'Idea',
      record: true,
    });
    expect(a[1]!.machine).toBe(false);
  });

  it('is no extensions on an older server, an error or no network', async () => {
    for (const f of [
      async () => new Response('not found', { status: 404 }),
      async () => new Response('{bad json'),
      async () => {
        throw new Error('offline');
      },
    ]) {
      const loader = new ExtLoader({ fetch: f as unknown as typeof fetch });
      expect(await loader.list()).toEqual([]);
    }
    expect(normalizeExts(null)).toEqual([]);
    expect(normalizeExts({ extensions: 'x' })).toEqual([]);
  });

  it('names the slots and their exports', () => {
    expect(SLOTS).toEqual(['page', 'version', 'sidebar-tab', 'review-panel']);
    expect(Object.values(EXPORTS)).toEqual(['page', 'version', 'sidebarTab', 'reviewPanel']);
  });
});

describe('a slot', () => {
  it('imports the module only when a slot shows, from an absolute URL with the version, and once', async () => {
    const mounts: string[] = [];
    const mod: Module = {
      sidebarTab: (e, ctx) => {
        mounts.push(ctx.slot);
        (e as unknown as FakeEl).append('hello');
      },
      page: () => void mounts.push('page'),
    };
    const { loader, imported } = setup({ hello: async () => mod });
    const [hello] = await loader.list();
    expect(imported).toEqual([]); // listing never imports
    const a = el(),
      b = el();
    await mountSlot(a, loader, hello!, 'sidebar-tab', host()).ready;
    await mountSlot(b, loader, hello!, 'page', host({ where: 'home', homeRoot: '' })).ready;
    expect(imported).toEqual(['http://h/p/clip/ext/hello/ui/hello.js?v=0.1.0']);
    expect(mounts).toEqual(['sidebar-tab', 'page']);
    expect(a.children).toEqual(['hello']);
  });

  it('a module that fails to load: one quiet line in the slot, logged once', async () => {
    const { loader, logs } = setup();
    const [, ignition] = await loader.list();
    const a = el(),
      b = el();
    await mountSlot(a, loader, ignition!, 'version', host()).ready;
    await mountSlot(b, loader, ignition!, 'version', host()).ready;
    expect(a.line).toMatch(/^Ignition didn't load: Failed to fetch dynamically imported module: /);
    expect(b.line).toBe(a.line);
    expect(logs).toHaveLength(1);
    expect(String(logs[0]![0])).toContain('Ignition');
  });

  it('a mount that throws, or no export for the slot: its line, and nothing it left behind', async () => {
    const { loader } = setup({
      hello: async () => ({
        page: (e: HTMLElement) => {
          (e as unknown as FakeEl).append('half-built');
          throw new Error('boom');
        },
      }),
    });
    const [hello] = await loader.list();
    const a = el();
    await mountSlot(a, loader, hello!, 'page', host()).ready;
    expect(a.line).toBe("Hello didn't load: boom");
    expect(a.children).toHaveLength(1);
    const b = el();
    await mountSlot(b, loader, hello!, 'sidebar-tab', host()).ready;
    expect(b.line).toBe("Hello didn't load: it has no sidebarTab export");
    expect(failedLine({ name: 'X' }, 'plain')).toBe("X didn't load: plain");
  });

  it('unmounts on a change: its cleanup, its ctx subscriptions, and an empty element', async () => {
    const cleanup = vi.fn();
    const recorded = new Set<unknown>();
    let seen: Ctx | null = null;
    const { loader } = setup({
      ignition: async () => ({
        version: (e: HTMLElement, ctx: Ctx) => {
          seen = ctx;
          (e as unknown as FakeEl).append('editor');
          ctx.onRecorded(() => {}); // never unsubscribed by the extension
          return cleanup;
        },
      }),
    });
    const [, ignition] = await loader.list();
    const h = host({
      onRecorded: (cb) => {
        recorded.add(cb);
        return () => void recorded.delete(cb);
      },
    });
    const a = el();
    const m = mountSlot(a, loader, ignition!, 'version', h);
    await m.ready;
    expect(a.children).toEqual(['editor']);
    expect(recorded.size).toBe(1);
    expect(seen!.ext).toEqual({
      id: 'ignition',
      name: 'Ignition',
      version: '1.0.0',
      base: 'ext/ignition/',
      machine: false,
    });
    m.unmount();
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(recorded.size).toBe(0);
    expect(a.children).toEqual([]);
    m.unmount(); // twice is once
    expect(cleanup).toHaveBeenCalledTimes(1);
  });

  it('unmounted before its module arrived: never mounts', async () => {
    let release: (m: Module) => void = () => {};
    const mount = vi.fn();
    const { loader } = setup({ hello: () => new Promise((res) => (release = res)) });
    const [hello] = await loader.list();
    const a = el();
    const m = mountSlot(a, loader, hello!, 'page', host());
    m.unmount();
    release({ page: mount });
    await m.ready;
    expect(mount).not.toHaveBeenCalled();
    expect(a.children).toEqual([]);
  });

  it('a cleanup that throws is logged; the slot still empties', async () => {
    const { loader, logs } = setup({
      hello: async () => ({
        page: (e: HTMLElement) => {
          (e as unknown as FakeEl).append('x');
          return () => {
            throw new Error('bad cleanup');
          };
        },
      }),
    });
    const [hello] = await loader.list();
    const a = el();
    const m = mountSlot(a, loader, hello!, 'page', host());
    await m.ready;
    m.unmount();
    expect(a.children).toEqual([]);
    expect(logs).toHaveLength(1);
  });

  it('resolves the module URL against the page, at the home and under a video', () => {
    const ext = { id: 'hello', base: 'ext/hello/', module: 'ui/hello.js', version: '1.0.0+b' } as ExtInfo;
    expect(new ExtLoader({ base: () => 'http://h/' }).moduleUrl(ext)).toBe(
      'http://h/ext/hello/ui/hello.js?v=1.0.0%2Bb',
    );
    expect(new ExtLoader({ base: () => 'http://h/p/a%20b/' }).moduleUrl(ext)).toBe(
      'http://h/p/a%20b/ext/hello/ui/hello.js?v=1.0.0%2Bb',
    );
  });
});
