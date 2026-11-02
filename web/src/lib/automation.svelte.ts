export interface AgentProfile {
  id: string;
  name: string;
  model?: string;
}
export interface EditingWorker {
  id: string;
  name: string;
  seen: number;
  agents: AgentProfile[];
  /** C5: the server says whether it's connected (else `seen` decides, lib/status.ts). */
  online?: boolean;
  /** Local mode: why this machine can't edit right now. */
  blocked?: string;
}
export interface EditingTask {
  id: string;
  notes: number[];
  version: string;
  /** The version the task makes or updates: its comments' version + 1 (spec/automation.md). */
  target?: string;
  state: string;
  agent: string;
  worker: string;
  attempt: number;
  next_agent?: string;
  error?: string;
  question?: string;
  result?: string;
  /** Notes an earlier attempt finished while others waited on an answer, and notes withdrawn from the batch. */
  done?: number[];
  dropped?: number[];
  events: { at: number; agent?: string; text: string }[];
  usage?: { input_tokens?: number; output_tokens?: number; estimated_cost_usd?: number };
}
export interface AutomationConfig {
  /** Older pages' "send finished reviews automatically". This page never sets it; it keeps whatever is there. */
  enabled: boolean;
  worker: string;
  agent: string;
  fallback?: string;
}
export interface AutomationState {
  config: AutomationConfig;
  workers: EditingWorker[];
  tasks: EditingTask[];
  renders: { label: string; complete: boolean; error?: string }[];
  /** C1: the worker runs inside this server (`retake open`), or connects from elsewhere (hosted). Absent on older
   * servers. */
  local?: boolean;
  /** Local mode: why the in-process worker can't edit (no agent installed, …). */
  blocked?: string;
  /** Takes sent with Send whose notes are still being processed: they're on their way to the agent. */
  sending?: { version: string; take: string }[];
}

/** The fallback when the server can't mint a connect command (an older server without POST /api/connect). */
export const CONNECT_FALLBACK = 'retake worker --remote NAME --project DIR';

/** POST <url> (C5's api/connect, owner only): `{"command": "retake connect https://host --token rk_…"}`. Anything
 * else (a 404 from an older server, a refusal, no network) gives the CLI line. */
export async function connectCommand(url: string, f: typeof fetch = fetch): Promise<string> {
  try {
    const r = await f(url, { method: 'POST' });
    const body = r.ok ? ((await r.json().catch(() => ({}))) as { command?: unknown }) : {};
    return typeof body.command === 'string' && body.command.trim() ? body.command.trim() : CONNECT_FALLBACK;
  } catch {
    return CONNECT_FALLBACK;
  }
}

/** Durable state comes from the host; provider credentials and raw agent logs remain on the editing machine. */
export class Automation {
  state = $state.raw<AutomationState | null>(null);
  error = $state('');
  busy = $state(false);
  /** The `retake connect …` line, once asked for (each ask mints a new token, so it's asked once per page). */
  command = $state('');
  #connecting: Promise<void> | null = null;

  async refresh(): Promise<void> {
    try {
      const response = await fetch('api/automation');
      if (response.ok) this.state = (await response.json()) as AutomationState;
    } catch {
      /* reconnect on the next heartbeat or event */
    }
  }

  async send(path: string, body: unknown): Promise<boolean> {
    this.busy = true;
    this.error = '';
    try {
      const response = await fetch(`api/automation${path}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!response.ok) {
        const error = (await response.json().catch(() => ({}))) as { error?: string };
        throw new Error(error.error || 'Could not update the task');
      }
      await this.refresh();
      return true;
    } catch (error) {
      this.error = error instanceof Error ? error.message : String(error);
      return false;
    } finally {
      this.busy = false;
    }
  }

  /** Change the agent settings (owner): the rest of the config stays as it is. */
  configure(change: Partial<AutomationConfig>): Promise<boolean> {
    const c = this.state?.config ?? { enabled: false, worker: '', agent: '' };
    return this.send('', { ...c, fallback: c.fallback ?? '', ...change });
  }

  /** POST <home>api/connect (C5, owner only): the one line that connects a computer. Falls back to the CLI line. */
  connect(home = ''): Promise<void> {
    this.#connecting ??= connectCommand(`${home}api/connect`).then((c) => {
      this.command = c;
    });
    return this.#connecting;
  }
}
