// Small helpers shared by every part of the page. Same math as the legacy player.

/** Seconds on the monotonic page clock (session_t is measured on it). */
export const now = (): number => performance.now() / 1000;
export const r3 = (x: number): number => Math.round(x * 1000) / 1000;
export const r4 = (x: number): number => Math.round(x * 10000) / 10000;
export const clamp = (x: number, a: number, b: number): number => Math.min(b, Math.max(a, x));
export const pad2 = (n: number): string => String(n).padStart(2, '0');

/** 0:00.0 (or h:mm:ss.t); `tenths = false` drops the tenths. Negative or non-finite times show as 0. */
export function fmt(t: number | null | undefined, tenths = true): string {
  let x = typeof t === 'number' ? t : NaN;
  if (!isFinite(x) || x < 0) x = 0;
  const tt = Math.floor(x * 10 + 1e-6),
    s = Math.floor(tt / 10),
    m = Math.floor(s / 60),
    h = Math.floor(m / 60);
  const body = h ? `${h}:${pad2(m % 60)}:${pad2(s % 60)}` : `${m}:${pad2(s % 60)}`;
  return tenths ? `${body}.${tt % 10}` : body;
}

/** A take id: local time as YYYY-MM-DD_HH-MM-SS. */
export const stamp = (d: Date): string =>
  `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}_${pad2(d.getHours())}-${pad2(d.getMinutes())}-${pad2(d.getSeconds())}`;

/** Local ISO 8601 time with the UTC offset, e.g. 2026-10-29T20:41:05+02:00. */
export function isoLocal(d: Date): string {
  const off = -d.getTimezoneOffset(),
    sg = off >= 0 ? '+' : '-';
  return (
    `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}T${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}` +
    `${sg}${pad2(Math.floor(Math.abs(off) / 60))}:${pad2(Math.abs(off) % 60)}`
  );
}
