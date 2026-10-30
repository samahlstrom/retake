// Dev only: a stand-in for the milestone-2 routes (spec/notes.md) until the server has them, so `npm run dev` shows
// the sidebar against a milestone-1 server. Everything else still goes to the real server through the dev proxy.
//
//   RETAKE_MOCK_NOTES=1 npm run dev        (RETAKE_MOCK_NOTES=fail: transcription fails, with a hint)
//
// It serves GET /api/notes (made-up questions on the newest version, resolved notes on older ones), the SSE stream
// GET /api/events, POST /api/notes/<id>/reopen, POST /api/session/<take>/done (fake transcribe and notes jobs that
// end in a new note), POST /api/session/<take>/image and GET /api/takes/<take>/<file> (images kept in memory).
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { Plugin } from 'vite';

interface Item {
  id: number;
  kind: 'note' | 'question';
  status: string;
  t0: number;
  t1: number;
  text: string;
  by: string;
  change?: string;
  answered_in?: string;
  question?: string;
  options?: string[];
}

interface Family {
  labels: string[];
  duration: number;
}

const IMAGE_NAME = /^img_\d{2,3}\.(png|jpg|webp|gif)$/;
const IMAGE_MAX = 25 * 1024 * 1024;

function body(req: IncomingMessage, max: number): Promise<Buffer | null> {
  return new Promise((res, rej) => {
    const parts: Buffer[] = [];
    let size = 0;
    req.on('data', (d: Buffer) => {
      size += d.length;
      if (size <= max) parts.push(d);
    });
    req.on('end', () => res(size > max ? null : Buffer.concat(parts)));
    req.on('error', rej);
  });
}

function send(res: ServerResponse, status: number, data: unknown): void {
  res.statusCode = status;
  res.setHeader('Content-Type', 'application/json');
  res.setHeader('Cache-Control', 'no-store');
  res.end(JSON.stringify(data));
}

/** Made-up sidebar items for a family: questions on the newest version, resolved notes on the older ones. */
function seed(fam: Family): Map<string, Item[]> {
  const out = new Map<string, Item[]>(),
    d = fam.duration || 40,
    at = (k: number) => Math.round(d * k * 10) / 10,
    newest = fam.labels[fam.labels.length - 1],
    prev = fam.labels[fam.labels.length - 2];
  let id = 1;
  for (const label of fam.labels.slice(0, -1)) {
    const next = fam.labels[fam.labels.indexOf(label) + 1];
    out.set(label, [
      {
        id: id++,
        kind: 'note',
        status: 'answered',
        t0: at(0.1),
        t1: at(0.15),
        text: 'The title comes in too late',
        by: 'you',
        change: 'Title now lands on the first beat',
        answered_in: next,
      },
      {
        id: id++,
        kind: 'note',
        status: 'question',
        t0: at(0.35),
        t1: at(0.4),
        text: 'That zoom feels off',
        by: 'you',
        question: 'Keep the zoom, or cut to the wide shot?',
        options: [label, next],
      },
      { id: id++, kind: 'note', status: 'retracted', t0: at(0.5), t1: at(0.52), text: 'Scrap that', by: 'you' },
      {
        id: id++,
        kind: 'note',
        status: 'accepted',
        t0: at(0.7),
        t1: at(0.75),
        text: 'Cut the long pause',
        by: 'you',
        change: 'Pause trimmed to half a second',
        answered_in: next,
      },
    ]);
  }
  out.set(newest, [
    {
      id: 100 + id++,
      kind: 'question',
      status: 'question',
      t0: at(0.35),
      t1: at(0.35),
      by: 'agent',
      text: 'Keep the zoom, or cut to the wide shot?',
      options: prev ? [prev, newest] : [],
    },
    {
      id: 100 + id,
      kind: 'question',
      status: 'question',
      t0: at(0.6),
      t1: at(0.6),
      by: 'agent',
      text: 'Is the music still too loud under the voice here?',
    },
  ]);
  return out;
}

