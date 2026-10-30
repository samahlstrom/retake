import { describe, expect, it } from 'vitest';
import phoneCss from '../phone.css?raw';
import { layoutMode, PHONE_MEDIA } from './layout';

const mode = (width: number, height: number, coarse: boolean) => layoutMode({ width, height, coarse });

describe('layout mode', () => {
  it('keeps the desktop layout for desktop windows at any normal size', () => {
    for (const [w, h] of [
      [1920, 1080],
      [2560, 1440],
      [1280, 720],
      [1024, 768],
      [800, 600],
      [800, 420],
    ])
      expect(mode(w, h, false), `${w}x${h}`).toBe('desktop');
  });

  it('gives phones their own layout, by orientation', () => {
    expect(mode(390, 844, true)).toBe('portrait'); // iPhone 14/15
    expect(mode(844, 390, true)).toBe('landscape');
    expect(mode(430, 932, true)).toBe('portrait'); // iPhone Pro Max
    expect(mode(932, 430, true)).toBe('landscape');
    expect(mode(360, 780, true)).toBe('portrait'); // small Android
    expect(mode(915, 412, true)).toBe('landscape');
  });

  it('treats a small touch tablet in portrait as a phone, a big one or a landscape tablet as a desktop', () => {
    expect(mode(820, 1180, true)).toBe('portrait'); // iPad Air portrait
    expect(mode(1180, 820, true)).toBe('desktop');
    expect(mode(1024, 1366, true)).toBe('desktop'); // iPad Pro 12.9 portrait
  });

  it('switches a desktop window that is too small for the bar', () => {
    expect(mode(799, 900, false)).toBe('portrait');
    expect(mode(1200, 419, false)).toBe('landscape');
    expect(mode(600, 600, false)).toBe('portrait'); // square: portrait
  });
});

describe('the pre-layout media query', () => {
  it('is the same test as layoutMode', () => {
    expect(PHONE_MEDIA).toBe(
      '(max-width: 799.98px), (max-height: 419.98px), (pointer: coarse) and (max-width: 900px), ' +
        '(pointer: coarse) and (max-height: 600px)',
    );
  });

  it('is the one phone.css hides the prerendered page under', () => {
    const css = phoneCss.replace(/\s+/g, ' ');
    expect(css).toContain(`@media ${PHONE_MEDIA} { html:not([data-layout]) #app { visibility: hidden; } }`);
  });
});
