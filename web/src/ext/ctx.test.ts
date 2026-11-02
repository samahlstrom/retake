import { describe, expect, it, vi } from 'vitest';
import api from './api.json';
import { ExtError, JOB_POLL_MS, makeCtx, WATCH_RENEW_MS, wordsPath, wordsText, type Host } from './ctx';
import type { ExtInfo } from './loader';
import type { Player } from './retake-ext';

const scripts: ExtInfo = {
  id: 'scripts',
  name: 'Scripts',
  version: '1.0.0',
  base: 'ext/scripts/',
  module: 'ui/scripts.js',
  style: '',
  nav: null,
  kinds: [
    {
      id: 'scripts.doc',
      kind: 'doc',
      folder: 'v0',
      marker: ['*_v0.md'],
      label: 'Script',
      status: 'Idea',
      record: true,
    },
  ],
  slots: [],
  machine: true,
};

interface Call {
  method: string;
  url: string;
  headers: Record<string, string>;
  body: unknown;
}

/** A fake server: answers by "METHOD url" (or a function of the call), and records every call. */
function server(routes: Record<string, (c: Call) => Response | Promise<Response>>) {
  const calls: Call[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const headers = Object.fromEntries(new Headers(init.headers).entries());
    let body: unknown = init.body;
    if (typeof body === 'string' && headers['content-type'] === 'application/json') body = JSON.parse(body);
    const c: Call = { method: init.method ?? 'GET', url: String(input), headers, body };
    calls.push(c);
    const r = routes[`${c.method} ${c.url}`];
    return r ? r(c) : new Response(JSON.stringify({ error: 'not found' }), { status: 404 });
  });
  return { fetch: fetch as unknown as typeof globalThis.fetch, calls };
}
const json = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } });

/** Timers the test runs by hand. */
function clock() {
  let next = 1;
  const intervals = new Map<number, { fn: () => void; ms: number }>();
  return {
    intervals,
    setInterval: (fn: () => void, ms: number) => {
      intervals.set(next, { fn, ms });
      return next++;
    },
    clearInterval: (id: unknown) => void intervals.delete(id as number),
    tick: () => {
      for (const t of [...intervals.values()]) t.fn();
    },
  };
}

/** The review page's events, driven by the test. */
function events() {
  const subs = new Map<string, Set<(d: unknown) => void>>();
  return {
    on: (name: string, cb: (d: unknown) => void) => {
      if (!subs.has(name)) subs.set(name, new Set());
      subs.get(name)!.add(cb);
      return () => void subs.get(name)?.delete(cb);
    },
    emit: (name: string, d: unknown) => {
      for (const cb of subs.get(name) ?? []) cb(d);
    },
    count: (name: string) => subs.get(name)?.size ?? 0,
  };
}

function videoHost(f: typeof fetch, over: Partial<Host> = {}): Host {
  return {
    where: 'video',
    homeRoot: '/',
    video: { name: 'jump-souls', title: 'jump-souls', path: 'deadlock/jump-souls', project: 'deadlock' },
    version: { label: 'v0', kind: 'scripts.doc' },
    role: 'reviewer',
    viewer: 'view1',
    toast: () => {},
    fetch: f,
    sleep: async () => {},
    ...over,
  };
}

