import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SEEK_STALL_MS, Scrubber, type Scrubbable } from './scrub';

/** A fake <video>: records seeks; `land()` fires `seeked` like the browser does when a seek finishes. */
class FakeVideo implements Scrubbable {
  paused = true;
  seeks: { t: number; fast: boolean }[] = [];
  plays = 0;
  #t = 0;
  #seeked: (() => void)[] = [];
  get currentTime(): number {
    return this.#t;
  }
  set currentTime(t: number) {
    this.#t = t;
    this.seeks.push({ t, fast: false });
  }
  fastSeek = (t: number): void => {
    this.#t = t;
    this.seeks.push({ t, fast: true });
  };
  pause(): void {
    this.paused = true;
  }
  play(): Promise<void> {
    this.paused = false;
    this.plays++;
    return Promise.resolve();
  }
  addEventListener(_: 'seeked', fn: () => void): void {
    this.#seeked.push(fn);
  }
  land(): void {
    for (const fn of this.#seeked) fn();
  }
}

describe('Scrubber', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('keeps one seek in flight and always goes to the latest finger position', () => {
    const v = new FakeVideo(),
      s = new Scrubber(v);
    s.start(100);
    s.to(90);
    s.to(80);
    s.to(70); // 90 and 80 are skipped: only the latest waits
    expect(v.seeks).toEqual([{ t: 100, fast: true }]);
    v.land();
    expect(v.seeks.map((x) => x.t)).toEqual([100, 70]);
    v.land();
    expect(v.seeks).toHaveLength(2);
  });

  it('ends with one exact seek to where the finger lifted', () => {
    const v = new FakeVideo(),
      s = new Scrubber(v);
    s.start(100);
    s.to(20);
    s.end();
    v.land(); // the fast seek to 100 lands; the exact seek to 20 goes
    expect(v.seeks.at(-1)).toEqual({ t: 20, fast: false });
    v.land();
    expect(v.currentTime).toBe(20);
  });

  it('pauses playback for the drag and plays again after, only if it was playing', () => {
    const v = new FakeVideo(),
      s = new Scrubber(v);
    v.paused = false; // playing
    s.start(50);
    expect(v.paused).toBe(true);
    s.end();
    v.land(); // fast seek
    expect(v.plays).toBe(0); // the exact seek is still to come
    v.land(); // exact seek
    expect(v.paused).toBe(false);
    expect(v.plays).toBe(1);

    const w = new FakeVideo(),
      t = new Scrubber(w); // paused before the drag: stays paused
    t.start(50);
    t.end();
    w.land();
    w.land();
    expect(w.paused).toBe(true);
    expect(w.plays).toBe(0);
  });

  it('a seek that never says seeked does not block the drag forever', () => {
    const v = new FakeVideo(),
      s = new Scrubber(v);
    s.start(100);
    s.to(10);
    vi.advanceTimersByTime(SEEK_STALL_MS + 1);
    expect(v.seeks.map((x) => x.t)).toEqual([100, 10]);
  });

  it('uses a plain exact seek where the browser has no fastSeek', () => {
    const v = new FakeVideo();
    delete (v as Partial<FakeVideo>).fastSeek;
    const s = new Scrubber(v);
    s.start(30);
    expect(v.seeks).toEqual([{ t: 30, fast: false }]);
  });
});
