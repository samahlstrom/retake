// Starts the server under test (new or legacy) on a fixed port, waits for /api/ping, and always stops it.
import { spawn, type ChildProcess } from 'node:child_process';
import { closeSync, existsSync, mkdirSync, openSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { CONFIG_HOME, LEGACY, LEGACY_REVIEW, RETAKE_BIN, WORK } from './env.ts';

const LOGS = join(WORK, 'logs');
const PIDS = join(WORK, 'pids');
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** The command line for one server: the same flags in both modes, plus `extra` ones (new server only). */
export function serverCommand(video: string, port: number, extra: string[] = []): [string, string[]] {
  const flags = ['--no-open', '--port', String(port), '--idle-exit', '0'];
  return LEGACY ? [LEGACY_REVIEW, [video, ...flags]] : [RETAKE_BIN, ['open', video, ...flags, ...extra]];
}

export class Server {
  readonly name: string;
  readonly port: number;
  readonly url: string;
  readonly log: string;
  private proc: ChildProcess;
  private exited: Promise<void>;

  constructor(name: string, port: number, proc: ChildProcess, log: string) {
    this.name = name;
    this.port = port;
    this.url = `http://127.0.0.1:${port}/`;
    this.proc = proc;
    this.log = log;
    this.exited = new Promise((r) => proc.once('exit', () => r()));
  }

  get running(): boolean { return this.proc.exitCode === null && this.proc.signalCode === null; }

  logTail(): string {
    try { return readFileSync(this.log, 'utf8').slice(-3000); } catch { return ''; }
  }

  /** SIGTERM to the whole process group, SIGKILL after 3 s. */
  async stop(): Promise<void> {
    const pid = this.proc.pid;
    if (pid && this.running) {
      try { process.kill(-pid, 'SIGTERM'); } catch { /* already gone */ }
      await Promise.race([this.exited, sleep(3000)]);
      if (this.running) {
        try { process.kill(-pid, 'SIGKILL'); } catch { /* already gone */ }
        await Promise.race([this.exited, sleep(2000)]);
      }
    }
    rmSync(join(PIDS, `${this.name}.pid`), { force: true });
  }
}

async function ping(port: number): Promise<boolean> {
  try {
    const r = await fetch(`http://127.0.0.1:${port}/api/ping`, { signal: AbortSignal.timeout(1000) });
    return r.ok && (await r.json() as { ok?: boolean }).ok === true;
  } catch {
    return false;
  }
}

/**
 * Starts a server for `video` and waits until it answers /api/ping. `extra`: more flags (new server only). `opts`:
 * another binary (an update), and environment variables over the usual ones (another config folder).
 */
export async function startServer(name: string, video: string, port: number, extra: string[] = [],
  opts: { bin?: string; env?: Record<string, string> } = {}): Promise<Server> {
  const [cmd, args] = serverCommand(video, port, extra);
  return startProcess(name, opts.bin ?? cmd, args, port, opts.env);
}

/** Starts `retake serve` (spec/server.md) with these arguments and waits until it answers /api/ping. */
export async function startTeamServer(name: string, port: number, args: string[]): Promise<Server> {
  return startProcess(name, RETAKE_BIN, ['serve', '--listen', `127.0.0.1:${port}`, ...args], port);
}

async function startProcess(name: string, cmd: string, args: string[], port: number,
  env: Record<string, string> = {}): Promise<Server> {
  if (!existsSync(cmd)) {
    throw new Error(LEGACY ? `legacy launcher not found: ${cmd} (set RETAKE_LEGACY_REVIEW)`
      : `${cmd} not found: run \`make build\` first (or set RETAKE_BIN)`);
  }
  if (await ping(port)) throw new Error(`port ${port} is already serving something; stop it first`);
  mkdirSync(LOGS, { recursive: true });
  mkdirSync(PIDS, { recursive: true });
  const log = join(LOGS, `server-${name}.log`);
  const fd = openSync(log, 'w');
  const proc = spawn(cmd, args, {
    detached: true, // own process group, so stop() reaches the launcher's children too
    stdio: ['ignore', fd, fd],
    env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME, RETAKE_THEME: '', ...env }, // no theme unless a test sets one
  });
  closeSync(fd);
  const server = new Server(name, port, proc, log);
  if (proc.pid) writeFileSync(join(PIDS, `${name}.pid`), String(proc.pid));
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (!server.running) throw new Error(`${name} server exited early:\n${server.logTail()}`);
    if (await ping(port)) return server;
    await sleep(100);
  }
  await server.stop();
  throw new Error(`${name} server did not answer /api/ping within 20 s:\n${server.logTail()}`);
}

/** Starts a process that serves nothing itself (`retake connect`), logged and stopped like a server. */
export function startDaemon(name: string, cmd: string, args: string[]): Server {
  mkdirSync(LOGS, { recursive: true });
  mkdirSync(PIDS, { recursive: true });
  const log = join(LOGS, `${name}.log`);
  const fd = openSync(log, 'a');
  const proc = spawn(cmd, args, {
    detached: true,
    stdio: ['ignore', fd, fd],
    env: { ...process.env, XDG_CONFIG_HOME: CONFIG_HOME },
  });
  closeSync(fd);
  if (proc.pid) writeFileSync(join(PIDS, `${name}.pid`), String(proc.pid));
  return new Server(name, 0, proc, log);
}

/** Kills servers a crashed run left behind (from their pid files). */
export function killLeftovers(): void {
  if (!existsSync(PIDS)) return;
  for (const f of readdirSync(PIDS)) {
    const pid = Number.parseInt(readFileSync(join(PIDS, f), 'utf8'), 10);
    if (pid > 1) {
      try { process.kill(-pid, 'SIGKILL'); } catch { /* gone */ }
    }
    rmSync(join(PIDS, f), { force: true });
  }
}
