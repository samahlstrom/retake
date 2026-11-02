// The live preview's decorations (spec/scripts.md, "The editor"): what's hidden off and on the caret's lines, the
// headings' classes, the links, lists and task boxes, images, and the agent's questions under their places.
import { describe, expect, it } from 'vitest';
import { EditorState } from '@codemirror/state';
import { LanguageSupport } from '@codemirror/language';
import { markdownLanguage } from '@codemirror/lang-markdown';
import { decorationsOf, livePreview, setFocus, setQuestions, toggleTask, type LiveConfig, type Question } from './live';

function state(doc: string, caret: number, focus = true, cfg: LiveConfig = {}): EditorState {
  const s = EditorState.create({
    doc,
    selection: { anchor: caret },
    extensions: [new LanguageSupport(markdownLanguage), livePreview({ image: (src) => `u/${src}`, ...cfg })],
  });
  return focus ? s.update({ effects: setFocus.of(true) }).state : s;
}

interface Seen {
  hidden: [number, number][];
  widgets: { from: number; to: number; block: boolean; name: string }[];
  marks: { cls: string; from: number; to: number; href?: string }[];
  lines: Map<number, string>;
}

function seen(s: EditorState): Seen {
  const out: Seen = { hidden: [], widgets: [], marks: [], lines: new Map() };
  decorationsOf(s).between(0, s.doc.length, (from, to, d) => {
    const spec = d.spec as {
      widget?: { constructor: { name: string } };
      block?: boolean;
      class?: string;
      attributes?: Record<string, string>;
    };
    if (spec.widget) out.widgets.push({ from, to, block: !!spec.block, name: spec.widget.constructor.name });
    else if (from === to && spec.class) {
      const n = s.doc.lineAt(from).number;
      out.lines.set(n, `${out.lines.get(n) ?? ''} ${spec.class}`.trim());
    } else if (spec.class) out.marks.push({ cls: spec.class, from, to, href: spec.attributes?.['data-href'] });
    else out.hidden.push([from, to]);
  });
  return out;
}

const text = (s: EditorState, r: [number, number][]) => r.map(([a, b]) => s.sliceDoc(a, b));

describe('headings', () => {
  const doc = '# Title\n\nBody text\n\n## Cold open ##\n';
  it('are drawn at their level, their #s hidden off the caret line', () => {
    const s = state(doc, doc.indexOf('Body'));
    const v = seen(s);
    expect(v.lines.get(1)).toBe('ig-h ig-h1');
    expect(v.lines.get(5)).toBe('ig-h ig-h2');
    expect(v.lines.has(3)).toBe(false);
    expect(text(s, v.hidden)).toEqual(['# ', '## ', ' ##']);
  });
  it('show their #s on the caret line, dim', () => {
    const s = state(doc, 3);
    const v = seen(s);
    expect(text(s, v.hidden)).toEqual(['## ', ' ##']);
    expect(v.marks.filter((m) => m.cls === 'ig-mk').map((m) => s.sliceDoc(m.from, m.to))).toEqual(['#']);
  });
  it('hide every mark while the text has no focus', () => {
    const s = state(doc, 3, false);
    expect(text(s, seen(s).hidden)).toEqual(['# ', '## ', ' ##']);
  });
  it('a selection over lines shows all of them', () => {
    let s = state(doc, 0);
    s = s.update({ selection: { anchor: 0, head: doc.length } }).state;
    expect(seen(s).hidden).toEqual([]);
  });
});

describe('inline', () => {
  const doc = 'Some **bold**, *it*, `code` and [site](https://example.com "t").\n\nNext';
  it('hides the marks and a link’s brackets and address off the caret line', () => {
    const s = state(doc, doc.length);
    const v = seen(s);
    expect(text(s, v.hidden)).toEqual(['**', '**', '*', '*', '`', '`', '[', '](https://example.com "t")']);
    const cls = (c: string) => v.marks.filter((m) => m.cls === c).map((m) => s.sliceDoc(m.from, m.to));
    expect(cls('ig-b')).toEqual(['**bold**']);
    expect(cls('ig-i')).toEqual(['*it*']);
    expect(cls('ig-code')).toEqual(['code']);
    const a = v.marks.find((m) => m.cls === 'ig-a')!;
    expect(s.sliceDoc(a.from, a.to)).toBe('site');
    expect(a.href).toBe('https://example.com');
  });
  it('shows them on the caret line, where a link is text to edit', () => {
    const s = state(doc, 2);
    const v = seen(s);
    expect(v.hidden).toEqual([]);
    expect(v.marks.filter((m) => m.cls === 'ig-mk').map((m) => s.sliceDoc(m.from, m.to))).toContain('[');
  });
  it('never makes a link of javascript: or a relative path', () => {
    for (const url of ['javascript:alert(1)', 'notes.md']) {
      const d = `[x](${url})\n\nNext`;
      const s = state(d, d.length);
      expect(seen(s).marks.find((m) => m.cls === 'ig-a')?.href).toBeUndefined();
    }
  });
  it('leaves a bare [picture or sound] of a script as typed', () => {
    const d = '[The jump.] So the idea is the jump.\n\nNext';
    const s = state(d, d.length);
    expect(seen(s).hidden).toEqual([]);
    expect(seen(s).marks.filter((m) => m.cls === 'ig-a')).toEqual([]);
    expect(seen(s).marks.find((m) => m.cls === 'ig-cue')).toMatchObject({ from: 0, to: 11 });
  });
  it('links a bare address', () => {
    const s = state('See https://example.com/a now\n\nNext', 35);
    expect(seen(s).marks.find((m) => m.cls === 'ig-a')?.href).toBe('https://example.com/a');
  });
});