export function mockNotes(opts: { server: string; transcribe: 'ok' | 'fail' }): Plugin {
  let items: Map<string, Item[]> | null = null;
  let fam: Family = { labels: [], duration: 40 };
  const clients = new Set<ServerResponse>();
  const images = new Map<string, { type: string; data: Buffer }>();
  let seq = 0,
    jobId = 0,
    noteId = 1000;

  const emit = (event: string, data: unknown) => {
    const msg = `id: ${++seq}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
    for (const c of clients) c.write(msg);
  };
  const later = (ms: number, fn: () => void) => setTimeout(fn, ms);

  async function load(): Promise<Map<string, Item[]>> {
    if (items) return items;
    const info = (await (await fetch(`${opts.server}/api/info`)).json()) as {
      versions?: { label: string; duration: number | null }[];
      label?: string;
      duration?: number | null;
    };
    const vs = info.versions?.length
      ? info.versions
      : [{ label: info.label ?? 'current', duration: info.duration ?? 40 }];
    fam = { labels: vs.map((v) => v.label), duration: vs[vs.length - 1].duration ?? 40 };
    return (items = seed(fam));
  }

  /** Done: transcribe, then notes, then a new note on that version (or a failure with its hint). */
  function runJobs(version: string): void {
    const t = ++jobId,
      n = ++jobId;
    emit('job', { id: t, kind: 'transcribe', state: 'queued' });
    later(600, () => emit('job', { id: t, kind: 'transcribe', state: 'running' }));
    if (opts.transcribe === 'fail') {
      later(2500, () =>
        emit('job', {
          id: t,
          kind: 'transcribe',
          state: 'failed',
          error: "Transcription isn't set up",
          hint: 'Install whisper.cpp, or set [transcribe] command in config.toml.',
        }),
      );
      return;
    }
    later(2500, () => {
      emit('job', { id: t, kind: 'transcribe', state: 'done' });
      emit('job', { id: n, kind: 'notes', state: 'running' });
    });
    later(3500, () => {
      const list = items?.get(version) ?? [];
      const t0 = Math.round(fam.duration * 0.25 * 10) / 10;
      list.push({
        id: ++noteId,
        kind: 'note',
        status: 'open',
        t0,
        t1: t0 + 2,
        by: 'you',
        text: 'A note from your take',
      });
      items?.set(version, list);
      emit('job', { id: n, kind: 'notes', state: 'done' });
      emit('notes', { version });
    });
  }

  async function handle(req: IncomingMessage, res: ServerResponse): Promise<boolean> {
    const u = new URL(req.url ?? '/', 'http://dev'),
      p = u.pathname;
    if (req.method === 'GET' && p === '/api/notes') {
      const all = await load(),
        v = u.searchParams.get('v') ?? '';
      if (!all.has(v)) return (send(res, 400, { error: 'bad version' }), true);
      send(res, 200, { version: v, newest: v === fam.labels[fam.labels.length - 1], items: all.get(v) });
      return true;
    }
    if (req.method === 'GET' && p === '/api/events') {
      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-store',
        Connection: 'keep-alive',
      });
      res.write('retry: 2000\n\n');
      clients.add(res);
      req.on('close', () => clients.delete(res));
      return true;
    }
    let m = /^\/api\/notes\/(\d+)\/reopen$/.exec(p);
    if (req.method === 'POST' && m) {
      const id = Number(m[1]);
      for (const list of (await load()).values()) {
        const it = list.find((x) => x.id === id);
        if (it) Object.assign(it, { status: 'open', change: undefined, answered_in: undefined });
      }
      emit('notes', {});
      send(res, 200, { ok: true });
      return true;
    }
    m = /^\/api\/session\/([^/]+)\/(done|image)$/.exec(p);
    if (req.method === 'POST' && m) {
      await load();
      const v = u.searchParams.get('v') || fam.labels[fam.labels.length - 1];
      if (m[2] === 'done') {
        await body(req, 0);
        send(res, 200, { ok: true });
        runJobs(v);
        return true;
      }
      const name = u.searchParams.get('name') ?? '';
      if (!IMAGE_NAME.test(name)) return (send(res, 400, { error: 'bad name' }), true);
      const data = await body(req, IMAGE_MAX);
      if (!data) return (send(res, 413, { error: 'body too large' }), true);
      images.set(`${m[1]}/${name}`, { type: String(req.headers['content-type'] ?? 'image/png'), data });
      send(res, 200, { ok: true, size: data.length });
      return true;
    }
    m = /^\/api\/takes\/([^/]+)\/([^/]+)$/.exec(p);
    if (req.method === 'GET' && m) {
      const img = images.get(`${m[1]}/${m[2]}`);
      if (!img) return (send(res, 404, { error: 'not found' }), true);
      res.setHeader('Content-Type', img.type);
      res.end(img.data);
      return true;
    }
    return false;
  }

  return {
    name: 'retake:mock-notes',
    apply: 'serve',
    configureServer(dev) {
      dev.config.logger.info(`retake: mocking the notes routes (transcription: ${opts.transcribe})`);
      dev.middlewares.use((req, res, next) => {
        handle(req, res).then(
          (done) => (done ? undefined : next()),
          (err: unknown) => next(err),
        );
      });
    },
  };
}
