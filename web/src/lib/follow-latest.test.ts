import { expect, it } from 'vitest';
import { Review } from './review.svelte';

it('catches a revision that arrived during the previous media switch', async () => {
  const calls: string[] = [];
  const view = {
    followLatest: true,
    finishing: false,
    seg: null,
    starting: false,
    switching: false,
    shown: 'v1',
    video: { currentTime: 1 },
    versions: [{ label: 'v1' }, { label: 'v2' }],
    async setBase(label: string) {
      calls.push(label);
      this.switching = true;
      if (label === 'v2') this.versions.push({ label: 'v3' });
      await Promise.resolve();
      this.shown = label;
      this.switching = false;
    },
  };
  await Review.prototype.followCurrent.call(view as unknown as Review);
  expect(calls).toEqual(['v2', 'v3']);
  expect(view.shown).toBe('v3');
  view.followLatest = false;
  view.versions.push({ label: 'v4' });
  await Review.prototype.followCurrent.call(view as unknown as Review);
  expect(view.shown).toBe('v3');
});
