// Keyboard: the legacy player's keys, unchanged, plus N (milestone 2) to show or hide the notes sidebar. Handled on
// window in the capture phase so they work whatever has focus, and nothing else on the page sees them.

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
  | 'notes';

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
  c: 'flip',
  '[': 'stepPrev',
  ']': 'stepNext',
  '1': 'color1',
  '2': 'color2',
  '3': 'color3',
  '4': 'color4',
  f: 'fullscreen',
  n: 'notes',
};

/** Keys that do nothing on auto-repeat (holding R must not toggle recording on and off). */
const NO_REPEAT = 'kzrfcn[] ';

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
