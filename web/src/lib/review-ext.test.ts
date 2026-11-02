// The review page with extensions (spec/extensions.md): the review keys stay core's whatever an extension's slot has
// focus; an extension's kind of version (v0) never comes up by following the latest or comparing; and a talk-out on
// it goes to the extension.
import { describe, expect, it, vi } from 'vitest';
import appSource from '../App.svelte?raw';
import { Review } from './review.svelte';
import type { Action } from './keys';
import type { Segment, Version } from './types';
import type { Recorded } from '../ext/retake-ext';

const v = (label: string, extra: Partial<Version> = {}): Version => ({
  label,
  n: Number(label.slice(1)),
  path: label,
  name: label,
  current: false,
  duration: 10,
  fps: 30,
  width: 1920,
  height: 1080,
  ...extra,
});
const V0 = v('v0', { kind: 'ignition.doc', duration: null });

/** An element with focus: `closest` answers as the DOM would for the page's selectors. */
function focused(tag: 'button' | 'textarea' | 'div', o: { editable?: string; inSlot?: boolean } = {}) {
  const el = {
    tagName: tag.toUpperCase(),
    blur: vi.fn(),
    closest(sel: string): unknown {
      const parts = sel.split(',').map((p) => p.trim());
      if (parts.includes(tag) && tag !== 'div' && tag !== 'button') return el;
      if (
        o.editable !== undefined &&
        o.editable !== 'false' &&
        parts.includes('[contenteditable]:not([contenteditable=false])')
      )
        return el;
      if (o.inSlot && parts.some((p) => p.startsWith('[data-ext') || p.startsWith('[data-slot'))) return {};
      return null;
    },
  };
  return el;
}

function press(review: Review, key: string, target: unknown, mods: { ctrlKey?: boolean; repeat?: boolean } = {}) {
  const e = {
    key,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    repeat: false,
    ...mods,
    target,
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
  };
  review.onKeydown(e as unknown as KeyboardEvent);
  return e;
}

function page(versions: Version[], shown: string) {
  const review = new Review();
  const ran: Action[] = [];
  review.run = (act: Action) => void ran.push(act);
  review.versions = versions;
  review.shown = review.base = shown;
  return { review, ran };
}

describe('the review keys with an extension on the page', () => {
  it("core's handler is the window's first listener (capture), set by the page itself", () => {
    expect(appSource).toContain('<svelte:window onkeydowncapture={review.onKeydown}');
  });

  it("R and the review keys still reach core with an extension's sidebar tab focused", () => {
    const { review, ran } = page([v('v1'), v('v2')], 'v2');
    const tab = focused('button', { inSlot: true });
    const keys: [string, Action][] = [
      ['r', 'toggleRec'],
      [' ', 'togglePlay'],
      ['z', 'undo'],
      ['1', 'color1'],
      ['4', 'color4'],
      ['e', 'eraser'],
      ['ArrowLeft', 'back'],
      ['.', 'frameNext'],
      ['n', 'notes'],
      ['>', 'next'],
      ['?', 'help'],
    ];
    for (const [key, act] of keys) {
      const e = press(review, key, tab);
      expect(ran.at(-1), key).toBe(act);
      expect(e.preventDefault, key).toHaveBeenCalled();
      expect(e.stopPropagation, key).toHaveBeenCalled(); // the slot never sees them
    }
    expect(ran).toHaveLength(keys.length);
  });

  it("typing in an extension's text field is its own: no R, no Space (contenteditable of any kind)", () => {
    const { review, ran } = page([v('v1')], 'v1');
    for (const target of [
      focused('textarea', { inSlot: true }),
      focused('div', { editable: '', inSlot: true }),
      focused('div', { editable: 'plaintext-only', inSlot: true }),
    ]) {
      for (const key of ['r', ' ', 'z', '1']) {
        const e = press(review, key, target);
        expect(e.preventDefault).not.toHaveBeenCalled();
      }
    }
    expect(ran).toEqual([]);
    // contenteditable="false" is no text field
    press(review, 'r', focused('div', { editable: 'false', inSlot: true }));
    expect(ran).toEqual(['toggleRec']);
  });

  it("on an extension's version: R talks (outside text), Esc leaves the text, the video's keys are the page's", () => {
    const { review, ran } = page([V0, v('v1')], 'v0');
    expect(review.kindShown).toBe('ignition.doc');
    const button = focused('button', { inSlot: true });
    press(review, 'r', button);
    expect(ran).toEqual(['toggleRec']);
    for (const key of [' ', 'z', '1', 'e', 'ArrowLeft', ',', 'f']) {
      const e = press(review, key, button);
      expect(e.preventDefault, key).not.toHaveBeenCalled();
      expect(e.stopPropagation, key).not.toHaveBeenCalled();
    }
    press(review, ' ', button, { repeat: true });
    expect(ran).toEqual(['toggleRec']);
    press(review, 'n', button);
    press(review, '?', button);
    expect(ran).toEqual(['toggleRec', 'notes', 'help']);
    const text = focused('textarea', { inSlot: true });
    expect(press(review, 'r', text).preventDefault).not.toHaveBeenCalled();
    press(review, 'Escape', text);
    expect(text.blur).toHaveBeenCalledTimes(1);
    expect(ran).toEqual(['toggleRec', 'notes', 'help']);
  });
});

