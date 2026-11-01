import { describe, expect, it } from 'vitest';
import { taskLabel, type EditingTask } from './automation.svelte';
import { inCurrentReview, markOf, toRow } from './notes/view';

describe('live comment task states', () => {
  const task = (state: string): EditingTask => ({
    id: 'task',
    state,
    notes: [1],
    version: 'v6',
    agent: 'codex',
    worker: 'machine',
    attempt: 1,
    events: [],
  });
  it('keeps operational pauses separate from creative questions', () => {
    expect(taskLabel(task('queued'))).toBe('Commented');
    expect(taskLabel(task('paused'))).toBe('Commented');
    expect(taskLabel(task('running'))).toBe('In progress');
    expect(taskLabel(task('stopping'))).toBe('In progress');
    expect(taskLabel(task('question'))).toBe('Question');
    expect(taskLabel(task('completed'))).toBe('Completed');
  });
  it('keeps the original annotation and version while surfacing the task question', () => {
    const item = {
      id: 1,
      kind: 'note' as const,
      status: 'open',
      t0: 12,
      text: 'Change this title',
      version: 'v6',
      drawings: ['red'],
      work: { ...task('question'), question: 'Blue or teal?' },
    };
    expect(markOf(item)).toBe('question');
    const row = toRow(item, false, { versions: [{ label: 'v6' }, { label: 'v7' }], shown: 'v7' });
    expect(row.version).toBe('v6');
    expect(row.drawings).toEqual(['red']);
    expect(row.work?.question).toBe('Blue or teal?');
    expect(inCurrentReview(row, 'v7')).toBe(true);
    expect(inCurrentReview({ ...row, work: undefined }, 'v7')).toBe(false);
  });
});