describe('lists and tasks', () => {
  const doc = '- one\n  - two\n1. first\n- [ ] todo\n- [x] done\n\nEnd';
  it('put a mark widget in the hanging indent off the caret line, with the depth on the line', () => {
    const s = state(doc, doc.length);
    const v = seen(s);
    expect(v.widgets.map((w) => [s.sliceDoc(w.from, w.to), w.name])).toEqual([
      ['- ', 'ListMark'],
      ['  - ', 'ListMark'],
      ['1. ', 'ListMark'],
      ['- [ ] ', 'TaskBox'],
      ['- [x] ', 'TaskBox'],
    ]);
    expect(v.lines.get(1)).toBe('ig-li ig-d0 ig-li-first');
    expect(v.lines.get(2)).toBe('ig-li ig-d1 ig-li-first');
    expect(v.lines.get(5)).toBe('ig-li ig-d0 ig-li-first ig-done');
  });
  it('start with a bold word and still get their mark (an outline: "1. **Cold open** (0:00–0:08): …")', () => {
    const d = '1. **Cold open** (0:00–0:08): the list\n2. **Retake** lands\n- **Flip** fast\n\nEnd';
    const s = state(d, d.length);
    const v = seen(s);
    expect(v.widgets.map((w) => [s.sliceDoc(w.from, w.to), w.name])).toEqual([
      ['1. ', 'ListMark'],
      ['2. ', 'ListMark'],
      ['- ', 'ListMark'],
    ]);
    expect(text(s, v.hidden)).toEqual(['**', '**', '**', '**', '**', '**']);
  });
  it('show the raw marks on the caret line', () => {
    const s = state(doc, doc.indexOf('todo'));
    const v = seen(s);
    expect(v.widgets.map((w) => s.sliceDoc(w.from, w.to))).not.toContain('- [ ] ');
    expect(v.marks.filter((m) => m.cls === 'ig-mk').map((m) => s.sliceDoc(m.from, m.to))).toEqual(['-', '[ ]']);
  });
  it('a tap on a box changes exactly the character between its brackets', () => {
    const s = state(doc, 0);
    const line = s.doc.line(4);
    const c = toggleTask(s, line.from)!;
    expect(c).toEqual({ from: line.from + 3, to: line.from + 4, insert: 'x' });
    const ticked = s.update({ changes: c }).state;
    expect(ticked.doc.toString()).toBe(doc.replace('- [ ] todo', '- [x] todo'));
    const back = ticked.update({ changes: toggleTask(ticked, line.from)! }).state;
    expect(back.doc.toString()).toBe(doc);
    expect(toggleTask(s, 0)).toBeNull(); // "- one" has no box
    // A quoted task, an upper-case X.
    const q = state('> - [X] quoted', 0);
    expect(q.update({ changes: toggleTask(q, 0)! }).state.doc.toString()).toBe('> - [ ] quoted');
  });
});

describe('blocks', () => {
  it('quotes: a line class, and > hidden off the caret line', () => {
    const s = state('> said\n> more\n\nEnd', 18);
    const v = seen(s);
    expect(v.lines.get(1)).toBe('ig-quote');
    expect(text(s, v.hidden)).toEqual(['> ', '> ']);
  });
  it('a rule is a line, its --- hidden', () => {
    const s = state('Above\n\n---\n\nBelow', 0);
    const v = seen(s);
    expect(v.lines.get(3)).toBe('ig-hr');
    expect(text(s, v.hidden)).toEqual(['---']);
  });
  it('code fences: a box, the fences hidden off their lines', () => {
    const s = state('```js\nlet a\n```\n\nEnd', 20);
    const v = seen(s);
    expect(v.lines.get(1)).toBe('ig-pre ig-pre-top');
    expect(v.lines.get(3)).toBe('ig-pre ig-pre-end');
    expect(text(s, v.hidden)).toEqual(['```', 'js', '```']);
  });
});

