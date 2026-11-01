import { describe, expect, it } from 'vitest';
import { TL_PAD } from './constants';
import { DEFAULT_PALETTE, palette } from './theme';
import {
  covers,
  drawTimeline,
  pendingStrokes,
  pendingVoiceBins,
  TRACK,
  TRACK_PENDING,
  tlT,
  tlX,
  type TimelineData,
} from './timeline';

/** A 2D context that records the rounded rectangles it fills (the track and the played part) with their color. */
function recorder() {
  const rects: { fill: string; x: number; w: number }[] = [];
  let fill = '';
  let path: { x: number; w: number } | null = null;
  const g = {
    set fillStyle(v: string) {
      fill = v;
    },
    get fillStyle() {
      return fill;
    },
    globalAlpha: 1,
    clearRect() {},
    fillRect() {},
    beginPath() {
      path = null;
    },
    roundRect(x: number, _y: number, w: number) {
      path = { x, w };
    },
    arc() {
      path = null;
    },
    fill() {
      if (path) rects.push({ fill, ...path });
    },
  };
  return { g: g as unknown as CanvasRenderingContext2D, rects };
}

const base: TimelineData = {
  d: 100,
  xd: 100,
  vt: 10,
  recording: false,
  takeBins: new Float32Array(1),
  histBins: new Float32Array(1),
  histStrokes: [],
  takeStrokes: [],
};
const W = 1000 + 2 * TL_PAD;

describe('the timeline', () => {
  it('maps time to x and back over the padded track', () => {
    expect(tlX(50, W, 100)).toBe(TL_PAD + 500);
    expect(tlT(TL_PAD + 250, 0, W, 100)).toBe(25);
  });

  it('draws the whole track for a finished version', () => {
    const { g, rects } = recorder();
    drawTimeline(g, W, 20, base);
    expect(rects[0]).toEqual({ fill: TRACK, x: TL_PAD, w: 1000 });
    expect(rects.some((r) => r.fill === TRACK_PENDING)).toBe(false);
  });

  it('draws what a streamed render has so far brighter than the rest, on the expected scale', () => {
    const { g, rects } = recorder();
    drawTimeline(g, W, 20, { ...base, rendered: 41 });
    expect(rects[0]).toEqual({ fill: TRACK_PENDING, x: TL_PAD, w: 1000 }); // the whole expected length, faint
    expect(rects[1]).toEqual({ fill: TRACK, x: TL_PAD, w: 410 }); // rendered so far
    expect(rects[2]!.w).toBeCloseTo(100); // the played part on top, at the same scale
  });

  it("draws in the theme's colours when it has them (lib/theme.ts)", () => {
    const { g, rects } = recorder();
    const colors = { ...DEFAULT_PALETTE, track: 'rgb(1, 2, 3)', trackPlayed: 'rgb(4, 5, 6)', playhead: 'rgb(7, 8, 9)' };
    drawTimeline(g, W, 20, { ...base, colors });
    expect(rects.map((r) => r.fill)).toEqual(['rgb(1, 2, 3)', 'rgb(4, 5, 6)']);
    expect(palette()).toBe(DEFAULT_PALETTE); // no document here: the built-in look
  });
});

describe('audio becoming comments', () => {
  const comment = { kind: 'note', take: 'take-a', seg: 'S1', t0: 0.25, t1: 0.5, text: 'Make this bigger' };
  const bins = new Float32Array([1, 2, 3, 4]);

  it('replaces only the transcribed clip, keeping overlapping recordings and active audio', () => {
    const tracks = [
      { take: 'take-a', seg: 'S1', bins },
      { take: 'take-a', seg: 'S2', bins },
      { take: 'take-b', seg: 'S1', bins },
    ];
    expect([...pendingVoiceBins(tracks, [])]).toEqual([3, 6, 9, 12]);
    expect([...pendingVoiceBins(tracks, [comment])]).toEqual([2, 4, 6, 8]);
    expect([...pendingVoiceBins([{ ...tracks[0]!, active: true }], [comment])]).toEqual([...bins]);
  });

  it('keeps audio until real text arrives, including when transcription fails or only drawings exist', () => {
    const track = [{ take: 'take-a', seg: 'S1', bins }];
    expect([...pendingVoiceBins(track, [{ ...comment, text: '' }])]).toEqual([...bins]);
    expect([...pendingVoiceBins(track, [{ ...comment, kind: 'question' }])]).toEqual([...bins]);
  });

  it('hides completed history even with no speech while preserving an overlapping pending clip', () => {
    expect([
      ...pendingVoiceBins(
        [
          { take: 'a', bins, transcribed: true },
          { take: 'a', seg: 'S2', bins },
        ],
        [],
      ),
    ]).toEqual([...bins]);
    expect([...pendingVoiceBins([{ take: 'a', bins, transcribed: true }], [])]).toEqual([0, 0, 0, 0]);
  });

  it('replaces only matching time ranges in older aggregate waveforms', () => {
    expect([...pendingVoiceBins([{ take: 'take-a', bins }], [comment])]).toEqual([1, 0, 0, 4]);
    expect([...pendingVoiceBins([{ take: 'take-b', bins }], [comment])]).toEqual([...bins]);
  });
});

describe('one row of markers: pen ticks fold into their comment', () => {
  const rows = [{ take: 'A', t0: 10, t1: 12 }];
  it('a stroke of the same take near the comment is covered', () => {
    expect(covers(rows[0]!, { take: 'A', video_t: 11 }, null)).toBe(true);
    expect(covers(rows[0]!, { take: 'A', video_t: 14.4 }, null)).toBe(true);
    expect(covers(rows[0]!, { take: 'A', video_t: 15 }, null)).toBe(false);
    expect(covers(rows[0]!, { take: 'B', video_t: 11 }, null)).toBe(false);
  });
  it('the take being recorded has no take id on its strokes', () => {
    expect(covers(rows[0]!, { video_t: 11 }, 'A')).toBe(true);
    expect(covers(rows[0]!, { video_t: 11 }, 'B')).toBe(false);
    expect(covers({ t0: 10, t1: 12 }, { video_t: 11 }, null)).toBe(false);
  });
  it('only strokes no comment covers stay on the timeline', () => {
    const strokes = [{ take: 'A', video_t: 11 }, { take: 'A', video_t: 30 }, { video_t: 11 }];
    expect(pendingStrokes(strokes, rows, 'B')).toEqual([{ take: 'A', video_t: 30 }, { video_t: 11 }]);
    expect(pendingStrokes(strokes, rows, 'A')).toEqual([{ take: 'A', video_t: 30 }]);
    expect(pendingStrokes(strokes, [], 'A')).toEqual(strokes);
  });
});
