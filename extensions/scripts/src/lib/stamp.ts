// Talk-out names and the words for times: v0/voice/<yyyymmdd-hhmmss>.webm, "0:12", "3 min ago".

const two = (n: number): string => String(n).padStart(2, '0');

/** A talk-out's stamp in local time: 2026-11-01 19:02:11 is "20261101-190211". */
export function stamp(d: Date): string {
  return (
    `${d.getFullYear()}${two(d.getMonth() + 1)}${two(d.getDate())}-` +
    `${two(d.getHours())}${two(d.getMinutes())}${two(d.getSeconds())}`
  );
}

/** The local time a stamp names (a "-2" after it is allowed), or null. */
export function parseStamp(s: string): Date | null {
  const m = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})(?:-\d+)?$/.exec(s);
  if (!m) return null;
  const [y, mo, d, h, mi, se] = m.slice(1).map(Number) as [number, number, number, number, number, number];
  const date = new Date(y, mo - 1, d, h, mi, se);
  return date.getMonth() === mo - 1 && date.getDate() === d ? date : null;
}

/** Seconds as m:ss (h:mm:ss from an hour). */
export function clock(sec: number): string {
  const s = Math.max(0, Math.floor(Number.isFinite(sec) ? sec : 0));
  const h = Math.floor(s / 3600),
    m = Math.floor((s % 3600) / 60);
  return h ? `${h}:${two(m)}:${two(s % 60)}` : `${m}:${two(s % 60)}`;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/** When something happened, quietly: "just now", "5 min ago", "3 h ago", "yesterday", "Oct 3", "Oct 3, 2025". */
export function ago(at: number, now: number): string {
  const s = Math.round((now - at) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  const a = new Date(at),
    n = new Date(now);
  const days = Math.round(
    (new Date(n.getFullYear(), n.getMonth(), n.getDate()).getTime() -
      new Date(a.getFullYear(), a.getMonth(), a.getDate()).getTime()) /
      86_400_000,
  );
  if (days <= 0) return `${Math.floor(s / 3600)} h ago`;
  if (days === 1) return 'yesterday';
  const day = `${MONTHS[a.getMonth()]} ${a.getDate()}`;
  return a.getFullYear() === n.getFullYear() ? day : `${day}, ${a.getFullYear()}`;
}

/** A talk-out's "when": "today 19:02", "yesterday 08:15", "Oct 3 19:02". */
export function when(d: Date, now: Date): string {
  const time = `${two(d.getHours())}:${two(d.getMinutes())}`;
  const days = Math.round(
    (new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() -
      new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()) /
      86_400_000,
  );
  if (days === 0) return `today ${time}`;
  if (days === 1) return `yesterday ${time}`;
  const day = `${MONTHS[d.getMonth()]} ${d.getDate()}`;
  return `${d.getFullYear() === now.getFullYear() ? day : `${day}, ${d.getFullYear()}`} ${time}`;
}

/** A file size: "820 B", "14 KB", "3.2 MB". */
export function size(bytes: number): string {
  if (bytes < 1000) return `${bytes} B`;
  if (bytes < 1_000_000) return `${Math.round(bytes / 1000)} KB`;
  const mb = bytes / 1_000_000;
  return `${mb < 10 ? mb.toFixed(1) : Math.round(mb)} MB`;
}
