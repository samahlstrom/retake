import { describe, expect, it } from 'vitest';
import { hasCut } from './versions';

describe('Start v1', () => {
  it('shows while api/info lists no version that is not a kind', () => {
    expect(hasCut({ versions: [{ label: 'v0', kind: 'ignition.doc' }] })).toBe(false);
    expect(hasCut({ versions: [{ label: 'v0', kind: 'ignition.doc' }, { label: 'v1' }] })).toBe(true);
    expect(hasCut({ versions: [{ label: 'v1', kind: null }] })).toBe(true);
  });

  it("doesn't know without a list", () => {
    expect(hasCut({})).toBeNull();
    expect(hasCut(null)).toBeNull();
  });
});
