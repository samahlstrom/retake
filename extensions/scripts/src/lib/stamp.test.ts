import { describe, expect, it } from 'vitest';
import { ago, clock, parseStamp, size, stamp, when } from './stamp';

describe('stamps', () => {
  it('formats a talk-out stamp in local time as yyyymmdd-hhmmss', () => {
    expect(stamp(new Date(2026, 10, 1, 19, 2, 11))).toBe('20261101-190211');
    expect(stamp(new Date(2026, 0, 5, 3, 4, 5))).toBe('20260105-030405');
  });

  it('reads a stamp back, with or without a -2', () => {
    expect(parseStamp('20261101-190211')?.getTime()).toBe(new Date(2026, 10, 1, 19, 2, 11).getTime());
    expect(parseStamp('20261101-190211-2')?.getHours()).toBe(19);
    expect(parseStamp('20261341-190211')).toBeNull();
    expect(parseStamp('hello')).toBeNull();
  });

  it('says times as m:ss', () => {
    expect(clock(0)).toBe('0:00');
    expect(clock(12.4)).toBe('0:12');
    expect(clock(61)).toBe('1:01');
    expect(clock(3725)).toBe('1:02:05');
    expect(clock(NaN)).toBe('0:00');
  });

  it('says when, quietly', () => {
    const now = new Date(2026, 10, 1, 19, 0, 0).getTime();
    expect(ago(now - 20_000, now)).toBe('just now');
    expect(ago(now - 5 * 60_000, now)).toBe('5 min ago');
    expect(ago(now - 3 * 3600_000, now)).toBe('3 h ago');
    expect(ago(new Date(2026, 9, 31, 23, 0).getTime(), now)).toBe('yesterday');
    expect(ago(new Date(2026, 9, 3, 12, 0).getTime(), now)).toBe('Oct 3');
    expect(ago(new Date(2025, 9, 3, 12, 0).getTime(), now)).toBe('Oct 3, 2025');
    const n = new Date(2026, 10, 1, 19, 0, 0);
    expect(when(new Date(2026, 10, 1, 8, 5), n)).toBe('today 08:05');
    expect(when(new Date(2026, 9, 31, 22, 0), n)).toBe('yesterday 22:00');
    expect(when(new Date(2026, 9, 3, 9, 30), n)).toBe('Oct 3 09:30');
  });

  it('says sizes', () => {
    expect(size(820)).toBe('820 B');
    expect(size(14_200)).toBe('14 KB');
    expect(size(3_240_000)).toBe('3.2 MB');
    expect(size(26_000_000)).toBe('26 MB');
  });
});
