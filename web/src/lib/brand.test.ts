import { describe, expect, it } from 'vitest';
import { brandOf, homeTitle, reviewTitle } from './brand';

/** A document with these metas. */
const doc = (metas: Record<string, string>): Pick<Document, 'querySelector'> => ({
  querySelector: ((selector: string) => {
    const name = /meta\[name="([^"]+)"\]/.exec(selector)?.[1] ?? '';
    return name in metas ? { content: metas[name] } : null;
  }) as Document['querySelector'],
});

describe('the brand', () => {
  it('is Retake with no logo without a theme', () => {
    const b = brandOf(doc({}));
    expect(b).toEqual({ name: 'Retake', logo: '', custom: false });
    expect(homeTitle('Projects', b)).toBe('Projects · Retake');
    expect(reviewTitle('clip_v3', b)).toBe('Review · clip_v3'); // the title the page always had
  });
  it("is the theme's name and logo when the server put them in", () => {
    const b = brandOf(doc({ 'retake-brand': ' Acme Review ', 'retake-logo': 'theme/logo.svg' }));
    expect(b).toEqual({ name: 'Acme Review', logo: 'theme/logo.svg', custom: true });
    expect(homeTitle('Launch', b)).toBe('Launch · Acme Review');
    expect(reviewTitle('clip_v3', b)).toBe('Review · clip_v3 · Acme Review');
  });
  it('ignores an empty name', () => {
    expect(brandOf(doc({ 'retake-brand': '  ' })).custom).toBe(false);
  });
  it('works with no document at all', () => {
    expect(brandOf(undefined).name).toBe('Retake');
  });
});
