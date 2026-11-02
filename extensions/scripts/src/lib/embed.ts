// Embeds (spec/scripts.md, "Embeds"): a web address alone on its own line shows what it points to. Known sites play
// in a frame on their own embed address (SITES: adding a site is one entry), a raw <iframe> line is rebuilt with only
// its address and size, and any other address is a link card. Every frame gets the same attributes and nothing else.

/** A frame the document shows. Its box is reserved before it loads: a ratio, or a height, and a widest width. */
export interface Frame {
  kind: 'frame';
  /** The site's name ("YouTube"), or the host of a raw frame. */
  site: string;
  src: string;
  title: string;
  /** Width over height, when the box keeps a ratio. */
  ratio?: number;
  /** A fixed height in px, when it doesn't. */
  height?: number;
  /** At most this wide, in px (a portrait player, a post). */
  maxWidth?: number;
}

/** A page that isn't a known site's: a link card. */
export interface Card {
  kind: 'card';
  url: string;
}

export type Embed = Frame | Card;

/** The attributes every frame gets, besides its src and title (spec/scripts.md, "Every frame"). */
export const FRAME_ATTRS: Readonly<Record<string, string>> = {
  sandbox: 'allow-scripts allow-same-origin allow-popups allow-presentation',
  allow: 'fullscreen; picture-in-picture; encrypted-media',
  referrerpolicy: 'strict-origin-when-cross-origin',
  loading: 'lazy',
};

type Box = Pick<Frame, 'ratio' | 'height' | 'maxWidth'>;
const WIDE: Box = { ratio: 16 / 9 };
const TALL: Box = { ratio: 9 / 16, maxWidth: 340 };

/** A site the document embeds: its frame for an address of it, or null when that address isn't one it plays. */
export interface Site {
  name: string;
  /** `u` is the address; `page` is this page's host name (Twitch wants it as `parent`). */
  frame(u: URL, page: string): (Box & { src: string; title: string }) | null;
}

/** The host without www., m. or mobile. */
const bare = (u: URL): string => u.hostname.toLowerCase().replace(/^(www|m|mobile)\./, '');
/** The path's segments, without empty ones. */
const segs = (u: URL): string[] =>
  u.pathname
    .split('/')
    .filter(Boolean)
    .map((x) => {
      try {
        return decodeURIComponent(x);
      } catch {
        return x;
      }
    });

/** Seconds from `90`, `90s`, `1m30s`, `1h2m3s`; 0 when it's none of those. */
export function seconds(t: string | null | undefined): number {
  const s = (t ?? '').trim().toLowerCase();
  if (/^\d+$/.test(s)) return Number(s);
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(s);
  if (!m || !s) return 0;
  return Number(m[1] ?? 0) * 3600 + Number(m[2] ?? 0) * 60 + Number(m[3] ?? 0);
}

/** `1h2m3s` for Twitch's time. */
const hms = (n: number): string => `${Math.floor(n / 3600)}h${Math.floor((n % 3600) / 60)}m${n % 60}s`;