describe('ctx', () => {
  it('has the members api.json lists, and its files, comments and player have theirs', () => {
    const player: Player = { time: () => 1, duration: () => 2, seek: () => {}, onTime: () => () => {} };
    const { ctx } = makeCtx(scripts, 'sidebar-tab', videoHost(server({}).fetch, { player: () => player }));
    expect(Object.keys(ctx).sort()).toEqual([...api.ctx].sort());
    expect(Object.keys(ctx.files()).sort()).toEqual([...api.files].sort());
    expect(Object.keys(ctx.comments()).sort()).toEqual([...api.comments].sort());
    expect(Object.keys(ctx.player!).sort()).toEqual([...api.player].sort());
    expect(api.api).toBe(1);
    expect(api.deprecated).toEqual({});
    expect(Object.keys(api.slots)).toEqual(['page', 'version', 'sidebar-tab', 'review-panel']);
  });

  it('gives a slot what is its own: the page its route, a sidebar tab the player, no video at the home', () => {
    const player: Player = { time: () => 1, duration: () => 2, seek: () => {}, onTime: () => () => {} };
    const f = server({}).fetch;
    const version = makeCtx(scripts, 'version', videoHost(f, { player: () => player, route: () => 'x' })).ctx;
    expect(version.player).toBeUndefined();
    expect(version.route).toBe('');
    expect(version.video).toEqual({
      name: 'jump-souls',
      title: 'jump-souls',
      path: 'deadlock/jump-souls',
      project: 'deadlock',
    });
    expect(version.version).toEqual({ label: 'v0', kind: 'scripts.doc' });
    expect(version.role).toBe('reviewer');
    let route = 'new';
    const page = makeCtx(scripts, 'page', {
      where: 'home',
      homeRoot: '',
      role: 'owner',
      viewer: 'v',
      toast: () => {},
      fetch: f,
      route: () => route,
    }).ctx;
    expect(page.route).toBe('new');
    route = '';
    expect(page.route).toBe('');
    expect(page.video).toBeUndefined();
    expect(page.version).toBeUndefined();
    expect(() => page.files()).toThrow(ExtError); // no video at the home: name one
  });
});

