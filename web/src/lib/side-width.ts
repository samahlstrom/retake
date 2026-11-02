// The comments column's width on a computer (spec/notes.md, "The column's width"): its left edge drags it wider or
// narrower, and the width is remembered per viewer. Pure (and a storage stand-in), so it is unit-tested.

/** localStorage key of the dragged width, in px. Not there: the default (`--sidebar-width`). */
export const SIDE_WIDTH_KEY = 'retake.sidebar-width';
export const SIDE_MIN = 300;
export const SIDE_MAX = 760;
/** The video or the document always keeps at least this much of the window. */
export const STAGE_MIN = 420;
/** ←/→ on the handle: this much a press, SIDE_STEP_BIG with Shift. */
export const SIDE_STEP = 16;
export const SIDE_STEP_BIG = 48;

type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem' | 'removeItem'>;

/** The widest the column can be in a window this wide: SIDE_MAX, less when the stage would get under STAGE_MIN. */
export const sideMax = (viewport: number): number => Math.max(SIDE_MIN, Math.min(SIDE_MAX, viewport - STAGE_MIN));

/** A width as the column can have it in a window this wide: whole px, SIDE_MIN to sideMax. */
export function clampSide(width: number, viewport: number): number {
  if (!Number.isFinite(width)) return SIDE_MIN;
  return Math.round(Math.min(sideMax(viewport), Math.max(SIDE_MIN, width)));
}

/** The remembered width, or null (none, a bad value, or no storage). */
export function loadSideWidth(storage: Storage | null | undefined): number | null {
  try {
    const raw = storage?.getItem(SIDE_WIDTH_KEY);
    const n = raw == null ? NaN : Number(raw);
    return Number.isFinite(n) && n > 0 ? Math.round(Math.min(SIDE_MAX, Math.max(SIDE_MIN, n))) : null;
  } catch {
    return null;
  }
}

/** Remembers the width, or forgets it (null: back to the default). A blocked storage just doesn't remember. */
export function saveSideWidth(storage: Storage | null | undefined, width: number | null): void {
  try {
    if (width == null) storage?.removeItem(SIDE_WIDTH_KEY);
    else storage?.setItem(SIDE_WIDTH_KEY, String(Math.round(width)));
  } catch {
    /* blocked */
  }
}

/** The column's width as CSS: the remembered width, but never more than leaves the stage STAGE_MIN. */
export const sideWidthCss = (width: number): string => `min(${Math.round(width)}px, 100vw - ${STAGE_MIN}px)`;

/** localStorage, or null where there is none or it's blocked. */
export function browserStorage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}
