// Small process helpers: run a command and collect its output, at low priority when asked.
import { spawn } from 'node:child_process';

export interface RunOptions {
  cwd?: string;
  /** Run under `nice -n 19` (heavy jobs such as encodes). */
  nice?: boolean;
  /** Bytes written to the child's stdin. */
  input?: Buffer;
  /** Extra environment variables. */
  env?: Record<string, string>;
}

/** Runs a command and resolves with its stdout. Rejects with stderr on a non-zero exit. */
export function run(cmd: string, args: string[], opts: RunOptions = {}): Promise<Buffer> {
  const [bin, argv] = opts.nice ? ['nice', ['-n', '19', cmd, ...args]] : [cmd, args];
  return new Promise((resolve, reject) => {
    const env = opts.env ? { ...process.env, ...opts.env } : undefined;
    const child = spawn(bin, argv, { cwd: opts.cwd, env, stdio: ['pipe', 'pipe', 'pipe'] });
    const out: Buffer[] = [];
    let err = '';
    child.stdout.on('data', (d: Buffer) => out.push(d));
    child.stderr.on('data', (d: Buffer) => { err += d.toString(); });
    child.on('error', reject);
    child.on('close', (code) => {
      if (code === 0) resolve(Buffer.concat(out));
      else reject(new Error(`${cmd} ${args.join(' ')} exited ${code}\n${err.slice(-2000)}`));
    });
    child.stdin.end(opts.input);
  });
}

/** Runs async jobs with at most `limit` of them in flight. */
export async function pool<T>(limit: number, jobs: (() => Promise<T>)[]): Promise<T[]> {
  const results: T[] = new Array(jobs.length);
  let next = 0;
  const worker = async (): Promise<void> => {
    while (next < jobs.length) {
      const i = next++;
      results[i] = await jobs[i]!();
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, jobs.length) }, worker));
  return results;
}