describe('ctx.files()', () => {
  it('lists, reads, writes and removes owned files under this video, with If-Match', async () => {
    const info = { path: 'v0/idea.md', size: 9, modified: 1, sha256: 'bbb' };
    const srv = server({
      'GET api/ext/scripts/files': () => json({ files: [info] }),
      'GET api/ext/scripts/files?glob=v0%2F*.md': () => json({ files: [] }),
      'GET api/ext/scripts/files/v0/idea.md': () =>
        new Response('# Jump souls\n', { headers: { ETag: '"aaa"', 'Content-Type': 'text/markdown' } }),
      'PUT api/ext/scripts/files/v0/idea.md': (c) =>
        c.headers['if-match'] === '"aaa"' ? json(info) : json({ error: 'changed', sha256: 'zzz' }, 409),
      'PUT api/ext/scripts/files/v0/voice/a%20b.webm': () => json({ ...info, path: 'v0/voice/a b.webm' }),
      'DELETE api/ext/scripts/files/v0/outline.md': () => new Response(null, { status: 204 }),
    });
    const files = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx.files();
    expect(await files.list()).toEqual([info]);
    expect(await files.list('v0/*.md')).toEqual([]);
    const read = await files.read('v0/idea.md');
    expect(read.sha256).toBe('aaa');
    expect(await read.text()).toBe('# Jump souls\n');
    expect(await read.text()).toBe('# Jump souls\n'); // read twice
    expect((await read.blob()).size).toBe(13);
    expect(await files.write('v0/idea.md', '# Jump souls!\n', { ifMatch: 'aaa' })).toEqual(info);
    const put = srv.calls.at(-1)!;
    expect(put.headers['content-type']).toBe('text/plain; charset=utf-8');
    expect(put.body).toBe('# Jump souls!\n');
    const lost = await files.write('v0/idea.md', 'x', { ifMatch: 'old' }).catch((e: unknown) => e);
    expect(lost).toBeInstanceOf(ExtError);
    expect(lost).toMatchObject({ status: 409, changed: true, sha256: 'zzz' });
    const audio = new Blob(['ogg'], { type: 'audio/webm' });
    await files.write('v0/voice/a b.webm', audio, { ifMatch: '*' });
    expect(srv.calls.at(-1)!.headers).toMatchObject({ 'if-match': '*', 'content-type': 'audio/webm' });
    await files.remove('v0/outline.md');
    expect(srv.calls.at(-1)!.method).toBe('DELETE');
    const missing = await files.read('v0/nope.md').catch((e: unknown) => e);
    expect(missing).toMatchObject({ status: 404, message: 'not found' });
    expect(files.url('v0/refs/a #1.png')).toBe('api/ext/scripts/files/v0/refs/a%20%231.png');
  });

  it("reaches another video's files through the home's root", async () => {
    const srv = server({ 'GET /p/other%20one/api/ext/scripts/files': () => json({ files: [] }) });
    const ctx = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx;
    expect(await ctx.files('other one').list()).toEqual([]);
    expect(ctx.files('jump-souls').url('v0/idea.md')).toBe('api/ext/scripts/files/v0/idea.md'); // this one: relative
    const home = makeCtx(scripts, 'page', {
      where: 'home',
      homeRoot: '',
      role: 'owner',
      viewer: 'v',
      toast: () => {},
    });
    expect(home.ctx.files('a').url('v0/idea.md')).toBe('p/a/api/ext/scripts/files/v0/idea.md');
  });

  it('history and undo: the agent edits, newest first; undo answers the file, or that it changed', async () => {
    const edit = { id: 'e1', path: 'v0/script.md', at: 5, before: 'a', after: 'b' };
    const srv = server({
      'GET api/ext/scripts/history': () => json({ edits: [edit] }),
      'GET api/ext/scripts/history?path=v0%2Fscript.md': () => json({ edits: [edit] }),
      'POST api/ext/scripts/history/undo': (c) =>
        (c.body as { id: string }).id === 'e1'
          ? json({ path: 'v0/script.md', size: 1, modified: 2, sha256: 'a' })
          : json({ error: 'changed', sha256: 'c' }, 409),
    });
    const files = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx.files();
    expect(await files.history()).toEqual([edit]);
    expect(await files.history('v0/script.md')).toEqual([edit]);
    expect(await files.undo('e1', 'v0/script.md')).toMatchObject({ sha256: 'a' });
    expect(srv.calls.at(-1)!.body).toEqual({ id: 'e1', path: 'v0/script.md' });
    expect(await files.undo('e2', 'v0/script.md').catch((e: unknown) => e)).toMatchObject({
      changed: true,
      sha256: 'c',
    });
  });

  it('watch: a lease at once and every 10 s while watched, its own ext.files only, none after the last unwatch', () => {
    const srv = server({ 'POST api/ext/scripts/watch?viewer=view1': () => json({ ok: true }) });
    const t = clock(),
      ev = events();
    const made = makeCtx(scripts, 'version', videoHost(srv.fetch, { ...t, events: ev.on }));
    const files = made.ctx.files();
    const a = vi.fn(),
      b = vi.fn();
    const offA = files.watch(a);
    const offB = files.watch(b);
    expect(srv.calls.map((c) => c.url)).toEqual(['api/ext/scripts/watch?viewer=view1']); // one lease for both
    expect([...t.intervals.values()].map((x) => x.ms)).toEqual([WATCH_RENEW_MS]);
    t.tick();
    t.tick();
    expect(srv.calls).toHaveLength(3);
    const changed = [{ path: 'v0/script.md', size: 3, modified: 9, sha256: 'x' }];
    ev.emit('ext.files', { ext: 'hello', files: [{ path: 'x' }] });
    ev.emit('ext.files', { ext: 'scripts', files: changed });
    expect(a).toHaveBeenCalledTimes(1);
    expect(a).toHaveBeenCalledWith(changed);
    expect(b).toHaveBeenCalledWith(changed);
    offA();
    t.tick();
    expect(srv.calls).toHaveLength(4); // b still watches
    offB();
    expect(t.intervals.size).toBe(0);
    expect(ev.count('ext.files')).toBe(0);
    // A slot that unmounts ends its watches, whether or not the extension did.
    files.watch(a);
    expect(t.intervals.size).toBe(1);
    made.dispose();
    expect(t.intervals.size).toBe(0);
    expect(ev.count('ext.files')).toBe(0);
  });

  it("watch at the home: the lease and the events are that video's", () => {
    const opened: string[] = [];
    const listeners = new Map<string, (e: MessageEvent) => void>();
    let closed = false;
    const srv = server({ 'POST p/idea/api/ext/scripts/watch?viewer=home1': () => json({ ok: true }) });
    const t = clock();
    const made = makeCtx(scripts, 'page', {
      where: 'home',
      homeRoot: '',
      role: 'owner',
      viewer: 'home1',
      toast: () => {},
      fetch: srv.fetch,
      ...t,
      eventSource: (url) => {
        opened.push(url);
        return {
          addEventListener: (type, cb) => void listeners.set(type, cb),
          close: () => void (closed = true),
        };
      },
    });
    const cb = vi.fn();
    const off = made.ctx.files('idea').watch(cb);
    expect(opened).toEqual(['p/idea/api/events']);
    expect(srv.calls[0]!.url).toBe('p/idea/api/ext/scripts/watch?viewer=home1');
    listeners.get('ext.files')!({
      data: JSON.stringify({ ext: 'scripts', files: [{ path: 'v0/idea.md' }] }),
    } as MessageEvent);
    expect(cb).toHaveBeenCalledWith([{ path: 'v0/idea.md' }]);
    off();
    expect(closed).toBe(true);
  });
});