describe("an extension's version is never landed on by following or comparing", () => {
  it('following the latest skips v0, and a v0-only video stays on v0', async () => {
    const calls: string[] = [];
    const view = {
      followLatest: true,
      finishing: false,
      seg: null,
      starting: false,
      switching: false,
      shown: 'v0',
      video: { currentTime: 0 },
      versions: [V0] as Version[],
      async setBase(label: string) {
        calls.push(label);
        this.shown = label;
      },
    };
    await Review.prototype.followCurrent.call(view as unknown as Review);
    expect(calls).toEqual([]);
    view.versions = [V0, v('v1')]; // Start v1's cut arrived
    await Review.prototype.followCurrent.call(view as unknown as Review);
    expect(calls).toEqual(['v1']);
    view.versions = [v('v1'), V0]; // whatever the order
    view.shown = 'v1';
    await Review.prototype.followCurrent.call(view as unknown as Review);
    expect(calls).toEqual(['v1']);
  });

  it('comparing (C) never goes to v0: with one cut there is nothing to compare', async () => {
    const { review } = page([V0, v('v1')], 'v1');
    const said: string[] = [];
    review.toast.say = (m: string) => void said.push(m);
    expect(review.cuts.map((x) => x.label)).toEqual(['v1']);
    await review.flip();
    expect(said).toEqual(['Only one version']);
    expect(review.shown).toBe('v1');
  });

  it('names versions as the strip and the picker read them; Record only where the kind records', () => {
    const { review } = page([V0, v('v1')], 'v0');
    expect(review.nameOf('v1')).toBe('v1');
    expect(review.nameOf('v0')).toBe('v0'); // api/ext hasn't answered: its label alone
    expect(review.recordHere).toBe(false);
    review.exts = [
      {
        id: 'ignition',
        name: 'Ignition',
        version: '1.0.0',
        base: 'ext/ignition/',
        module: 'ui/ignition.js',
        style: '',
        nav: null,
        kinds: [
          {
            id: 'ignition.doc',
            kind: 'doc',
            folder: 'v0',
            marker: 'idea.md',
            label: 'Script',
            status: 'Idea',
            record: true,
          },
        ],
        slots: [{ slot: 'version', kind: 'ignition.doc', title: '', when: 'always' }],
        machine: true,
      },
    ];
    expect(review.nameOf('v0')).toBe('v0 · Script');
    expect(review.recordHere).toBe(true);
    expect(review.versionExt?.id).toBe('ignition');
    review.shown = 'v1';
    expect(review.recordHere).toBe(true);
    expect(review.versionExt).toBeNull();
  });
});

describe("a talk-out on an extension's version", () => {
  it("goes to the slot's ctx.onRecorded when it stops: the blob, its type, how long, when", async () => {
    const { review } = page([V0], 'v0');
    const got: Recorded[] = [];
    const off = review.extHost('version').onRecorded!((a) => got.push(a));
    const rec = {
      mimeType: 'audio/webm;codecs=opus',
      onstop: null as null | (() => void),
      stop() {
        this.onstop?.();
      },
    };
    review.seg = {
      talk: true,
      id: 'R',
      rec,
      mime: 'audio/webm',
      chunks: [new Blob(['ab']), new Blob(['cd'])],
      p0: 1,
      p1: null,
    } as unknown as Segment;
    await review.stopRec();
    expect(review.seg).toBeNull();
    expect(got).toHaveLength(1);
    expect(got[0]!.type).toBe('audio/webm');
    expect(got[0]!.blob.size).toBe(4);
    expect(got[0]!.duration).toBeGreaterThanOrEqual(0);
    expect(typeof got[0]!.started).toBe('string');
    expect(review.take.id).toBeNull(); // no take
    off();
  });

  it("R doesn't record where the kind doesn't; on a video R shows To do (an extension's tab closes)", async () => {
    const { review } = page([V0, v('v1')], 'v0');
    await review.startRec();
    expect(review.starting).toBe(false);
    review.shown = 'v1';
    review.sideTab = 'hello/sidebar-tab';
    review.commentFilter = 'done';
    review.toast.say = () => {};
    const start = review.startRec(); // no microphone in a test: it says so and stops
    expect(review.sideTab).toBeNull();
    expect(review.commentFilter).toBe('todo');
    await start;
  });
});
