// Images on the video (take format v1.1): while recording, an image dragged or pasted onto the video is pinned to the
// playhead and to the drop point, shown as a card at its box, and saved in the take as img_NN.<ext>. Pure, so it is
// unit-tested; the page wires it up in review.svelte.ts.
import type { Box, Point, Stroke, Take, TakeImage } from './types';
import { clamp, pad2, r4 } from './util';

/** The image types a take holds, and their file extensions. */
export const IMAGE_TYPES: Readonly<Record<string, string>> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/webp': 'webp',
  'image/gif': 'gif',
};
/** The server refuses bigger images (spec/notes.md). */
export const IMAGE_MAX = 25 * 1024 * 1024;
/** A new card is at most this share of the frame's width and height. */
export const CARD_W = 0.3;
export const CARD_H = 0.45;
/** Resizing never makes a card narrower than this share of the frame. */
export const CARD_MIN = 0.04;

export const HINT_IMAGE = 'Press R to record before adding images';
export const MSG_IMAGE_TYPE = 'Images can be PNG, JPEG, WebP or GIF';
export const MSG_IMAGE_BIG = 'That image is over 25 MB';
export const MSG_IMAGE_BAD = "Couldn't read that image";
export const MSG_IMAGE_NOFILE = "Couldn't get that image. Save it, then drop the file";
export const MSG_IMAGE_REFUSED = "This review server can't store images yet";

/** The file extension for an image type, or null if a take can't hold it. */
export function imageExt(mime: string): string | null {
  const m = mime.toLowerCase().split(';')[0].trim();
  return Object.hasOwn(IMAGE_TYPES, m) ? IMAGE_TYPES[m] : null;
}

/** The n-th image of a take (1-based): img_01.png, img_02.jpg, … */
export const imageName = (n: number, ext: string): string => `img_${pad2(n)}.${ext}`;

/** What the page decides about a dropped or pasted file before decoding it: null = fine, else the refusal toast. */
export function refuseImage(f: { type: string; size: number }, recording: boolean): string | null {
  if (!recording) return HINT_IMAGE;
  if (!imageExt(f.type)) return MSG_IMAGE_TYPE;
  if (f.size > IMAGE_MAX) return MSG_IMAGE_BIG;
  return null;
}

const r4box = (b: Box): Box => [r4(b[0]), r4(b[1]), r4(b[2]), r4(b[3])];

/**
 * The card for an image of `iw` x `ih` pixels dropped at `at` (normalized) on a frame of `fw` x `fh` CSS px: its own
 * size, but at most CARD_W of the frame's width and CARD_H of its height, centered on the drop point and kept inside
 * the frame.
 */
export function placeBox(at: Point, iw: number, ih: number, fw: number, fh: number): Box {
  const W = Math.max(1, fw),
    H = Math.max(1, fh),
    aspect = Math.max(1, ih) / Math.max(1, iw);
  let w = Math.min(Math.max(1, iw), W * CARD_W),
    h = w * aspect;
  if (h > H * CARD_H) {
    h = H * CARD_H;
    w = h / aspect;
  }
  const nw = w / W,
    nh = h / H;
  return r4box([clamp(at[0] - nw / 2, 0, 1 - nw), clamp(at[1] - nh / 2, 0, 1 - nh), nw, nh]);
}

/**
 * Resize a card by its bottom-right corner, dragged to `p` (normalized): the top-left corner stays, the aspect ratio
 * is kept, and the card stays inside the frame and at least CARD_MIN wide.
 */
export function resizeBox(box: Box, p: Point): Box {
  const [x, y, w, h] = box;
  if (w <= 0 || h <= 0) return box;
  const k = Math.max((p[0] - x) / w, (p[1] - y) / h);
  const lo = CARD_MIN / w,
    hi = Math.min((1 - x) / w, (1 - y) / h);
  const s = Math.max(Math.min(k, hi), Math.min(lo, hi));
  return r4box([x, y, w * s, h * s]);
}

/** What Z undoes next: the stroke or image added last (they share the take's id counter), or null. */
export function lastUndoable(
  t: Pick<Take, 'strokes' | 'images'>,
): { kind: 'stroke'; item: Stroke } | { kind: 'image'; item: TakeImage } | null {
  let best: { kind: 'stroke'; item: Stroke } | { kind: 'image'; item: TakeImage } | null = null;
  for (const st of t.strokes) if (!st.undone && (!best || st.id > best.item.id)) best = { kind: 'stroke', item: st };
  for (const im of t.images) if (!im.undone && (!best || im.id > best.item.id)) best = { kind: 'image', item: im };
  return best;
}

/** The first image file in a drop or a paste (`DataTransfer.files`, else its file items), or null. */
export function firstImageFile(dt: Pick<DataTransfer, 'files' | 'items'> | null | undefined): File | null {
  if (!dt) return null;
  for (const f of Array.from(dt.files ?? [])) if (f.type.startsWith('image/')) return f;
  for (const it of Array.from(dt.items ?? [])) {
    if (it.kind !== 'file' || !it.type.startsWith('image/')) continue;
    const f = it.getAsFile();
    if (f) return f;
  }
  return null;
}

/** A drag that may carry an image: files, or a URL (an image dragged from another tab). */
export const dragMayHoldImage = (types: readonly string[] | null | undefined): boolean =>
  !!types && (types.includes('Files') || types.includes('text/uri-list'));

/** The image URL of a drag from another tab that exposes no file (uri-list first, else the <img src> of its HTML). */
export function droppedImageUrl(getData: (type: string) => string): string | null {
  const list = getData('text/uri-list')
    .split(/\r?\n/)
    .map((s) => s.trim())
    .find((s) => s && !s.startsWith('#'));
  const html = /<img[^>]+src=["']([^"']+)["']/i.exec(getData('text/html'))?.[1];
  const url = html || list || '';
  return /^(https?:|data:image\/|blob:)/i.test(url) ? url : null;
}
