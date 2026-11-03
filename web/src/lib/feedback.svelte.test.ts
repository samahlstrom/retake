import { describe, expect, it, vi } from 'vitest';
import { Feedback } from './feedback.svelte';

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });

describe('Feedback', () => {
  it('uses the browser fetch with its required Window receiver', async () => {
    vi.stubGlobal('fetch', function (this: unknown) {
      if (this !== globalThis) throw new TypeError('Illegal invocation');
      return Promise.resolve(json(200, { enabled: true }));
    });
    try {
      const feedback = new Feedback();
      await feedback.load();
      expect(feedback.enabled).toBe(true);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('loads availability and makes a GitHub fallback from only the draft', async () => {
    const fetch = vi.fn(async () => json(200, { enabled: false })) as unknown as typeof globalThis.fetch;
    const feedback = new Feedback(fetch);
    feedback.type = 'feature';
    feedback.title = 'Export the notes';
    feedback.description = 'Please add a CSV export.';

    await feedback.load();

    expect(feedback.enabled).toBe(false);
    expect(feedback.error).toBe('');
    const fallback = new URL(feedback.githubURL());
    expect(fallback.origin + fallback.pathname).toBe('https://github.com/samahlstrom/retake/issues/new');
    expect(fallback.searchParams.get('title')).toBe('Export the notes');
    expect(fallback.searchParams.get('body')).toBe('Type: feature\n\nPlease add a CSV export.');
    expect(fetch).toHaveBeenCalledWith('api/feedback', expect.anything());
    expect(fallback.toString()).not.toContain('private-path');
  });

  it('validates the draft locally and preserves it after a rejected post', async () => {
    const fetch = vi.fn(async () =>
      json(502, { error: 'GitHub is unavailable' }),
    ) as unknown as typeof globalThis.fetch;
    const feedback = new Feedback(fetch);
    feedback.type = 'bug';
    feedback.title = 'x'.repeat(201);
    feedback.description = 'detail';

    await feedback.submit();
    expect(feedback.error).not.toBe('');
    expect(fetch).not.toHaveBeenCalled();

    feedback.title = 'The export misses rows';
    await feedback.submit();
    expect(feedback.error).not.toBe('');
    expect(feedback.title).toBe('The export misses rows');
    expect(feedback.description).toBe('detail');
    expect(feedback.url).toBe('');
    expect(feedback.number).toBeNull();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('sends once while pending and only succeeds with a valid created issue', async () => {
    let release!: () => void;
    const wait = new Promise<void>((resolve) => (release = resolve));
    const fetch = vi.fn(async () => {
      await wait;
      return json(201, { url: 'https://github.com/samahlstrom/retake/issues/42', number: 42 });
    }) as unknown as typeof globalThis.fetch;
    const feedback = new Feedback(fetch);
    feedback.type = 'feature';
    feedback.title = 'Export notes';
    feedback.description = 'A CSV download would help.';

    const first = feedback.submit();
    const second = feedback.submit();
    expect(feedback.pending).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(1);
    release();
    await Promise.all([first, second]);
    expect(feedback.pending).toBe(false);
    expect(feedback.url).toBe('https://github.com/samahlstrom/retake/issues/42');
    expect(feedback.number).toBe(42);

    const invalid = new Feedback(
      vi.fn(async () => json(201, { url: 'https://example.test/issues/42', number: 42 })) as unknown as typeof fetch,
    );
    invalid.title = 'Still broken';
    invalid.description = 'The issue URL must be GitHub for this fixed project.';
    await invalid.submit();
    expect(invalid.url).toBe('');
    expect(invalid.error).not.toBe('');

    const wrongStatus = new Feedback(
      vi.fn(async () =>
        json(200, { url: 'https://github.com/samahlstrom/retake/issues/43', number: 43 }),
      ) as unknown as typeof fetch,
    );
    wrongStatus.title = 'Still broken';
    wrongStatus.description = 'Only a created issue is a success.';
    await wrongStatus.submit();
    expect(wrongStatus.url).toBe('');
    expect(wrongStatus.error).not.toBe('');
  });
});
