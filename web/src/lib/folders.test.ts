import { describe, expect, it } from 'vitest';
import { join } from './folders';

describe('join', () => {
  it('joins with the separator the folder already uses', () => {
    expect(join('~', 'Clips')).toBe('~/Clips');
    expect(join('/', 'x')).toBe('/x');
    expect(join('~/Videos', 'a b')).toBe('~/Videos/a b');
    // Windows: a drive's top, a folder outside the home folder, and one under it (the server shows those as ~/…).
    expect(join('C:\\', 'Users')).toBe('C:\\Users');
    expect(join('D:\\Footage', 'raw')).toBe('D:\\Footage\\raw');
    expect(join('~/Videos', 'raw')).toBe('~/Videos/raw');
  });
});
