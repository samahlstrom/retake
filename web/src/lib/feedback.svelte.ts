type FeedbackType = 'bug' | 'feature';

const characters = (value: string): number => Array.from(value).length;

function issueURL(value: unknown, number: unknown): string {
  if (typeof value !== 'string' || typeof number !== 'number' || !Number.isInteger(number) || number < 1) return '';
  try {
    const u = new URL(value);
    return u.origin === 'https://github.com' &&
      u.pathname === `/samahlstrom/retake/issues/${number}` &&
      !u.search &&
      !u.hash
      ? u.toString()
      : '';
  } catch {
    return '';
  }
}

async function errorText(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { error?: unknown };
    if (typeof body.error === 'string' && body.error) return body.error;
  } catch {
    // A proxy may replace the JSON error. The draft remains available either way.
  }
  return 'Couldn’t send feedback. Try again.';
}

/** The feedback draft and its safe, testable API boundary. */
export class Feedback {
  type = $state<FeedbackType>('bug');
  title = $state('');
  description = $state('');
  enabled = $state<boolean | null>(null);
  pending = $state(false);
  error = $state('');
  url = $state('');
  number = $state<number | null>(null);

  readonly #fetch: typeof globalThis.fetch;

  constructor(fetcher?: typeof globalThis.fetch) {
    this.#fetch = fetcher ?? ((...args) => globalThis.fetch(...args));
  }

  async load(): Promise<void> {
    try {
      const response = await this.#fetch('api/feedback', { cache: 'no-store' });
      if (!response.ok) {
        this.enabled = false;
        return;
      }
      const body = (await response.json()) as { enabled?: unknown };
      this.enabled = body.enabled === true;
      this.error = '';
    } catch {
      this.enabled = false;
    }
  }

  githubURL(): string {
    return (
      'https://github.com/samahlstrom/retake/issues/new?title=' +
      encodeURIComponent(this.title) +
      '&body=' +
      encodeURIComponent(`Type: ${this.type}\n\n${this.description}`)
    );
  }

  /** A confirmed report is not a draft: the next panel starts clean. */
  resetSuccess(): void {
    if (!this.url) return;
    this.type = 'bug';
    this.title = '';
    this.description = '';
    this.error = '';
    this.url = '';
    this.number = null;
  }

  async submit(): Promise<void> {
    if (this.pending || this.url) return;
    const title = this.title.trim();
    const description = this.description.trim();
    if (!title || characters(title) > 200 || !description || characters(description) > 8000) {
      this.error = 'Give a title up to 200 characters and details up to 8,000 characters.';
      return;
    }
    this.pending = true;
    this.error = '';
    try {
      const response = await this.#fetch('api/feedback', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: this.type, title, description }),
      });
      if (response.status !== 201) {
        this.error = await errorText(response);
        return;
      }
      const body = (await response.json()) as { url?: unknown; number?: unknown };
      const url = issueURL(body.url, body.number);
      if (!url) {
        this.error = 'Couldn’t confirm the GitHub issue. Try again.';
        return;
      }
      this.url = url;
      this.number = body.number as number;
    } catch {
      this.error = 'Couldn’t send feedback. Try again.';
    } finally {
      this.pending = false;
    }
  }
}
