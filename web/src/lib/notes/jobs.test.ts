import { describe, expect, it } from 'vitest';
import { applyJob, jobLine } from './jobs';
import type { Job } from './types';

const run = (list: Job[], ...evs: Job[]) => evs.reduce((l, e) => applyJob(l, e), list);

describe('job progress', () => {
  it('shows the running job, then nothing once it is done', () => {
    let l = run([], { id: 1, kind: 'transcribe', state: 'queued' });
    expect(jobLine(l)).toEqual({ text: 'Transcribing your take…', error: false, hint: null });
    l = run(l, { id: 1, kind: 'transcribe', state: 'running' }, { id: 2, kind: 'notes', state: 'queued' });
    expect(jobLine(l)?.text).toBe('Transcribing your take…');
    l = run(l, { id: 1, kind: 'transcribe', state: 'done' }, { id: 2, kind: 'notes', state: 'running' });
    expect(jobLine(l)?.text).toBe('Writing up your notes…');
    l = run(l, { id: 2, kind: 'notes', state: 'done' });
    expect(l).toEqual([]);
    expect(jobLine(l)).toBeNull();
  });

  it('a failure shows the server error and hint first, until a job of that kind succeeds', () => {
    let l = run(
      [],
      { id: 1, kind: 'transcribe', state: 'running' },
      {
        id: 1,
        kind: 'transcribe',
        state: 'failed',
        error: "transcription isn't set up",
        hint: 'Install whisper.cpp, or set [transcribe] command in config.toml',
      },
      { id: 3, kind: 'notes', state: 'running' },
    );
    expect(jobLine(l)).toEqual({
      text: "transcription isn't set up",
      error: true,
      hint: 'Install whisper.cpp, or set [transcribe] command in config.toml',
    });
    l = run(l, { id: 4, kind: 'transcribe', state: 'failed' });
    expect(l.filter((j) => j.state === 'failed')).toHaveLength(1); // the latest failure of a kind replaces the last
    expect(jobLine(l)).toEqual({ text: 'Transcription failed', error: true, hint: null });
    l = run(l, { id: 5, kind: 'transcribe', state: 'done' });
    expect(jobLine(l)?.text).toBe('Writing up your notes…');
  });

  it('names unknown kinds plainly', () => {
    expect(jobLine([{ id: 'x', kind: 'thumbs', state: 'running' }])?.text).toBe('Working: thumbs…');
    expect(jobLine([{ id: 'x', kind: 'thumbs', state: 'failed' }])?.text).toBe('thumbs failed');
  });
});