describe('images', () => {
  const doc = 'Intro\n\n![The shop](images/shop.png)\n\nEnd';
  it('take the place of their Markdown off the caret line', () => {
    const s = state(doc, 0);
    const w = seen(s).widgets;
    expect(w).toHaveLength(1);
    expect(w[0]).toMatchObject({ name: 'Picture', block: false });
    expect(s.sliceDoc(w[0]!.from, w[0]!.to)).toBe('![The shop](images/shop.png)');
  });
  it('show under their Markdown on the caret line', () => {
    const s = state(doc, doc.indexOf('shop'));
    const w = seen(s).widgets;
    expect(w).toEqual([{ from: s.doc.line(3).to, to: s.doc.line(3).to, block: true, name: 'Picture' }]);
  });
  it('stay Markdown when they can’t show', () => {
    const s = state(doc, 0, true, { image: () => null });
    expect(seen(s).widgets).toEqual([]);
  });
});

describe('questions', () => {
  it('show under their place: the heading, or the paragraph of the quote', () => {
    const doc = '# T\n\n## Cold open\n\nThe jump,\nthen the shop.\n\n## The trick\n';
    const q = (id: number, anchor: Question['anchor']): Question => ({
      id,
      note: 1,
      text: 'Which?',
      anchor,
      options: [],
    });
    const s = state(doc, 0).update({
      effects: setQuestions.of([
        q(7, { line: 3, heading: 'Cold open' }),
        q(8, { line: 5, heading: 'Cold open', quote: 'The jump, then the shop.' }),
      ]),
    }).state;
    const w = seen(s).widgets.filter((x) => x.name === 'Ask');
    expect(w.map((x) => [s.doc.lineAt(x.from).number, x.block])).toEqual([
      [3, true],
      [6, true],
    ]);
  });
});

describe('embeds', () => {
  const doc = [
    '# Refs', // 1
    '', // 2
    'https://youtu.be/dQw4w9WgXcQ', // 3: a player
    '', // 4
    'See https://example.com/inline for more.', // 5: a link in a sentence
    '', // 6
    '<https://example.com/a-page>', // 7: a card
    '', // 8
    '```', // 9
    'https://youtu.be/dQw4w9WgXcQ', // 10: code
    '```', // 11
    '', // 12
    '<iframe src="https://player.example/x" width="4" height="3"></iframe>', // 13: a raw frame
    '', // 14
    '    https://example.com/indented-code', // 15: code
    '',
  ].join('\n');
  type EmbedSpec = { kind: string; src?: string; url?: string; ratio?: number };
  function embeds(s: EditorState): { line: number; e: EmbedSpec }[] {
    const out: { line: number; e: EmbedSpec }[] = [];
    decorationsOf(s).between(0, s.doc.length, (from, _to, d) => {
      const w = (d.spec as { widget?: { constructor: { name: string }; e?: EmbedSpec } }).widget;
      if (w?.constructor.name === 'EmbedView') out.push({ line: s.doc.lineAt(from).number, e: w.e! });
    });
    return out;
  }

  it('an address alone on its line, or one raw frame, embeds under it; a link in a sentence and code don’t', () => {
    const s = state(doc, 0, true, { page: 'retake.example' });
    expect(embeds(s)).toEqual([
      {
        line: 3,
        e: expect.objectContaining({ kind: 'frame', src: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ' }),
      },
      { line: 7, e: { kind: 'card', url: 'https://example.com/a-page' } },
      { line: 13, e: expect.objectContaining({ kind: 'frame', src: 'https://player.example/x', ratio: 4 / 3 }) },
    ]);
    const v = seen(s);
    const blocks = v.widgets.filter((w) => w.name === 'EmbedView');
    expect(blocks.every((w) => w.block && w.from === s.doc.line(s.doc.lineAt(w.from).number).to)).toBe(true);
    // Off the caret's line, the line folds away: only its embed shows.
    expect([3, 7, 13].map((n) => v.lines.get(n))).toEqual(['ig-embed-src', 'ig-embed-src', 'ig-embed-src']);
    expect(v.lines.get(5)).toBeUndefined();
  });

  it('on the caret’s line the address shows as typed, with the same embed under it', () => {
    const at = doc.indexOf('https://youtu.be');
    const s = state(doc, at + 3);
    const v = seen(s);
    expect(v.lines.get(3)).toBeUndefined();
    expect(v.lines.get(7)).toBe('ig-embed-src');
    expect(embeds(s).map((x) => x.line)).toEqual([3, 7, 13]);
    // The widget is equal across the caret coming and going, so its element (a playing video) stays.
    const widget = (st: EditorState) => {
      let w: { eq(o: unknown): boolean } | undefined;
      decorationsOf(st).between(at, at + 40, (_f, _t, d) => {
        const x = (d.spec as { widget?: { constructor: { name: string }; eq(o: unknown): boolean } }).widget;
        if (x?.constructor.name === 'EmbedView') w = x;
      });
      return w!;
    };
    expect(widget(s).eq(widget(state(doc, 0)))).toBe(true);
  });

  it('a frame of this page’s own host stays text', () => {
    const own = '<iframe src="https://retake.example/p/x/"></iframe>\n';
    expect(embeds(state(own, own.length, true, { page: 'retake.example' }))).toEqual([]);
  });
});
