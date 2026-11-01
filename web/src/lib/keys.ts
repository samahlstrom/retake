// Keyboard: the legacy player's keys, unchanged, plus N (milestone 2) to show or hide the notes sidebar, E for the
// eraser and > for the next video of a home. Handled on window in the capture phase so they work whatever has focus, and nothing else on the page sees
// them.

export type Action =
  | 'save'
  | 'undo'
  | 'togglePlay'
  | 'toggleRec'
  | 'back'
  | 'forward'
  | 'framePrev'
  | 'frameNext'
  | 'escape'
  | 'flip'
  | 'stepPrev'
  | 'stepNext'
  | 'color1'
  | 'color2'
  | 'color3'
  | 'color4'
  | 'fullscreen'
  | 'notes'
  | 'eraser'
  | 'next'
  | 'help';

const PLAIN: Record<string, Action> = {
  ' ': 'togglePlay',
  k: 'togglePlay',
  r: 'toggleRec',
  ArrowLeft: 'back',
  ArrowRight: 'forward',
  z: 'undo',
  ',': 'framePrev',
  '.': 'frameNext',
  Escape: 'escape',
  // Versions (spec/notes.md, "Comments belong to their version"): the comments and markers follow.
  c: 'flip',
  '[': 'stepPrev',
  ']': 'stepNext',
  '1': 'color1',
  '2': 'color2',
  '3': 'color3',
  '4': 'color4',
  f: 'fullscreen',
  n: 'notes',
  e: 'eraser',
  // The next video to review, in a home (spec/home.md). Shift+. on a US keyboard: its own character, so it never
  // reaches '.' (a frame step), and N stays the notes.
  '>': 'next',
  // The keys sheet (Shift+/ on a US keyboard).
  '?': 'help',
};

/** Keys that do nothing on auto-repeat (holding R must not toggle recording on and off). */
const NO_REPEAT = 'kzrfcne[]>? ';

export interface KeyLike {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  repeat: boolean;
}

/** What a keydown does. `null`: not ours, let it through. Otherwise the event is cancelled (`preventDefault`), also
 * stopped from reaching anything else when `stop`, and `act` runs unless it is null (an auto-repeat to ignore). */
export interface KeyResult {
  act: Action | null;
  stop: boolean;
}

/** Ctrl/Cmd+S saves and Ctrl/Cmd+Z undoes; any other modifier combination is left alone. */
export function keyAction(e: KeyLike): KeyResult | null {
  const k = e.key.length === 1 ? e.key.toLowerCase() : e.key,
    mod = e.ctrlKey || e.metaKey;
  if (mod && k === 's') return { act: 'save', stop: false };
  if (mod && k === 'z') return { act: 'undo', stop: false };
  if (mod || e.altKey) return null;
  const act = Object.hasOwn(PLAIN, k) ? PLAIN[k] : null;
  if (!act) return null;
  return { act: e.repeat && NO_REPEAT.includes(k) ? null : act, stop: true };
}

/** The keys sheet (? or its button): every key of the player, in the order people need them. */
export const KEY_SHEET: readonly { keys: string[]; what: string }[] = [
  { keys: ['R'], what: 'Record your voice and pen. R again stops' },
  { keys: ['Space'], what: 'Play or pause' },
  { keys: ['←', '→'], what: 'Back or forward 5 seconds' },
  { keys: [',', '.'], what: 'One frame back or forward' },
  { keys: ['1', '2', '3', '4'], what: 'Pen color' },
  { keys: ['E'], what: 'Eraser' },
  { keys: ['Z'], what: 'Undo the last line' },
  { keys: ['C'], what: 'Flip to the other version and back' },
  { keys: ['[', ']'], what: 'Older or newer version' },
  { keys: ['N'], what: 'Show or hide comments' },
  { keys: ['>'], what: 'Next video to review' },
  { keys: ['F'], what: 'Full screen' },
  { keys: ['?'], what: 'These keys' },
];

/** localStorage key: the keyboard hint has been seen (it shows once per browser). */
export const HINT_KEY = 'retake.hint';

type HintStorage = Pick<Storage, 'getItem' | 'setItem'> | null;
const hintStorage = (): HintStorage => {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null; // blocked
  }
};

/** The hint was dismissed in this browser before. */
export function hintSeen(storage: HintStorage = hintStorage()): boolean {
  try {
    return storage?.getItem(HINT_KEY) === 'seen';
  } catch {
    return false;
  }
}

export function markHintSeen(storage: HintStorage = hintStorage()): void {
  try {
    storage?.setItem(HINT_KEY, 'seen');
  } catch {
    /* blocked: it shows again next time, nothing worse */
  }
}
