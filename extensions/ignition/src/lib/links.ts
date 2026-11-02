// Links and images in the document: where a link may go when tapped, the owned file an image names (relative to v0/),
// and the name a pasted or dropped image is saved under (v0/images/<name>).

/** Where a tapped link opens, or null: only the web and mail (never javascript: or a relative path). */
export function safeHref(raw: string): string | null {
  const url = raw.trim().replace(/^<(.*)>$/, '$1');
  if (/^https?:\/\/[^\s]+$/i.test(url) || /^mailto:[^\s]+$/i.test(url)) return url;
  if (/^www\.[^\s]+$/i.test(url)) return `https://${url}`;
  return null;
}

/** The owned file an image's source names, relative to the document's folder v0/ ("images/a.png" is
 * "v0/images/a.png"), or null: a web address, an absolute path, or a path out of v0/. */
export function imagePath(src: string): string | null {
  let s = src.trim().replace(/^<(.*)>$/, '$1');
  if (!s || /^[a-z][a-z0-9+.-]*:/i.test(s) || s.startsWith('/') || s.startsWith('\\')) return null;
  s = s.replace(/[?#].*$/, '');
  try {
    s = decodeURI(s);
  } catch {
    return null;
  }
  const out: string[] = [];
  for (const seg of s.split('/')) {
    if (seg === '' || seg === '.') continue;
    if (seg === '..') {
      if (!out.length) return null;
      out.pop();
    } else out.push(seg);
  }
  return out.length ? `v0/${out.join('/')}` : null;
}

/** An image's address for <img>: an owned file's URL, or a https address as it is; null when it can't show. */
export function imageUrl(src: string, url: (path: string) => string): string | null {
  const s = src.trim().replace(/^<(.*)>$/, '$1');
  if (/^https:\/\/[^\s]+$/i.test(s)) return s;
  const p = imagePath(s);
  return p ? url(p) : null;
}

const EXT: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/webp': 'webp',
  'image/gif': 'gif',
  'image/avif': 'avif',
};

/** The largest image a paste or a drop saves (the files API's limit for one file). */
export const MAX_IMAGE = 25 << 20;

/** Whether a pasted or dropped file is an image the document can show (never an SVG: it can hold a script). */
export const isImage = (f: { type: string }): boolean => f.type in EXT;

/**
 * The name a pasted or dropped image is saved under in v0/images/: its own name in lower case with every run of other
 * characters turned into "-" (a pasted screenshot's "image.png" is "image"), at most 48 characters, then the stamp and
 * the extension of its type. `n` > 1 adds "-n" for a name that's taken.
 */
export function imageName(file: { name?: string; type: string }, st: string, n = 1): string {
  const ext = EXT[file.type] ?? 'png';
  const base = (file.name ?? '')
    .replace(/\.[a-z0-9]{1,5}$/i, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48)
    .replace(/-+$/, '');
  const stem = `${base || 'image'}-${st}`;
  return `${n > 1 ? `${stem}-${n}` : stem}.${ext}`;
}

/** The Markdown that shows a saved image: `![](images/<name>)` (a name with spaces or brackets in <>). */
export const imageMarkdown = (name: string): string =>
  /[\s()<>]/.test(name) ? `![](<images/${name}>)` : `![](images/${name})`;