describe('ctx.transcribe()', () => {
  it('starts the job, asks every second until it is done, then reads the words', async () => {
    let polls = 0;
    const slept: number[] = [];
    const srv = server({
      'POST api/ext/scripts/transcribe': () => json({ job: 'j7' }, 202),
      'GET api/ext/scripts/jobs/j7': () =>
        json({ id: 'j7', kind: 'transcribe-file', state: ++polls < 3 ? 'running' : 'done' }),
      'GET api/ext/scripts/files/v0/voice/20261101-190211.words.json': () =>
        json(
          {
            model: 'small',
            words: [
              { w: ' So', s: 0, e: 0.2, p: 0.9 },
              { w: ' the', s: 0.2, e: 0.3 },
              { w: ' idea', s: 0.3, e: 0.6 },
              { w: ',', s: 0.6, e: 0.6 },
              { w: 7, s: 1, e: 2 },
            ],
          },
          200,
          { ETag: '"w"' },
        ),
    });
    const ctx = makeCtx(
      scripts,
      'version',
      videoHost(srv.fetch, {
        sleep: async (ms) => {
          slept.push(ms);
        },
      }),
    ).ctx;
    const r = await ctx.transcribe('v0/voice/20261101-190211.webm');
    expect(srv.calls[0]).toMatchObject({ method: 'POST', body: { path: 'v0/voice/20261101-190211.webm' } });
    expect(polls).toBe(3);
    expect(slept).toEqual([JOB_POLL_MS, JOB_POLL_MS, JOB_POLL_MS]);
    expect(r.text).toBe('So the idea,');
    expect(r.words).toHaveLength(4); // the malformed one is left out
  });

  it("reads the job's words_path when it names one", async () => {
    const srv = server({
      'POST api/ext/scripts/transcribe': () => json({ job: 'j1' }, 202),
      'GET api/ext/scripts/jobs/j1': () => json({ state: 'done', words_path: 'v0/voice/x.words.json' }),
      'GET api/ext/scripts/files/v0/voice/x.words.json': () => json({ words: [{ w: 'hi', s: 0, e: 1 }] }),
    });
    const r = await makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx.transcribe('v0/voice/x.ogg');
    expect(r).toEqual({ text: 'hi', words: [{ w: 'hi', s: 0, e: 1 }] });
  });

  it("rejects with the job's hint when it fails", async () => {
    const srv = server({
      'POST api/ext/scripts/transcribe': () => json({ job: 'j2' }, 202),
      'GET api/ext/scripts/jobs/j2': () =>
        json({ state: 'failed', error: 'no transcriber', hint: 'Set up [transcribe] in config.toml' }),
    });
    const err = await makeCtx(scripts, 'version', videoHost(srv.fetch))
      .ctx.transcribe('v0/voice/x.webm')
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ExtError);
    expect(err).toMatchObject({
      message: 'Set up [transcribe] in config.toml',
      hint: 'Set up [transcribe] in config.toml',
    });
    const offline = server({
      'POST api/ext/scripts/transcribe': () => json({ error: 'editing machine is offline' }, 503),
    });
    expect(
      await makeCtx(scripts, 'version', videoHost(offline.fetch))
        .ctx.transcribe('v0/voice/x.webm')
        .catch((e: unknown) => e),
    ).toMatchObject({ status: 503, message: 'editing machine is offline' });
  });

  it('names where the words land and reads them as text', () => {
    expect(wordsPath('v0/voice/20261101-190211.webm')).toBe('v0/voice/20261101-190211.words.json');
    expect(wordsPath('v0/voice/no-ext')).toBe('v0/voice/no-ext.words.json');
    expect(
      wordsText([
        { w: 'Hello', s: 0, e: 1 },
        { w: 'world', s: 1, e: 2 },
        { w: '!', s: 2, e: 2 },
      ]),
    ).toBe('Hello world!');
  });
});

