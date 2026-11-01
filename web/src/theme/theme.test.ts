// The Theme API's lint (spec/theme.md): every colour and font of the page comes from a token, every built-in rule is in
// @layer retake (so a theme wins without !important), every var() names a token, and the data-part hooks are exactly
// the documented ones. A new hard-coded colour anywhere but tokens.css fails here.
import { describe, expect, it } from 'vitest';
import tokensCss from './tokens.css?raw';
import apiJson from './api.json?raw';
import { DEFAULT_PALETTE, PALETTE_TOKENS } from '../lib/theme';

const sources = import.meta.glob(['/src/**/*.css', '/src/**/*.svelte', '/src/**/*.ts', '!/src/**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;
/** Paths as the test names them: src/app.css, src/components/Bar.svelte. */
const files = Object.fromEntries(Object.entries(sources).map(([k, v]) => [k.replace(/^\//, ''), v]));

/** api.json: the parts, and the names a major release deprecated. */
const api = JSON.parse(apiJson) as {
  api: number;
  parts: Record<string, string>;
  deprecated: { tokens: Record<string, unknown>; parts: Record<string, unknown> };
};
const TOKEN_FILE = 'src/theme/tokens.css';
/**
 * Files allowed colour literals, and why. Keep this short.
 * - constants.ts: the pen colours are review data (the agent reads them as words), not the look.
 * - lib/theme.ts: the canvas defaults; the last test below keeps them equal to tokens.css.
 * - Sketch.svelte: the comment card's drawing thumbnail, which another change owns this round. TODO: tokenize its
 *   frame (border, background) and put its style in @layer retake, then take it off this list.
 */
const COLOUR_OK = new Set([TOKEN_FILE, 'src/lib/constants.ts', 'src/lib/theme.ts']);
const NOT_THEMED_YET = new Set(['src/components/sidebar/Sketch.svelte']);
/** Custom properties that aren't tokens: a stage's aspect ratio, and Bits UI's own. */
const INTERNAL_VAR = /^--(ar|bits-[a-z-]+)$/;

const NAMED =
  'white|black|red|green|blue|yellow|orange|purple|pink|gr[ae]y|silver|navy|teal|maroon|olive|lime|aqua|fuchsia|gold|' +
  'violet|indigo|brown|crimson|coral|salmon|tomato|khaki|ivory|beige|lavender|plum|orchid|tan|wheat|cyan|magenta';
const COLOUR = new RegExp(
  String.raw`#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(|(?<![\w-])(?:${NAMED})(?![\w-])`,
  'i',
);

const stripComments = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, '');
/** A .svelte file's style block, or "". */
const styleOf = (svelte: string) => /<style>([\s\S]*?)<\/style>/.exec(svelte)?.[1] ?? '';
/** A .svelte file's markup: without its script and style. */
const markupOf = (svelte: string) =>
  svelte.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<style>[\s\S]*?<\/style>/g, '');
const scriptOf = (svelte: string) =>
  [...svelte.matchAll(/<script[^>]*>([\s\S]*?)<\/script>/g)].map((m) => m[1]).join('\n');

/** Every declaration of a stylesheet: property and value, data: URLs left out. */
function declarations(css: string): { prop: string; value: string }[] {
  const out: { prop: string; value: string }[] = [];
  for (const m of stripComments(css).matchAll(/([a-z-]+)\s*:\s*([^;{}]+?)\s*(?=[;}])/gi)) {
    out.push({ prop: m[1]!.toLowerCase(), value: m[2]!.replace(/url\((["']?)data:[\s\S]*?\1\)/g, 'url()') });
  }
  return out;
}

/** Each built-in stylesheet: the CSS files and the components' style blocks. */
const sheets = Object.entries(files).flatMap(([path, text]) =>
  path.endsWith('.css')
    ? [{ path, css: text }]
    : path.endsWith('.svelte') && styleOf(text)
      ? [{ path, css: styleOf(text) }]
      : [],
);
const tokens = new Map([...tokensCss.matchAll(/^\s*(--[a-z0-9-]+):\s*([^;]+);/gm)].map((m) => [m[1]!, m[2]!.trim()]));

describe('the tokens', () => {
  it('are defined once each, in tokens.css', () => {
    expect(tokens.size).toBeGreaterThan(40);
    for (const name of ['--bg', '--surface', '--text', '--muted', '--accent', '--record', '--agent', '--font-ui']) {
      expect(tokens.has(name), name).toBe(true);
    }
    for (const status of ['open', 'working', 'question', 'done']) expect(tokens.has(`--status-${status}`)).toBe(true);
  });
});

describe('no hard-coded look outside tokens.css', () => {
  it('has no colour literal in any stylesheet', () => {
    const found: string[] = [];
    for (const { path, css } of sheets) {
      if (COLOUR_OK.has(path) || NOT_THEMED_YET.has(path)) continue;
      for (const d of declarations(css)) {
        if (d.prop.startsWith('--')) found.push(`${path}: a custom property outside tokens.css: ${d.prop}`);
        else if (COLOUR.test(d.value)) found.push(`${path}: ${d.prop}: ${d.value}`);
      }
    }
    expect(found).toEqual([]);
  });

  it('has no colour literal in the markup or the scripts', () => {
    const found: string[] = [];
    for (const [path, text] of Object.entries(files)) {
      if (COLOUR_OK.has(path) || NOT_THEMED_YET.has(path)) continue;
      if (path.endsWith('.svelte')) {
        for (const m of markupOf(text).matchAll(
          /\b(fill|stroke|color|stop-color|style|style:[a-z-]+)=("[^"]*"|\{[^}]*\})/g,
        )) {
          if (COLOUR.test(m[2]!.replace(/currentColor|none/g, ''))) found.push(`${path}: ${m[0]}`);
        }
      }
      const script = path.endsWith('.svelte') ? scriptOf(text) : path.endsWith('.ts') ? text : '';
      for (const m of script.matchAll(/(['"`])((?:#[0-9a-f]{3,8}|(?:rgba?|hsla?)\([^'"`]*\)))\1/gi)) {
        found.push(`${path}: ${m[0]}`);
      }
    }
    expect(found).toEqual([]);
  });

  it('takes every font from --font-ui or --font-mono', () => {
    const found: string[] = [];
    for (const { path, css } of sheets) {
      if (path === TOKEN_FILE) continue;
      for (const d of declarations(css)) {
        if (d.prop !== 'font' && d.prop !== 'font-family') continue;
        if (d.value === 'inherit' || /var\(--font-(ui|mono)\)/.test(d.value)) continue;
        found.push(`${path}: ${d.prop}: ${d.value}`);
      }
    }
    for (const [path, text] of Object.entries(files)) {
      if (path.endsWith('.svelte') && /\bfont-family=/.test(markupOf(text)))
        found.push(`${path}: a font-family attribute`);
    }
    expect(found).toEqual([]);
  });

  it('names only tokens in var()', () => {
    const found: string[] = [];
    for (const { path, css } of [...sheets, { path: TOKEN_FILE, css: tokensCss }]) {
      for (const m of stripComments(css).matchAll(/var\((--[a-z0-9-]+)/gi)) {
        if (!tokens.has(m[1]!) && !INTERNAL_VAR.test(m[1]!)) found.push(`${path}: ${m[1]}`);
      }
    }
    expect(found).toEqual([]);
  });
});

describe('the cascade', () => {
  it('puts every built-in rule in @layer retake, so a theme wins without !important', () => {
    const outside: string[] = [];
    for (const { path, css } of sheets) {
      if (NOT_THEMED_YET.has(path)) continue;
      const body = stripComments(css).trim();
      if (!body.startsWith('@layer retake {') || !body.endsWith('}')) {
        outside.push(path);
        continue;
      }
      // The block that opens at the start closes at the very end: nothing after it.
      let depth = 0;
      for (let i = body.indexOf('{'); i < body.length; i++) {
        if (body[i] === '{') depth++;
        else if (body[i] === '}' && --depth === 0 && i !== body.length - 1) {
          outside.push(path);
          break;
        }
      }
    }
    expect(outside).toEqual([]);
  });
});

describe('the parts (data-part)', () => {
  const used = new Map<string, string[]>();
  for (const [path, text] of Object.entries(files)) {
    if (!path.endsWith('.svelte')) continue;
    for (const m of markupOf(text).matchAll(/data-part="([^"]*)"/g)) {
      for (const name of m[1]!.split(/\s+/).filter(Boolean)) used.set(name, [...(used.get(name) ?? []), path]);
    }
  }
  const documented = Object.keys(api.parts);

  it('are all documented in api.json', () => {
    expect([...used.keys()].filter((p) => !documented.includes(p))).toEqual([]);
  });
  it('are all still on the page (a documented part is only removed in a major release)', () => {
    const deprecated = Object.keys(api.deprecated.parts);
    expect(documented.filter((p) => !used.has(p) && !deprecated.includes(p))).toEqual([]);
  });
  it('cover what the spec promises', () => {
    for (const p of ['bar', 'play', 'timeline', 'marker', 'version-picker', 'pens', 'record', 'send', 'agent-status']) {
      expect(used.has(p), p).toBe(true);
    }
    for (const p of ['agent-menu', 'sidebar', 'tabs', 'comment', 'status', 'reply-box', 'question-card']) {
      expect(used.has(p), p).toBe(true);
    }
    for (const p of ['agent-visuals', 'home-grid', 'video-card', 'connect-card', 'toast', 'brand', 'logo']) {
      expect(used.has(p), p).toBe(true);
    }
  });
  it('are lower-case words with dashes', () => {
    for (const p of documented) expect(p).toMatch(/^[a-z]+(-[a-z]+)*$/);
  });
});

describe('the canvas colours', () => {
  /** rgba of a plain colour, a var() of a token, or color-mix(in srgb, X N%, transparent). */
  function rgba(value: string): number[] {
    const v = value.trim();
    const ref = /^var\((--[a-z0-9-]+)\)$/.exec(v);
    if (ref) return rgba(tokens.get(ref[1]!)!);
    const mix = /^color-mix\(in srgb, (.+) ([\d.]+)%, transparent\)$/.exec(v);
    if (mix) {
      const [r, g, b, a] = rgba(mix[1]!);
      return [r!, g!, b!, a! * (Number(mix[2]) / 100)];
    }
    const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(v);
    if (hex) {
      const h = hex[1]!.length === 3 ? [...hex[1]!].map((c) => c + c).join('') : hex[1]!;
      return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16)).concat(1);
    }
    const fn = /^rgba?\(([^)]*)\)$/.exec(v);
    if (fn) {
      const parts = fn[1]!.split(',').map(Number);
      return [parts[0]!, parts[1]!, parts[2]!, parts[3] ?? 1];
    }
    throw new Error(`can't read ${value}`);
  }

  it("are the tokens' built-in values", () => {
    for (const [key, token] of Object.entries(PALETTE_TOKENS)) {
      const want = rgba(DEFAULT_PALETTE[key as keyof typeof DEFAULT_PALETTE]);
      const got = rgba(tokens.get(token)!);
      expect(
        got.map((n) => +n.toFixed(3)),
        `${key} (${token})`,
      ).toEqual(want.map((n) => +n.toFixed(3)));
    }
  });
});
