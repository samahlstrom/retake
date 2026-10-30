// Scrubbing the timeline the way video players do. A drag fires dozens of moves a second; Safari's engine (all iOS
// browsers) gives up on a seek that's still loading when the next one arrives and snaps back to where it was, and can
// keep playing through it. So: playback pauses for the drag, one seek is in flight at a time (always to the latest
// finger position, a fast keyframe seek where the browser has one), the release does one exact seek, and playback
// resumes only if it was playing when the drag began.

/** What the scrubber needs of a <video>. */
export interface Scrubbable {
  currentTime: number;
  readonly paused: boolean;
  fastSeek?: (t: number) => void;
  pause(): void;
  play(): Promise<void>;
  addEventListener(type: 'seeked', fn: () => void): void;
}

/** How long a seek may stay in flight before the next one goes anyway (a browser that never says `seeked`). */
export const SEEK_STALL_MS = 1500;

export class Scrubber {
  readonly #v: Scrubbable;
  #active = false;
  #wasPlaying = false;
  #inFlight = false;
  #pending: number | null = null;
  #pendingExact = false;
  #last: number | null = null;
  #resume = false;
  #stall: ReturnType<typeof setTimeout> | null = null;

  constructor(v: Scrubbable) {
    this.#v = v;
    v.addEventListener('seeked', () => this.#landed());
  }

  get active(): boolean {
    return this.#active;
  }

  /** The finger went down at time `t`. */
  start(t: number): void {
    this.#active = true;
    this.#resume = false;
    this.#wasPlaying = !this.#v.paused;
    if (this.#wasPlaying) this.#v.pause();
    this.to(t);
  }

  /** The finger moved to time `t`: seek there as soon as the seek in flight lands. */
  to(t: number): void {
    this.#last = t;
    this.#pending = t;
    this.#pendingExact = !this.#active;
    this.#pump();
  }

  /** The finger lifted: one exact seek to where it was, then play again if it was playing before the drag. */
  end(): void {
    if (!this.#active) return;
    this.#active = false;
    if (this.#last !== null) {
      this.#pending = this.#last;
      this.#pendingExact = true;
    }
    this.#resume = this.#wasPlaying;
    this.#pump();
  }

  #pump(): void {
    if (this.#inFlight || this.#pending === null) {
      if (!this.#inFlight && this.#pending === null) this.#finish();
      return;
    }
    const t = this.#pending,
      exact = this.#pendingExact;
    this.#pending = null;
    this.#inFlight = true;
    if (!exact && this.#v.fastSeek) this.#v.fastSeek(t);
    else this.#v.currentTime = t;
    this.#stall = setTimeout(() => this.#landed(), SEEK_STALL_MS);
  }

  #landed(): void {
    if (this.#stall) clearTimeout(this.#stall);
    this.#stall = null;
    if (!this.#inFlight) return; // someone else's seek (a key, a version switch)
    this.#inFlight = false;
    this.#pump();
  }

  /** Nothing pending and nothing in flight: after a release, play again if the drag paused playback. */
  #finish(): void {
    if (this.#active || !this.#resume) return;
    this.#resume = false;
    this.#v.play().catch(() => {});
  }
}