describe('ctx.comments()', () => {
  const items = [
    { id: 1, kind: 'note', status: 'open', version: 'v0', t0: 0, text: 'Work this talk-out in', key: 'doc/1' },
    { id: 2, kind: 'note', status: 'open', version: 'v0', t0: 0, text: 'Sent', work: { id: 't1', state: 'running' } },
    {
      id: 3,
      kind: 'note',
      status: 'question',
      version: 'v0',
      t0: 0,
      text: 'Shape the outline',
      work: { id: 't2', state: 'question' },
      ask: {
        id: 40,
        task: 't2',
        text: 'Which opening?',
        anchor: { file: 'v0/script.md', line: 3, heading: 'Cold open' },
      },
    },
    { id: 4, kind: 'note', status: 'open', version: 'v1', t0: 3, text: 'On the cut' },
    { id: 5, kind: 'question', status: 'question', t0: 3, text: 'About note 4?', note: 4 },
  ];
  const routes = {
    'GET api/notes?v=v0&scope=video': () => json({ version: 'v0', newest: false, scope: 'video', items }),
    'POST api/notes': (c: Call) => json({ id: 9, kind: 'note', status: 'open', ...(c.body as object) }, 201),
    'POST api/automation/tasks': () => json({ ok: true }),
    'POST api/automation/tasks/t2/answer': () => json({ ok: true }),
  };

  it('list: api/notes items, of one version when asked', async () => {
    const srv = server(routes);
    const c = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx.comments();
    expect((await c.list()).map((i) => i.id)).toEqual([1, 2, 3, 4, 5]);
    expect((await c.list('v0')).map((i) => i.id)).toEqual([1, 2, 3]);
  });

  it("add: a typed comment on this extension's version, with its anchor and wants", async () => {
    const srv = server(routes);
    const c = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx.comments();
    const anchor = { file: 'v0/script.md', line: 12, heading: 'Cold open', quote: 'So the idea' };
    const item = await c.add({ text: 'Tighten this', anchor });
    expect(srv.calls.at(-1)!.body).toEqual({ version: 'v0', text: 'Tighten this', anchor });
    expect(item).toMatchObject({ id: 9, text: 'Tighten this' });
    await c.add({ text: 'Build the first cut (v1) from script.md.', wants: 'version' });
    expect(srv.calls.at(-1)!.body).toEqual({
      version: 'v0',
      text: 'Build the first cut (v1) from script.md.',
      wants: 'version',
    });
    // From a video version (a sidebar tab on v3), a comment still goes on the extension's own version.
    const fromCut = makeCtx(scripts, 'sidebar-tab', videoHost(srv.fetch, { version: { label: 'v3' } })).ctx;
    await fromCut.comments().add({ text: 'x' });
    expect((srv.calls.at(-1)!.body as { version: string }).version).toBe('v0');
  });

  it('send: the ids given, or the open ones of its version nobody sent yet', async () => {
    const srv = server(routes);
    const c = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx.comments();
    await c.send([1, 3]);
    expect(srv.calls.at(-1)).toMatchObject({ url: 'api/automation/tasks', body: { notes: [1, 3] } });
    await c.send();
    expect(srv.calls.at(-1)).toMatchObject({ url: 'api/automation/tasks', body: { notes: [1] } });
  });

  it("answer: a question's thread, to its task", async () => {
    const srv = server(routes);
    const c = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx.comments();
    await c.answer(40, 'Start on the jump');
    expect(srv.calls.at(-1)).toMatchObject({
      url: 'api/automation/tasks/t2/answer',
      body: { question: 40, message: 'Start on the jump' },
    });
    expect(await c.answer(99, 'x').catch((e: unknown) => e)).toMatchObject({ status: 404 });
  });
});