/** The sites, in the order they're tried (spec/scripts.md, "Embeds", the table). */
export const SITES: Site[] = [
  {
    name: 'YouTube',
    frame(u) {
      const h = bare(u);
      const [a = '', b = ''] = segs(u);
      let id = '';
      let box = WIDE;
      if (h === 'youtu.be') id = a;
      else if (h === 'youtube.com' || h === 'music.youtube.com' || h === 'youtube-nocookie.com') {
        if (a === 'watch') id = u.searchParams.get('v') ?? '';
        else if (a === 'shorts') [id, box] = [b, TALL];
        else if (a === 'live' || a === 'embed' || a === 'v') id = b;
      }
      if (!/^[\w-]{11}$/.test(id)) return null;
      const start = seconds(u.searchParams.get('t') ?? u.searchParams.get('start') ?? /t=([^&]+)/.exec(u.hash)?.[1]);
      const src = `https://www.youtube-nocookie.com/embed/${id}${start ? `?start=${start}` : ''}`;
      return { src, title: 'YouTube video', ...box };
    },
  },
  {
    name: 'Vimeo',
    frame(u) {
      const h = bare(u);
      const s = segs(u);
      let id = '',
        hash = u.searchParams.get('h') ?? '';
      if (h === 'player.vimeo.com' && s[0] === 'video') id = s[1] ?? '';
      else if (h === 'vimeo.com') {
        const i = s.findIndex((x) => /^\d+$/.test(x));
        if (i >= 0 && (i === 0 || s[0] === 'channels' || s[0] === 'groups' || s[0] === 'showcase')) {
          id = s[i]!;
          if (/^[0-9a-f]{6,}$/i.test(s[i + 1] ?? '')) hash = s[i + 1]!;
        }
      }
      if (!/^\d{1,12}$/.test(id)) return null;
      const t = /t=([^&]+)/.exec(u.hash)?.[1];
      const q = /^[0-9a-f]+$/i.test(hash) ? `?h=${hash}` : '';
      const src = `https://player.vimeo.com/video/${id}${q}${seconds(t) ? `#t=${seconds(t)}s` : ''}`;
      return { src, title: 'Vimeo video', ...WIDE };
    },
  },
  {
    name: 'Loom',
    frame(u) {
      const [a, id = ''] = segs(u);
      if (bare(u) !== 'loom.com' || (a !== 'share' && a !== 'embed') || !/^[0-9a-f]{16,64}$/i.test(id)) return null;
      return { src: `https://www.loom.com/embed/${id}`, title: 'Loom video', ...WIDE };
    },
  },
  {
    name: 'Twitch',
    frame(u, page) {
      const h = bare(u);
      const s = segs(u);
      const parent = encodeURIComponent(page || 'localhost');
      let clip = '';
      if (h === 'clips.twitch.tv') clip = s[0] === 'embed' ? (u.searchParams.get('clip') ?? '') : (s[0] ?? '');
      else if (h === 'twitch.tv' && s[1] === 'clip') clip = s[2] ?? '';
      if (clip) {
        if (!/^[\w-]{3,120}$/.test(clip)) return null;
        return { src: `https://clips.twitch.tv/embed?clip=${clip}&parent=${parent}`, title: 'Twitch clip', ...WIDE };
      }
      if (h === 'twitch.tv' && s[0] === 'videos' && /^\d{1,15}$/.test(s[1] ?? '')) {
        const t = seconds(u.searchParams.get('t'));
        return {
          src: `https://player.twitch.tv/?video=${s[1]}&parent=${parent}&autoplay=false${t ? `&time=${hms(t)}` : ''}`,
          title: 'Twitch video',
          ...WIDE,
        };
      }
      return null;
    },
  },
  {
    name: 'X',
    frame(u) {
      const h = bare(u);
      if (h !== 'x.com' && h !== 'twitter.com') return null;
      const s = segs(u);
      const i = s.indexOf('status');
      const id = i >= 1 ? (s[i + 1] ?? '') : '';
      if (!/^\d{1,20}$/.test(id)) return null;
      return {
        src: `https://platform.twitter.com/embed/Tweet.html?id=${id}`,
        title: 'Post on X',
        height: 560,
        maxWidth: 550,
      };
    },
  },
  {
    name: 'Instagram',
    frame(u) {
      const [a = '', code = ''] = segs(u);
      const kind = a === 'reels' ? 'reel' : a;
      if (bare(u) !== 'instagram.com' || !['p', 'reel', 'tv'].includes(kind) || !/^[\w-]{5,40}$/.test(code))
        return null;
      return {
        src: `https://www.instagram.com/${kind}/${code}/embed`,
        title: 'Instagram post',
        height: 600,
        maxWidth: 400,
      };
    },
  },
  {
    name: 'TikTok',
    frame(u) {
      const s = segs(u);
      const i = s.indexOf('video');
      const id = bare(u) === 'tiktok.com' && i >= 1 && s[0]!.startsWith('@') ? (s[i + 1] ?? '') : '';
      if (!/^\d{5,25}$/.test(id)) return null;
      return { src: `https://www.tiktok.com/embed/v2/${id}`, title: 'TikTok video', height: 700, maxWidth: 340 };
    },
  },
  {
    name: 'Spotify',
    frame(u) {
      let s = segs(u);
      if (/^intl-[a-z-]+$/i.test(s[0] ?? '')) s = s.slice(1);
      if (s[0] === 'embed') s = s.slice(1);
      const [type = '', id = ''] = s;
      const types = ['track', 'album', 'playlist', 'episode', 'show', 'artist'];
      if (bare(u) !== 'open.spotify.com' || !types.includes(type) || !/^[A-Za-z0-9]{10,40}$/.test(id)) return null;
      return {
        src: `https://open.spotify.com/embed/${type}/${id}`,
        title: 'Spotify player',
        height: type === 'track' || type === 'episode' ? 152 : 352,
      };
    },
  },
  {
    name: 'SoundCloud',
    frame(u) {
      const s = segs(u);
      const reserved = ['discover', 'search', 'stream', 'you', 'upload', 'charts', 'stations', 'settings', 'pages'];
      const set = s.length === 3 && s[1] === 'sets';
      if (bare(u) !== 'soundcloud.com' || reserved.includes(s[0] ?? '') || !(s.length === 2 || set)) return null;
      if (!s.every((x) => /^[\w-]+$/.test(x))) return null;
      const page = `https://soundcloud.com/${s.join('/')}`;
      return {
        src: `https://w.soundcloud.com/player/?url=${encodeURIComponent(page)}&visual=false`,
        title: 'SoundCloud player',
        height: set ? 450 : 166,
      };
    },
  },
  {
    name: 'Figma',
    frame(u) {
      const [a = '', key = ''] = segs(u);
      const kinds = ['file', 'design', 'proto', 'board', 'slides', 'deck'];
      if (bare(u) !== 'figma.com' || !kinds.includes(a) || !/^[\w-]{10,64}$/.test(key)) return null;
      const src = `https://www.figma.com/embed?embed_host=retake&url=${encodeURIComponent(u.href)}`;
      return { src, title: 'Figma file', ...WIDE };
    },
  },
  {
    name: 'Google',
    frame(u) {
      const [kind = '', d, id = ''] = segs(u);
      const kinds: Record<string, string> = {
        document: 'Google Doc',
        presentation: 'Google Slides',
        spreadsheets: 'Google Sheet',
      };
      if (bare(u) !== 'docs.google.com' || !kinds[kind] || d !== 'd' || !/^[\w-]{20,80}$/.test(id)) return null;
      return {
        src: `https://docs.google.com/${kind}/d/${id}/preview`,
        title: kinds[kind]!,
        ...(kind === 'presentation' ? WIDE : { height: 560 }),
      };
    },
  },
];

