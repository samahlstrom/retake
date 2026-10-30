import { describe, expect, it } from 'vitest';
import { BIN, WAVE_STEP } from './constants';
import { addVoice, levelDb, newAcc, newVad, pushBars, vadStep, type WaveBar } from './voice';

describe('VAD', () => {
  it('measures block power in dB', () => {
    expect(levelDb(new Float32Array(1024).fill(1))).toBeCloseTo(0, 5);
    expect(levelDb(new Float32Array(1024).fill(0.1))).toBeCloseTo(-20, 3);
    expect(levelDb(new Float32Array(1024))).toBeCloseTo(-120, 3);
  });

  it('follows the noise floor down at once and up slowly (1.5 dB/s)', () => {
    const v = newVad();
    vadStep(v, -70, 0.016);
    expect(v.floor).toBe(-70);
    vadStep(v, -40, 1);
    expect(v.floor).toBe(-68.5);
    vadStep(v, -95, 0.016);
    expect(v.floor).toBe(-90); // never below -90
  });

  it('calls it voice 12 dB over the floor (and over -52 dB), with a 0.3 s hangover', () => {
    const v = newVad();
    vadStep(v, -70, 0.016); // floor -70
    vadStep(v, -55, 0.016); // 15 dB over the floor but under -52
    expect(v.active).toBe(false);
    const level = vadStep(v, -30, 0.016);
    expect(v.active).toBe(true);
    expect(v.hang).toBe(0.3);
    expect(level).toBeCloseTo(0.64);
    vadStep(v, -70, 0.2);
    expect(v.active).toBe(true); // still in the hangover
    vadStep(v, -70, 0.2);
    expect(v.active).toBe(false);
  });

  it('maps -62..-12 dB to a 0..1 level', () => {
    expect(vadStep(newVad(), -80, 0.016)).toBe(0);
    expect(vadStep(newVad(), -12, 0.016)).toBe(1);
    expect(vadStep(newVad(), 0, 0.016)).toBe(1);
  });
});

describe('waveform bars', () => {
  it('emits one bar per WAVE_STEP with the peak and whether voice was heard', () => {
    let bars: WaveBar[] = [];
    const acc = newAcc();
    bars = pushBars(bars, acc, WAVE_STEP / 2, 0.2, false);
    expect(bars).toEqual([]);
    bars = pushBars(bars, acc, WAVE_STEP / 2 + 1e-9, 0.6, true);
    expect(bars).toEqual([[0.6, true]]);
    bars = pushBars(bars, acc, WAVE_STEP * 2, 0.1, false);
    expect(bars.length).toBe(3);
    expect(bars[1][1]).toBe(true); // the bar that held voice keeps it
  });

  it('keeps the last 200 bars once there are more than 400', () => {
    let bars: WaveBar[] = Array.from({ length: 400 }, (_, i): WaveBar => [i / 400, false]);
    bars = pushBars(bars, newAcc(), WAVE_STEP, 1, true);
    expect(bars.length).toBe(200);
    expect(bars[199]).toEqual([1, true]);
  });
});

describe('voice bins', () => {
  it('spreads time over the bins passed while playing smoothly', () => {
    const bins = new Float32Array(20);
    addVoice(bins, 0.1, 1.05, 0.95, true, false); // from bin 3 to bin 4
    expect(bins[3]).toBeCloseTo(0.05);
    expect(bins[4]).toBeCloseTo(0.05);
  });
  it('puts all of it in the bin under the playhead when paused, seeking or jumping', () => {
    const bins = new Float32Array(20);
    addVoice(bins, 0.1, 2.0, 1.9, false, false);
    expect(bins[Math.floor(2 / BIN)]).toBeCloseTo(0.1);
    addVoice(bins, 0.1, 2.0, 0.5, true, false); // a jump of 1.5 s
    expect(bins[8]).toBeCloseTo(0.2);
    addVoice(bins, 0.1, 2.0, 1.95, true, true); // seeking
    expect(bins[8]).toBeCloseTo(0.3);
    addVoice(bins, 0.1, 1.0, null, true, false); // no previous playhead
    expect(bins[4]).toBeCloseTo(0.1);
  });
});