describe('ctx: the rest', () => {
  it('createVideo goes to the home; 409 when the folder is there', async () => {
    const srv = server({
      'POST /api/ext/scripts/videos': (c) =>
        (c.body as { name: string }).name === 'taken'
          ? json({ error: 'exists' }, 409)
          : json({ name: 'deadlock-jump-souls', url: 'p/deadlock-jump-souls/' }, 201),
    });
    const ctx = makeCtx(scripts, 'version', videoHost(srv.fetch)).ctx;
    const files = { 'v0/idea.md': '# Jump souls\n\n' };
    expect(await ctx.createVideo({ project: 'deadlock', name: 'jump-souls', files })).toEqual({
      name: 'deadlock-jump-souls',
      url: 'p/deadlock-jump-souls/',
    });
    expect(srv.calls[0]!.body).toEqual({ project: 'deadlock', name: 'jump-souls', files });
    expect(await ctx.createVideo({ project: 'deadlock', name: 'taken', files }).catch((e: unknown) => e)).toMatchObject(
      {
        status: 409,
      },
    );
    const home = server({ 'POST api/ext/scripts/videos': () => json({ name: 'a', url: 'p/a/' }, 201) });
    await makeCtx(scripts, 'page', {
      where: 'home',
      homeRoot: '',
      role: 'owner',
      viewer: 'v',
      toast: () => {},
      fetch: home.fetch,
    }).ctx.createVideo({ project: '', name: 'a', files });
    expect(home.calls[0]!.url).toBe('api/ext/scripts/videos');
  });

  it('navigate: another video by URL, this one in place, a route on the home', () => {
    const go = vi.fn(),
      show = vi.fn(),
      setHash = vi.fn();
    const ctx = makeCtx(scripts, 'version', videoHost(server({}).fetch, { go, show })).ctx;
    ctx.navigate({ video: 'other one', version: 'v3', t: 12.4 });
    expect(go).toHaveBeenLastCalledWith('/p/other%20one/?v=v3&t=12.4');
    ctx.navigate({ video: 'idea' });
    expect(go).toHaveBeenLastCalledWith('/p/idea/');
    ctx.navigate({ version: 'v3', t: 12 });
    expect(show).toHaveBeenLastCalledWith('v3', 12);
    ctx.navigate({ video: 'jump-souls', version: 'v1' }); // this video: in place
    expect(show).toHaveBeenLastCalledWith('v1', undefined);
    ctx.navigate({ route: 'new' });
    expect(go).toHaveBeenLastCalledWith('/#ext/scripts/new');
    const page = makeCtx(scripts, 'page', {
      where: 'home',
      homeRoot: '',
      role: 'owner',
      viewer: 'v',
      toast: () => {},
      go,
      setHash,
    }).ctx;
    page.navigate({ route: '/new' });
    expect(setHash).toHaveBeenLastCalledWith('#ext/scripts/new');
    page.navigate({ video: 'idea', version: 'v0' });
    expect(go).toHaveBeenLastCalledWith('p/idea/?v=v0');
  });

  it('fetch reaches its server; home() reads the home; toast is the page’s', async () => {
    const srv = server({
      'GET ext/scripts/api/beats?x=1': () => json({ ok: 1 }),
      'GET /api/home': () => json({ title: 'Videos', role: 'owner', projects: [], tree: {} }),
      'GET ext/scripts/api/a': () => json({}),
    });
    const toast = vi.fn();
    const ctx = makeCtx(scripts, 'version', videoHost(srv.fetch, { toast })).ctx;
    expect(await (await ctx.fetch('beats?x=1')).json()).toEqual({ ok: 1 });
    await ctx.fetch('/a');
    expect((await ctx.home()).title).toBe('Videos');
    ctx.toast('Saved');
    expect(toast).toHaveBeenCalledWith('Saved');
  });

  it('on: the page’s events, files only its own; recording; a callback that throws is contained', () => {
    const ev = events();
    let rec = false;
    const recSubs = new Set<(on: boolean) => void>();
    const select = vi.fn();
    const made = makeCtx(
      scripts,
      'version',
      videoHost(server({}).fetch, {
        events: ev.on,
        recording: () => rec,
        onRecording: (cb) => {
          recSubs.add(cb);
          return () => void recSubs.delete(cb);
        },
        selectComment: select,
      }),
    );
    const { ctx } = made;
    const files = vi.fn(),
      notes = vi.fn();
    ctx.on('files', files);
    const offNotes = ctx.on('notes', notes);
    ev.emit('ext.files', { ext: 'hello', files: [] });
    ev.emit('ext.files', { ext: 'scripts', files: [] });
    ev.emit('notes', { version: 'v0' });
    expect(files).toHaveBeenCalledTimes(1);
    expect(notes).toHaveBeenCalledWith({ version: 'v0' });
    offNotes();
    ev.emit('notes', {});
    expect(notes).toHaveBeenCalledTimes(1);
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    ctx.on('job', () => {
      throw new Error('extension bug');
    });
    expect(() => ev.emit('job', { id: 1 })).not.toThrow();
    expect(err).toHaveBeenCalledTimes(1);
    err.mockRestore();
    expect(ctx.recording()).toBe(false);
    rec = true;
    expect(ctx.recording()).toBe(true);
    const seen: boolean[] = [];
    ctx.onRecording((on) => seen.push(on));
    for (const cb of recSubs) cb(true);
    expect(seen).toEqual([true]);
    ctx.selectComment(40);
    expect(select).toHaveBeenCalledWith(40);
    made.dispose();
    expect(ev.count('ext.files') + ev.count('job') + recSubs.size).toBe(0);
  });

  it('onAnchor: the version slot tells core where a typed comment points; reveal brings a place back', () => {
    let given: (() => unknown) | null = null;
    const reveals = new Set<(d: { anchor: { file: string; line: number } }) => void>();
    const host = videoHost(server({}).fetch, {
      onAnchor: (fn) => {
        given = fn;
        return () => {
          if (given === fn) given = null;
        };
      },
      onReveal: (cb) => {
        reveals.add(cb);
        return () => void reveals.delete(cb);
      },
    });
    const made = makeCtx(scripts, 'version', host);
    const { ctx } = made;
    let anchor: unknown = { file: 'v0/jump-souls_v0.md', line: 12.7, heading: ' Cold open ', quote: 'q'.repeat(300) };
    const off = ctx.onAnchor(() => anchor as never);
    expect(given!()).toEqual({ file: 'v0/jump-souls_v0.md', line: 12, heading: 'Cold open', quote: 'q'.repeat(200) });
    anchor = { line: 3 }; // no file: not an anchor
    expect(given!()).toBeNull();
    anchor = null;
    expect(given!()).toBeNull();
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    ctx.onAnchor(() => {
      throw new Error('extension bug');
    });
    expect(given!()).toBeNull();
    expect(err).toHaveBeenCalledTimes(1);
    err.mockRestore();
    off();
    // Only the version slot points comments.
    const tab = makeCtx(scripts, 'sidebar-tab', host);
    const before = given;
    tab.ctx.onAnchor(() => ({ file: 'x', line: 1 }));
    expect(given).toBe(before);

    const seen: unknown[] = [];
    ctx.on('reveal', (d) => seen.push(d));
    for (const cb of reveals) cb({ anchor: { file: 'v0/jump-souls_v0.md', line: 4 } });
    expect(seen).toEqual([{ anchor: { file: 'v0/jump-souls_v0.md', line: 4 } }]);
    made.dispose();
    expect(reveals.size).toBe(0);
    expect(given).toBeNull();
  });

  it("player: the host's, its time callbacks ended with the slot", () => {
    const subs = new Set<(t: number) => void>();
    const seek = vi.fn();
    const player: Player = {
      time: () => 4,
      duration: () => 60,
      seek,
      onTime: (cb) => {
        subs.add(cb);
        return () => void subs.delete(cb);
      },
    };
    const made = makeCtx(scripts, 'review-panel', videoHost(server({}).fetch, { player: () => player }));
    const p = made.ctx.player!;
    expect([p.time(), p.duration()]).toEqual([4, 60]);
    p.seek(10);
    expect(seek).toHaveBeenCalledWith(10);
    const times: number[] = [];
    p.onTime((t) => times.push(t));
    for (const cb of subs) cb(5);
    expect(times).toEqual([5]);
    made.dispose();
    expect(subs.size).toBe(0);
  });
});
