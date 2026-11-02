import { describe, expect, it } from 'vitest';
import { decide } from './merge';

describe('an outside edit', () => {
  const have = { sha: 'a', dirty: false, saving: false };

  it('is ignored when it is the version we have (our own save coming back)', () => {
    expect(decide({ sha256: 'a' }, have)).toBe('ignore');
    expect(decide({ sha256: 'a' }, { ...have, dirty: true })).toBe('ignore');
  });

  it('reloads in place when there is no unsaved typing', () => {
    expect(decide({ sha256: 'b' }, have)).toBe('reload');
  });

  it('asks when there is unsaved typing', () => {
    expect(decide({ sha256: 'b' }, { ...have, dirty: true })).toBe('conflict');
  });

  it('waits for a save on its way', () => {
    expect(decide({ sha256: 'b' }, { ...have, saving: true })).toBe('later');
  });

  it('treats a deletion as a change to "no file"', () => {
    expect(decide({ sha256: 'a', deleted: true }, have)).toBe('reload');
    expect(decide({ sha256: 'a', deleted: true }, { ...have, dirty: true })).toBe('conflict');
    expect(decide({ sha256: 'x', deleted: true }, { sha: '', dirty: false, saving: false })).toBe('ignore');
  });
});
