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
}
export interface EditingTask {
  id: string;
  notes: number[];
  version: string;
  state: string;
  agent: string;
  worker: string;
  attempt: number;
  next_agent?: string;
  error?: string;
  question?: string;
  result?: string;
  events: { at: number; agent?: string; text: string }[];
  usage?: { input_tokens?: number; output_tokens?: number; estimated_cost_usd?: number };
}
export interface AutomationConfig {
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
}

export function taskLabel(task?: EditingTask): string {
  if (task?.state === 'completed') return 'Completed';
  if (task?.state === 'question') return 'Question';
  if (task?.state === 'running' || task?.state === 'stopping') return 'In progress';
  return 'Commented';
}

/** Durable state comes from the host; provider credentials and raw agent logs remain on the editing machine. */
export class Automation {
  state = $state.raw<AutomationState | null>(null);
  error = $state('');
  busy = $state(false);
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
        const error = (await response.json()) as { error?: string };
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
}
