import { describe, expect, it } from 'vitest';
import { clamp, fmt, isoLocal, pad2, r3, r4, stamp } from './util';

describe('fmt', () => {
  it('formats m:ss.t with tenths', () => {
    expect(fmt(0)).toBe('0:00.0');
    expect(fmt(9.96)).toBe('0:09.9'); // floors, never rounds up
    expect(fmt(65.25)).toBe('1:05.2');
    expect(fmt(599.99)).toBe('9:59.9');
  });
  it('drops tenths on request', () => {
    expect(fmt(40, false)).toBe('0:40');
    expect(fmt(10, false)).toBe('0:10');
    expect(fmt(3599.9, false)).toBe('59:59');
  });
  it('adds hours past one hour', () => {
    expect(fmt(3600)).toBe('1:00:00.0');
    expect(fmt(3723.4)).toBe('1:02:03.4');
    expect(fmt(7322, false)).toBe('2:02:02');
  });
  it('treats negative, NaN, infinite and missing times as zero', () => {
    for (const t of [-1, NaN, Infinity, -Infinity, null, undefined]) expect(fmt(t)).toBe('0:00.0');
  });
  it('is exact on float tenths (0.3 is not 0:00.2)', () => {
    expect(fmt(0.3)).toBe('0:00.3');
    expect(fmt(2.7)).toBe('0:02.7');
  });
});

describe('rounding and helpers', () => {
  it('rounds to 3 and 4 decimals', () => {
    expect(r3(1.23456)).toBe(1.235);
    expect(r3(-0.0004)).toBe(-0);
    expect(r4(0.123456)).toBe(0.1235);
    expect(r4(1)).toBe(1);
  });
  it('clamps and pads', () => {
    expect(clamp(5, 0, 3)).toBe(3);
    expect(clamp(-1, 0, 3)).toBe(0);
    expect(clamp(2, 0, 3)).toBe(2);
    expect(pad2(3)).toBe('03');
    expect(pad2(12)).toBe('12');
  });
});

describe('take ids and timestamps', () => {
  it('stamps a take id from local time', () => {
    expect(stamp(new Date(2026, 9, 29, 7, 5, 9))).toBe('2026-10-29_07-05-09');
    expect(stamp(new Date(2026, 9, 29, 7, 5, 9))).toMatch(/^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$/);
  });
  it('writes local ISO time with the UTC offset', () => {
    const d = new Date(2026, 0, 2, 3, 4, 5);
    const s = isoLocal(d);
    expect(s.startsWith('2026-01-02T03:04:05')).toBe(true);
    expect(s).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}$/);
    const off = -d.getTimezoneOffset();
    expect(s.slice(-6)).toBe(
      `${off >= 0 ? '+' : '-'}${pad2(Math.floor(Math.abs(off) / 60))}:${pad2(Math.abs(off) % 60)}`,
    );
  });
});
