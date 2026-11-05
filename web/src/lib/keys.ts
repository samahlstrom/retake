// Keyboard: the legacy player's keys, plus N (milestone 2) to show or hide the notes sidebar, E for the eraser and >
// for the next video of a home. The legacy [ and ] (step the version) are gone: the version picker and the comments'
// version strip do that; C still flips. Handled on window in the capture phase so they work whatever has focus, and
// nothing else on the page sees them.

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
  | 'color1'
  | 'color2'
  | 'color3'
  | 'color4'
  | 'fullscreen'
  | 'notes'
  | 'eraser'
  | 'next'
  | 'help'
  | 'slower'
  | 'faster'
  | 'stop';

export type ShortcutAction = Exclude<Action, 'save' | 'escape'>;
export type PlayerShortcuts = Record<ShortcutAction, string>;

/** The editable actions keep the legacy defaults. Space and Escape remain safe fixed aliases. */
export const SHORTCUT_ACTIONS: readonly { action: ShortcutAction; label: string; defaultKey: string }[] = [
  { action: 'toggleRec', label: 'Record your voice', defaultKey: 'R' },
  { action: 'togglePlay', label: 'Play or pause', defaultKey: 'K' },
  { action: 'back', label: 'Back 5 seconds', defaultKey: 'ArrowLeft' },
  { action: 'forward', label: 'Forward 5 seconds', defaultKey: 'ArrowRight' },
  { action: 'framePrev', label: 'One frame back', defaultKey: ',' },
  { action: 'frameNext', label: 'One frame forward', defaultKey: '.' },
  { action: 'color1', label: 'Red pen', defaultKey: '1' },
  { action: 'color2', label: 'Yellow pen', defaultKey: '2' },
  { action: 'color3', label: 'Green pen', defaultKey: '3' },
  { action: 'color4', label: 'White pen', defaultKey: '4' },
  { action: 'eraser', label: 'Eraser', defaultKey: 'E' },
  { action: 'undo', label: 'Undo', defaultKey: 'Z' },
  { action: 'flip', label: 'Compare versions', defaultKey: 'C' },
  { action: 'notes', label: 'Show or hide comments', defaultKey: 'N' },
  { action: 'fullscreen', label: 'Full screen', defaultKey: 'F' },
  { action: 'next', label: 'Next video', defaultKey: '>' },
  { action: 'help', label: 'Keyboard shortcuts', defaultKey: '?' },
  { action: 'slower', label: 'Slower playback', defaultKey: 'A' },
  { action: 'faster', label: 'Faster playback', defaultKey: 'D' },
  { action: 'stop', label: 'Stop playback', defaultKey: 'S' },
];

export const DEFAULT_SHORTCUTS: PlayerShortcuts = Object.fromEntries(
  SHORTCUT_ACTIONS.map(({ action, defaultKey }) => [action, defaultKey]),
) as PlayerShortcuts;
export const SHORTCUTS_KEY = 'retake.shortcuts';
const FIXED: Record<string, Action> = Object.assign(Object.create(null), { ' ': 'togglePlay', Escape: 'escape' });
const ENTRY_KEYS = new Set(['Enter', 'Escape', 'Tab']);
const SPECIAL_KEYS: Record<string, string> = { '←': 'ArrowLeft', '→': 'ArrowRight', Space: ' ' };

const canonicalKey = (key: string): string => SPECIAL_KEYS[key] ?? key;
const foldedKey = (key: string): string => (key.length === 1 ? key.toLowerCase() : key);
const fixedAction = (key: string): Action | null => (Object.hasOwn(FIXED, key) ? FIXED[key]! : null);

/** Stored preferences and input changes use this same validation, so no browser preference can shadow a command. */
export function validateShortcuts(value: unknown): { shortcuts: PlayerShortcuts } | { error: string } {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return { error: 'Invalid keyboard shortcuts.' };
  const input = value as Record<string, unknown>;
  const actions = Object.keys(DEFAULT_SHORTCUTS) as ShortcutAction[];
  if (Object.keys(input).some((key) => !actions.includes(key as ShortcutAction)))
    return { error: 'Invalid keyboard shortcuts.' };
  const out = {} as PlayerShortcuts;
  const used = new Set<string>();
  for (const action of actions) {
    const raw = input[action];
    if (typeof raw !== 'string') return { error: 'Invalid keyboard shortcuts.' };
    const key = canonicalKey(raw);
    if (!key || (key.length !== 1 && key !== 'ArrowLeft' && key !== 'ArrowRight') || ENTRY_KEYS.has(key))
      return { error: 'Use one player key.' };
    const folded = foldedKey(key);
    const fixed = fixedAction(key) ?? fixedAction(folded);
    if ((fixed && fixed !== action) || used.has(folded)) return { error: `${shortcutLabel(key)} is already in use.` };
    used.add(folded);
    out[action] = key;
  }
  return { shortcuts: out };
}

/** Browser preferences are optional: corrupt, unknown or colliding stored values return all defaults. */
export function playerShortcuts(storage: Pick<Storage, 'getItem'> | null = keyStorage()): PlayerShortcuts {
  try {
    const raw = storage?.getItem(SHORTCUTS_KEY);
    if (!raw) return { ...DEFAULT_SHORTCUTS };
    const parsed = validateShortcuts(JSON.parse(raw));
    return 'shortcuts' in parsed ? parsed.shortcuts : { ...DEFAULT_SHORTCUTS };
  } catch {
    return { ...DEFAULT_SHORTCUTS };
  }
}

export function shortcutLabel(key: string): string {
  return key === ' ' ? 'Space' : key === 'ArrowLeft' ? '←' : key === 'ArrowRight' ? '→' : key.toUpperCase();
}

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
export function keyAction(e: KeyLike, shortcuts: PlayerShortcuts = DEFAULT_SHORTCUTS): KeyResult | null {
  const k = e.key.length === 1 ? e.key.toLowerCase() : e.key,
    mod = e.ctrlKey || e.metaKey;
  if (mod && k === 's') return { act: 'save', stop: false };
  if (mod && k === 'z') return { act: 'undo', stop: false };
  if (mod || e.altKey) return null;
  for (const action of Object.keys(shortcuts) as ShortcutAction[]) {
    if (k === foldedKey(shortcuts[action])) {
      const repeated = [
        'toggleRec',
        'togglePlay',
        'undo',
        'flip',
        'fullscreen',
        'notes',
        'eraser',
        'next',
        'help',
      ].includes(action);
      return { act: e.repeat && repeated ? null : action, stop: true };
    }
  }
  const act = fixedAction(k);
  if (!act) return null;
  return { act: e.repeat && act === 'togglePlay' ? null : act, stop: true };
}

/** localStorage key: the keyboard hint has been seen (it shows once per browser). */
export const HINT_KEY = 'retake.hint';

type HintStorage = Pick<Storage, 'getItem' | 'setItem'> | null;
type KeyStorage = Pick<Storage, 'getItem'> | null;
const keyStorage = (): KeyStorage => {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
};
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
