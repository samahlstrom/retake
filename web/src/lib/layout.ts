// Which layout the page uses. Desktop keeps the bar under the video exactly as the legacy player has it. Phones get
// their own layout: portrait (the video on top, big controls under it) or landscape (the video fills the height, a
// slim bar at the bottom). A desktop window made too small for the bar gets the phone layout too.

export type LayoutMode = 'desktop' | 'portrait' | 'landscape';

/** A touch screen (coarse pointer) up to this wide, or up to TOUCH_SHORT high, is a phone (or a small tablet). */
export const TOUCH_NARROW = 900;
export const TOUCH_SHORT = 600;
/** Any window narrower or shorter than this can't fit the desktop bar. */
export const SMALL_W = 800;
export const SMALL_H = 420;

export interface Viewport {
  width: number;
  height: number;
  /** `(pointer: coarse)`: the main pointer is a finger. */
  coarse: boolean;
}

export function layoutMode({ width, height, coarse }: Viewport): LayoutMode {
  const small = width < SMALL_W || height < SMALL_H;
  const touchSized = coarse && (width <= TOUCH_NARROW || height <= TOUCH_SHORT);
  if (!small && !touchSized) return 'desktop';
  return width > height ? 'landscape' : 'portrait';
}

/**
 * The same test as a media query. app.css hides the prerendered page under it until the script has picked the layout,
 * so a phone never flashes the desktop bar. (The thresholds above, less 0.02 px for the strict ones.)
 */
export const PHONE_MEDIA =
  `(max-width: ${SMALL_W - 0.02}px), (max-height: ${SMALL_H - 0.02}px), ` +
  `(pointer: coarse) and (max-width: ${TOUCH_NARROW}px), (pointer: coarse) and (max-height: ${TOUCH_SHORT}px)`;
