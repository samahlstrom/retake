import { describe, expect, it } from 'vitest';
import { MAX_SLUG, nth, slug } from './slug';

describe('slug', () => {
  it('is the title in lower case with every run of other characters turned into -', () => {
    expect(slug('Jump to Secure Souls!')).toBe('jump-to-secure-souls');
    expect(slug('  The   (best) item_shop?? build  ')).toBe('the-best-item-shop-build');
    expect(slug('v2.0 / take 3')).toBe('v2-0-take-3');
  });

  it('drops accents and keeps digits', () => {
    expect(slug('Café Crème 2026')).toBe('cafe-creme-2026');
  });

  it('is at most 63 characters and never ends in -', () => {
    const s = slug('a'.repeat(62) + ' bcd');
    expect(s.length).toBeLessThanOrEqual(MAX_SLUG);
    expect(s.endsWith('-')).toBe(false);
    expect(slug('x'.repeat(100))).toBe('x'.repeat(63));
  });

  it('is "idea" for a title with no letters or digits', () => {
    expect(slug('!!! ???')).toBe('idea');
    expect(slug('')).toBe('idea');
  });

  it('numbers the later tries -2, -3, … within 63 characters', () => {
    expect(nth('jump', 1)).toBe('jump');
    expect(nth('jump', 2)).toBe('jump-2');
    expect(nth('jump', 13)).toBe('jump-13');
    const long = slug('y'.repeat(63));
    expect(nth(long, 2)).toBe(`${'y'.repeat(61)}-2`);
    expect(nth(long, 2).length).toBe(63);
    expect(nth(`${'a'.repeat(60)}-bc`, 10)).toBe(`${'a'.repeat(60)}-10`);
  });
});
