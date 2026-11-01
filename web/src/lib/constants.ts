// Every tuning constant of the legacy player, unchanged.

/** The pen colours. They are part of the review (the agent reads them as words), not of the look: no theme changes
 * them (spec/theme.md). */
export const COLORS = { red: '#ff3b30', yellow: '#ffd60a', green: '#30d158', white: '#ffffff' } as const;
export type PenColor = keyof typeof COLORS;
export const COLOR_KEYS: readonly PenColor[] = ['red', 'yellow', 'green', 'white'];

/** A stroke shows within ±SHOW s of its video time, fully opaque within ±SOLID s. */
export const SHOW = 1.5;
export const SOLID = 0.5;
/** Voice coverage resolution, in seconds of video. */
export const BIN = 0.25;
/** ←/→ seek step, seconds. */
export const SEEK = 5;
export const AUTOSAVE_MS = 5000;
/** A `tick` event every TICK_MS while playing keeps the two clocks locked. */
export const TICK_MS = 2000;
export const PING_MS = 15000;
/** One waveform bar per WAVE_STEP seconds of recording. */
export const WAVE_STEP = 0.06;
/** Horizontal padding of the timeline track inside its canvas, px. */
export const TL_PAD = 8;

/** Keepalive requests on page exit are limited in size; legacy keeps each one under this many bytes. */
export const KEEPALIVE_MAX = 60000;

export const HINT_DRAW = 'Press R to record before drawing';
/** The phone layout's wording: there is no R key, the record button is ●. */
export const HINT_DRAW_TOUCH = 'Tap ● to record before drawing';
export const MSG_SAVED = 'Saved. Press R to start a new take.';
export const MSG_SAVED_TOUCH = 'Saved. Tap ● to start a new take.';
export const MSG_SAVE_FAILED = 'Could not save. Is the review server still running?';
/** A viewer's share link (spec/team.md): watching only. */
export const VIEW_ONLY = 'View only';