/** The address on a line that holds only it (`https://…` or `<https://…>`), or null. */
export function aloneUrl(line: string): string | null {
  const m = /^\s*(<)?(https?:\/\/[^\s<>]+)(>)?\s*$/i.exec(line);
  if (!m || !m[1] !== !m[3]) return null;
  try {
    const u = new URL(m[2]!);
    return u.hostname ? m[2]! : null;
  } catch {
    return null;
  }
}

/** What an address alone on a line shows: a known site's frame, else a link card. `page` is this page's host. */
export function embedFor(url: string, page: string): Embed | null {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return null;
  }
  if ((u.protocol !== 'https:' && u.protocol !== 'http:') || !u.hostname) return null;
  for (const site of SITES) {
    const f = site.frame(u, page);
    if (f) return { kind: 'frame', site: site.name, ...f };
  }
  return { kind: 'card', url: u.href };
}

const ENTITIES: Record<string, string> = { amp: '&', quot: '"', apos: "'", lt: '<', gt: '>', nbsp: ' ' };
/** An attribute's value with its character references decoded. */
function decode(v: string): string {
  return v.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (all, e: string) => {
    if (e[0] === '#') {
      const n = e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
      return n > 0 && n <= 0x10ffff ? String.fromCodePoint(n) : '';
    }
    return ENTITIES[e.toLowerCase()] ?? all;
  });
}

/** A size attribute in px ("560", "560px"), or null for anything else ("100%"). */
function px(v: string | undefined): number | null {
  const m = /^\s*(\d{1,5}(?:\.\d+)?)\s*(?:px)?\s*$/i.exec(v ?? '');
  const n = m ? Number(m[1]) : NaN;
  return n > 0 ? n : null;
}

const clamp = (n: number, lo: number, hi: number): number => Math.min(hi, Math.max(lo, n));

/**
 * A raw frame (spec/scripts.md, "A raw frame"): a line that is only `<iframe …></iframe>`, rebuilt from its `src`
 * (https, not `page`, this page's own host), `width`, `height` and `title`; every other attribute is dropped. null when
 * the line isn't one safe frame.
 */
export function rawFrame(line: string, page: string): Frame | null {
  const m = /^\s*<iframe\b([^<>]*)>\s*(?:<\/iframe\s*>)?\s*$/i.exec(line);
  if (!m) return null;
  const attrs: Record<string, string> = Object.create(null) as Record<string, string>;
  const re = /([^\s"'<>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;
  const body = m[1]!.replace(/\/\s*$/, '');
  let a: RegExpExecArray | null;
  let at = 0;
  while ((a = re.exec(body))) {
    if (body.slice(at, a.index).trim()) return null; // something that isn't an attribute
    at = a.index + a[0].length;
    const k = a[1]!.toLowerCase();
    if (!(k in attrs)) attrs[k] = decode(a[2] ?? a[3] ?? a[4] ?? '');
  }
  if (body.slice(at).trim()) return null;
  let u: URL;
  try {
    u = new URL((attrs.src ?? '').trim());
  } catch {
    return null;
  }
  const own = page.toLowerCase().replace(/^\[|\]$/g, '');
  if (u.protocol !== 'https:' || !u.hostname || u.username || u.password) return null;
  if (u.hostname.toLowerCase().replace(/^\[|\]$/g, '') === own) return null; // never Retake itself
  const w = px(attrs.width),
    h = px(attrs.height);
  const box: Box = w && h ? { ratio: clamp(w / h, 1 / 3, 4) } : h ? { height: Math.round(clamp(h, 80, 1200)) } : WIDE;
  const title = (attrs.title ?? '').replace(/\s+/g, ' ').trim().slice(0, 200) || `Embedded from ${u.hostname}`;
  return { kind: 'frame', site: u.hostname, src: u.href, title, ...box };
}

/** What a line of the document embeds, or null: an address alone on it, or one raw frame. */
export function embedOfLine(line: string, page: string): Embed | null {
  const url = aloneUrl(line);
  if (url) return embedFor(url, page);
  return rawFrame(line, page);
}

/** A card's words before its preview comes (and when there is none): the host, and the rest of the address. */
export function cardFallback(url: string): { title: string; site: string } {
  try {
    const u = new URL(url);
    const path = decodeURI(u.pathname + u.search).replace(/\/$/, '');
    const host = u.hostname.replace(/^www\./, '');
    return { title: host + path, site: host };
  } catch {
    return { title: url, site: '' };
  }
}
