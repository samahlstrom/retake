// The agent's words in the chat are Markdown (spec/chat.md). This renders the part a chat reply uses, as HTML that
// escapes everything else: paragraphs with their line breaks, headings (as bold lines), lists, quotes, code, rules,
// bold, italic, links (opened in a new tab) and images. Images and links resolve through `img` and stay on http(s)
// or the video's own files; anything else stays text.

const esc = (s: string): string =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');

/** A link's address when it is one to follow: http(s) or mailto. */
export function safeHref(href: string): string | null {
  const h = href.trim();
  return /^(https?:\/\/|mailto:)/i.test(h) ? h : null;
}

const INLINE = new RegExp(
  [
    '`([^`]+)`', // 1 code
    '!\\[([^\\]]*)\\]\\(([^)\\s]+)(?:\\s+"[^"]*")?\\)', // 2 alt, 3 src
    '\\[([^\\]]+)\\]\\(([^)\\s]+)(?:\\s+"[^"]*")?\\)', // 4 text, 5 href
    '\\*\\*(?=\\S)([^*]+?)\\*\\*', // 6 bold
    '(?<![\\w])__(?=\\S)([^_]+?)__(?![\\w])', // 7 bold
    '\\*(?=[^*\\s])([^*]+?)\\*', // 8 italic
    '(?<![\\w])_(?=[^_\\s])([^_]+?)_(?![\\w])', // 9 italic
    '(https?:\\/\\/[^\\s<>()]+[^\\s<>().,;:!?\'"])', // 10 a bare address
  ].join('|'),
  'g',
);

function inline(s: string, img: (src: string) => string | null): string {
  let out = '',
    last = 0;
  INLINE.lastIndex = 0;
  const parts: RegExpExecArray[] = [];
  for (let m = INLINE.exec(s); m; m = INLINE.exec(s)) parts.push(m);
  for (const m of parts) {
    out += esc(s.slice(last, m.index));
    last = m.index + m[0].length;
    if (m[1] != null) out += `<code>${esc(m[1])}</code>`;
    else if (m[3] != null) {
      const src = img(m[3]);
      out += src ? `<img src="${esc(src)}" alt="${esc(m[2] ?? '')}" loading="lazy">` : esc(m[0]);
    } else if (m[5] != null) {
      const href = safeHref(m[5]);
      out += href
        ? `<a href="${esc(href)}" target="_blank" rel="noopener noreferrer">${inline(m[4]!, img)}</a>`
        : inline(m[4]!, img);
    } else if (m[6] != null || m[7] != null) out += `<strong>${inline((m[6] ?? m[7])!, img)}</strong>`;
    else if (m[8] != null || m[9] != null) out += `<em>${inline((m[8] ?? m[9])!, img)}</em>`;
    else if (m[10] != null)
      out += `<a href="${esc(m[10])}" target="_blank" rel="noopener noreferrer">${esc(m[10])}</a>`;
  }
  return out + esc(s.slice(last));
}

const LIST = /^(\s*)([-*+]|\d{1,3}[.)])\s+(.*)$/;
const BLOCK_START = /^(```|#{1,6}\s|>|(\s*)([-*+]|\d{1,3}[.)])\s+|(-{3,}|\*{3,}|_{3,})\s*$)/;

/** Markdown as HTML (see above). */
export function renderMarkdown(text: string, img: (src: string) => string | null = () => null): string {
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const out: string[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    if (!line.trim()) {
      i++;
      continue;
    }
    if (line.startsWith('```')) {
      const code: string[] = [];
      for (i++; i < lines.length && !lines[i]!.startsWith('```'); i++) code.push(lines[i]!);
      i++;
      out.push(`<pre><code>${esc(code.join('\n'))}</code></pre>`);
      continue;
    }
    const h = /^#{1,6}\s+(.*)$/.exec(line);
    if (h) {
      out.push(`<p class="h"><strong>${inline(h[1]!.replace(/\s+#+\s*$/, ''), img)}</strong></p>`);
      i++;
      continue;
    }
    if (/^(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      out.push('<hr>');
      i++;
      continue;
    }
    if (line.startsWith('>')) {
      const q: string[] = [];
      for (; i < lines.length && lines[i]!.startsWith('>'); i++) q.push(lines[i]!.replace(/^>\s?/, ''));
      out.push(`<blockquote>${renderMarkdown(q.join('\n'), img)}</blockquote>`);
      continue;
    }
    const first = LIST.exec(line);
    if (first) {
      const ordered = /\d/.test(first[2]!);
      const items: string[] = [];
      while (i < lines.length) {
        const m = LIST.exec(lines[i]!);
        if (m && /\d/.test(m[2]!) === ordered) {
          items.push(m[3]!);
          i++;
        } else if (items.length && lines[i]!.trim() && /^\s{2,}/.test(lines[i]!) && !LIST.test(lines[i]!)) {
          items[items.length - 1] += `\n${lines[i]!.trim()}`; // a wrapped item
          i++;
        } else break;
      }
      const tag = ordered ? 'ol' : 'ul';
      out.push(
        `<${tag}>${items
          .map(
            (it) =>
              `<li>${it
                .split('\n')
                .map((l) => inline(l, img))
                .join('<br>')}</li>`,
          )
          .join('')}</${tag}>`,
      );
      continue;
    }
    const para: string[] = [];
    for (; i < lines.length && lines[i]!.trim() && (!para.length || !BLOCK_START.test(lines[i]!)); i++)
      para.push(lines[i]!);
    out.push(`<p>${para.map((l) => inline(l, img)).join('<br>')}</p>`);
  }
  return out.join('');
}
