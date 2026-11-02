// The documents are plain Markdown (spec/ignition.md): headings are `#` lines, and that's all Ignition reads into them.
// Pure text logic: headings, where an anchor is, the talk-out block, and keeping the caret when the text changes.
import type { Anchor } from '../types';

export interface Heading {
  /** From 0. */
  line: number;
  level: number;
  text: string;
}

/** A change to a text: `del` characters at `at` replaced by `ins`. */
export interface Splice {
  at: number;
  del: number;
  ins: string;
}

const HEADING = /^(#{1,6})(?:[ \t]+(.*?))?[ \t]*$/;

/** A heading line's level and text ("## Cold open ##" is 2, "Cold open"), or null. */
export function headingOf(line: string): { level: number; text: string } | null {
  const m = HEADING.exec(line);
  if (!m) return null;
  return {
    level: m[1]!.length,
    text: (m[2] ?? '')
      .replace(/[ \t]+#+$/, '')
      .replace(/^#+$/, '')
      .trim(),
  };
}

export function headings(text: string): Heading[] {
  const out: Heading[] = [];
  text.split('\n').forEach((l, line) => {
    const h = headingOf(l);
    if (h) out.push({ line, ...h });
  });
  return out;
}

/** The line (from 0) a position is on. */
export function lineOf(text: string, pos: number): number {
  let n = 0;
  const end = Math.min(Math.max(0, pos), text.length);
  for (let i = text.indexOf('\n'); i !== -1 && i < end; i = text.indexOf('\n', i + 1)) n++;
  return n;
}

/** Where a line (from 0) starts; past the end, the text's length. */
export function lineStart(text: string, line: number): number {
  let pos = 0;
  for (let i = 0; i < line; i++) {
    const nl = text.indexOf('\n', pos);
    if (nl === -1) return text.length;
    pos = nl + 1;
  }
  return pos;
}

/** The heading the caret is under: the nearest heading line at or above it, or null. */
export function headingAbove(text: string, pos: number): Heading | null {
  const line = lineOf(text, pos);
  let best: Heading | null = null;
  for (const h of headings(text)) {
    if (h.line > line) break;
    best = h;
  }
  return best;
}

const blank = (l: string | undefined): boolean => l === undefined || l.trim() === '';
const squash = (s: string): string => s.replace(/\s+/g, ' ').trim();
const key = (s: string): string => squash(s).toLowerCase();

/** The paragraph around a line, as [first, last] lines: the non-blank lines next to it, never across a heading. A
 * heading is a paragraph of its own; a blank line has none. */
export function paragraph(lines: string[], line: number): [number, number] | null {
  if (blank(lines[line])) return null;
  if (headingOf(lines[line]!)) return [line, line];
  let a = line,
    b = line;
  while (a > 0 && !blank(lines[a - 1]) && !headingOf(lines[a - 1]!)) a--;
  while (b < lines.length - 1 && !blank(lines[b + 1]) && !headingOf(lines[b + 1]!)) b++;
  return [a, b];
}

/** A comment's anchor at the caret (spec/extensions.md, "Typed comments"): the line, the heading above it, and up to
 * 200 characters of the caret's paragraph (none on a heading or a blank line). */
export function anchorAt(file: string, text: string, pos: number): Anchor {
  const lines = text.split('\n');
  const line = lineOf(text, pos);
  const a: Anchor = { file, line: line + 1 };
  const h = headingAbove(text, pos);
  if (h?.text) a.heading = h.text;
  const p = paragraph(lines, line);
  if (p && !headingOf(lines[p[0]]!)) {
    const quote = squash(lines.slice(p[0], p[1] + 1).join(' ')).slice(0, 200);
    if (quote) a.quote = quote;
  }
  return a;
}

/** The first line (from 0) of the paragraph in [from, to) that holds the quote, or -1. A quote that no longer matches
 * as a whole is tried by its first 40 characters. */
function findQuote(lines: string[], quote: string, from: number, to: number): number {
  const q = key(quote);
  if (!q) return -1;
  const tries = q.length > 40 ? [q, q.slice(0, 40)] : [q];
  for (const t of tries) {
    for (let i = from; i < to; i++) {
      const p = paragraph(lines, i);
      if (!p) continue;
      if (key(lines.slice(p[0], p[1] + 1).join(' ')).includes(t)) return p[0];
      i = Math.max(i, p[1]);
    }
  }
  return -1;
}

/** Where an anchor is now (a line from 0): by its heading (and inside that section by its quote), then by its quote
 * anywhere, then by its line. -1 for an empty text. */
export function findAnchor(text: string, a: Pick<Anchor, 'line' | 'heading' | 'quote'>): number {
  if (!text) return -1;
  const lines = text.split('\n');
  if (a.heading) {
    const all = headings(text);
    const i = all.findIndex((h) => key(h.text) === key(a.heading!));
    if (i !== -1) {
      const h = all[i]!;
      if (a.quote) {
        const next = all.slice(i + 1).find((x) => x.level <= h.level);
        const q = findQuote(lines, a.quote, h.line, next ? next.line : lines.length);
        if (q !== -1) return q;
      }
      return h.line;
    }
  }
  if (a.quote) {
    const q = findQuote(lines, a.quote, 0, lines.length);
    if (q !== -1) return q;
  }
  const line = Number.isFinite(a.line) ? Math.floor(a.line) - 1 : 0;
  return Math.min(Math.max(0, line), lines.length - 1);
}

// ---------------------------------------------------------------- talk-outs

/** Where a talk-out stands while its words are on the way. */
export const TRANSCRIBING = '(transcribing…)';
const MARKER = /^<!--\s*talk-out\s+(\S+)\s*-->\s*$/;

/** The marker line of a talk-out: `<!-- talk-out voice/20261101-190211.webm -->`. */
export const marker = (rel: string): string => `<!-- talk-out ${rel} -->`;

/** A talk-out as it goes into a document: the marker line, then its words. */
export const talkOutBlock = (rel: string, body: string): string => `${marker(rel)}\n${body.trim()}`;

/** The talk-out a line marks (`voice/….webm`), or null. */
export function talkOutOf(line: string): string | null {
  return MARKER.exec(line)?.[1] ?? null;
}

/**
 * Puts a block in at the caret as a paragraph of its own: on the caret's line when it's blank, else after it, with a
 * blank line on each side. Returns the change, and the caret after the block.
 */
export function insertBlock(text: string, pos: number, block: string): { text: string; splice: Splice; caret: number } {
  const line = lineOf(text, pos);
  const lines = text.split('\n');
  const start = lineStart(text, line);
  const at = blank(lines[line]) ? start : start + lines[line]!.length;
  const pre = text.slice(0, at),
    post = text.slice(at);
  const lead = pre === '' || pre.endsWith('\n\n') ? '' : pre.endsWith('\n') ? '\n' : '\n\n';
  const tail =
    post.trim() === ''
      ? post.startsWith('\n')
        ? ''
        : '\n'
      : post.startsWith('\n\n')
        ? ''
        : post.startsWith('\n')
          ? '\n'
          : '\n\n';
  const ins = `${lead}${block}${tail}`;
  return { text: pre + ins + post, splice: { at, del: 0, ins }, caret: at + lead.length + block.length };
}

/** The lines of a talk-out's words: those after its marker, up to a blank line, a heading or another marker. */
function talkOutLines(lines: string[], rel: string): [number, number] | null {
  const m = lines.findIndex((l) => talkOutOf(l) === rel);
  if (m === -1) return null;
  let end = m + 1;
  while (end < lines.length && !blank(lines[end]) && !headingOf(lines[end]!) && !talkOutOf(lines[end]!)) end++;
  return [m + 1, end];
}

/** The line (from 0) of a talk-out's marker, or -1. */
export const talkOutLine = (text: string, rel: string): number =>
  text.split('\n').findIndex((l) => talkOutOf(l) === rel);

/**
 * Puts a talk-out's words in place of its placeholder. null when there's nothing to do: the marker is gone (someone
 * removed it) or the words under it aren't the placeholder any more (someone wrote there).
 */
export function fillTalkOut(text: string, rel: string, body: string): { text: string; splice: Splice } | null {
  const lines = text.split('\n');
  const r = talkOutLines(lines, rel);
  if (!r) return null;
  const old = lines.slice(r[0], r[1]).join('\n');
  if (old.trim() !== TRANSCRIBING) return null;
  const at = lineStart(text, r[0]);
  const ins = body.trim();
  return { text: text.slice(0, at) + ins + text.slice(at + old.length), splice: { at, del: old.length, ins } };
}

/** The talk-outs of a text still waiting for their words (their placeholder is there), by their audio's path. */
export function waitingTalkOuts(text: string): string[] {
  const lines = text.split('\n');
  const out: string[] = [];
  lines.forEach((l, i) => {
    const rel = talkOutOf(l);
    if (rel && lines[i + 1]?.trim() === TRANSCRIBING) out.push(rel);
  });
  return out;
}

/** A words file's text: its words joined, or "" (spec/extensions.md: `{model, words: [{w, s, e, p}]}`). */
export function wordsText(body: unknown): string {
  const words = (body as { words?: unknown } | null)?.words;
  if (!Array.isArray(words)) return '';
  return words
    .map((w) => (w && typeof (w as { w?: unknown }).w === 'string' ? (w as { w: string }).w : ''))
    .join(' ')
    .replace(/\s+/g, ' ')
    .replace(/\s+([,.!?;:])/g, '$1')
    .trim();
}

// ---------------------------------------------------------------- the caret

/** Where the caret goes after a splice: before it, it stays; after it, it moves with the text; inside it, to its end. */
export function mapCaret(caret: number, s: Splice): number {
  if (caret <= s.at) return caret;
  if (caret >= s.at + s.del) return caret + s.ins.length - s.del;
  return s.at + s.ins.length;
}

/** The caret on the same line and column of a new text (clamped): how a reload in place keeps your place. */
export function keepLine(oldText: string, caret: number, newText: string): number {
  const line = lineOf(oldText, caret);
  const col = caret - lineStart(oldText, line);
  const lines = newText.split('\n');
  const l = Math.min(line, lines.length - 1);
  return lineStart(newText, l) + Math.min(col, lines[l]!.length);
}

// ---------------------------------------------------------------- an idea's title and first lines

/** The `# ` line of idea.md, or "". */
export function titleOf(md: string): string {
  for (const l of md.split('\n')) {
    const h = headingOf(l);
    if (h?.level === 1 && h.text) return h.text;
  }
  return '';
}

/** The first lines of an idea, for its row: not blank, not its title, not a talk-out marker or other HTML comment. */
export function firstLines(md: string, n = 2): string[] {
  const out: string[] = [];
  let titled = false;
  for (const l of md.split('\n')) {
    const t = l.trim();
    if (!t || /^<!--.*-->$/.test(t)) continue;
    const h = headingOf(t);
    if (h?.level === 1 && !titled) {
      titled = true;
      continue;
    }
    out.push(h ? h.text : t);
    if (out.length === n) break;
  }
  return out;
}

// ---------------------------------------------------------------- the script's sections

export interface Section {
  /** The `##` heading's text, or null for what comes before the first one. */
  heading: string | null;
  /** Lines [start, end) from 0; the heading line is the first. */
  start: number;
  end: number;
  lines: string[];
}

/** A script cut at its `##` headings (deeper headings stay inside their section). */
export function sections(text: string): Section[] {
  const lines = text.split('\n');
  const out: Section[] = [];
  let cur: Section = { heading: null, start: 0, end: 0, lines: [] };
  lines.forEach((l, i) => {
    const h = headingOf(l);
    if (h?.level === 2) {
      cur.end = i;
      if (cur.heading !== null || cur.lines.some((x) => x.trim())) out.push(cur);
      cur = { heading: h.text, start: i, end: i, lines: [] };
    }
    cur.lines.push(l);
  });
  cur.end = lines.length;
  if (cur.heading !== null || cur.lines.some((x) => x.trim())) out.push(cur);
  return out;
}

/** Lines as read-only blocks: paragraphs (their lines kept) and headings, without blank lines or HTML comments (the
 * talk-out markers), which rendered Markdown doesn't show either. */
export function blocks(lines: string[]): { heading: boolean; text: string }[] {
  const out: { heading: boolean; text: string }[] = [];
  let para: string[] = [];
  const end = () => {
    if (para.length) out.push({ heading: false, text: para.join('\n') });
    para = [];
  };
  for (const l of lines) {
    const t = l.trim();
    const h = headingOf(t);
    if (!t) end();
    else if (/^<!--.*-->$/.test(t)) continue;
    else if (h) {
      end();
      if (h.text) out.push({ heading: true, text: h.text });
    } else para.push(l);
  }
  end();
  return out;
}
