import { describe, expect, it } from 'vitest';
import {
  CARD_MIN,
  dragMayHoldImage,
  droppedImageUrl,
  firstImageFile,
  HINT_IMAGE,
  IMAGE_MAX,
  imageExt,
  imageName,
  lastUndoable,
  MSG_IMAGE_BIG,
  MSG_IMAGE_TYPE,
  placeBox,
  refuseImage,
  resizeBox,
} from './images';
import { newTake } from './take';
import type { Box, Stroke, Take, TakeImage } from './types';

describe('image files', () => {
  it('maps the four image types to their extensions, and nothing else', () => {
    expect(imageExt('image/png')).toBe('png');
    expect(imageExt('image/jpeg')).toBe('jpg');
    expect(imageExt('IMAGE/WEBP')).toBe('webp');
    expect(imageExt('image/gif; charset=binary')).toBe('gif');
    for (const m of ['image/svg+xml', 'image/avif', 'text/plain', '', 'constructor']) expect(imageExt(m)).toBeNull();
  });

  it('names images img_01, img_02, … like segments', () => {
    expect(imageName(1, 'png')).toBe('img_01.png');
    expect(imageName(12, 'jpg')).toBe('img_12.jpg');
    expect(imageName(100, 'gif')).toBe('img_100.gif');
  });

  it('refuses an image unless recording, of a known type, and at most 25 MiB', () => {
    const png = { type: 'image/png', size: 1000 };
    expect(refuseImage(png, false)).toBe(HINT_IMAGE);
    expect(HINT_IMAGE).toBe('Press R to record before adding images');
    expect(refuseImage(png, true)).toBeNull();
    expect(refuseImage({ type: 'image/svg+xml', size: 10 }, true)).toBe(MSG_IMAGE_TYPE);
    expect(refuseImage({ type: 'image/jpeg', size: IMAGE_MAX }, true)).toBeNull();
    expect(refuseImage({ type: 'image/jpeg', size: IMAGE_MAX + 1 }, true)).toBe(MSG_IMAGE_BIG);
  });
});

describe('image box math', () => {
  const close = (a: Box, b: Box) => a.forEach((v, i) => expect(v).toBeCloseTo(b[i], 4));

  it('a small image keeps its own size, centered on the drop point', () => {
    // 200x100 px on a 1600x900 frame: 0.125 x 0.1111 of it.
    close(placeBox([0.5, 0.5], 200, 100, 1600, 900), [0.4375, 0.4444, 0.125, 0.1111]);
  });

  it('a big image is at most 30% of the frame wide (and 45% high), with its aspect ratio', () => {
    const [, , w, h] = placeBox([0.5, 0.5], 3840, 2160, 1600, 900);
    expect(w).toBeCloseTo(0.3, 4);
    expect((h * 900) / (w * 1600)).toBeCloseTo(2160 / 3840, 3);
    const tall = placeBox([0.5, 0.5], 1000, 4000, 1600, 900);
    expect(tall[3]).toBeCloseTo(0.45, 4);
    expect((tall[2] * 1600) / (tall[3] * 900)).toBeCloseTo(0.25, 3);
  });

  it('stays inside the frame when dropped near an edge', () => {
    const b = placeBox([0.99, 0.01], 400, 300, 1600, 900);
    expect(b[0] + b[2]).toBeLessThanOrEqual(1);
    expect(b[1]).toBe(0);
    expect(b[0]).toBeCloseTo(1 - b[2], 4);
  });

  it('dragging the corner resizes from the top-left corner, keeping the aspect ratio', () => {
    const box: Box = [0.2, 0.2, 0.2, 0.1];
    close(resizeBox(box, [0.6, 0.4]), [0.2, 0.2, 0.4, 0.2]);
    // The farther axis wins, so a diagonal drag grows it smoothly.
    close(resizeBox(box, [0.3, 0.5]), [0.2, 0.2, 0.6, 0.3]);
    close(resizeBox(box, [0.3, 0.25]), [0.2, 0.2, 0.1, 0.05]);
  });

  it('never grows past the frame or shrinks below the minimum', () => {
    const box: Box = [0.6, 0.5, 0.2, 0.1];
    const big = resizeBox(box, [5, 5]);
    expect(big[0] + big[2]).toBeCloseTo(1, 4);
    expect(big[3] / big[2]).toBeCloseTo(0.5, 3);
    const small = resizeBox(box, [0, 0]);
    expect(small[2]).toBeCloseTo(CARD_MIN, 4);
    expect(small[3]).toBeCloseTo(CARD_MIN / 2, 4);
  });
});

describe('undo across strokes and images', () => {
  const stroke = (id: number): Stroke => ({
    id,
    seg: 'S1',
    version: 'v6',
    flip: false,
    color: 'red',
    p0: id,
    p1: id + 0.5,
    video_t: id,
    video_t_end: id,
    paused: true,
    points: [[0.1, 0.1]],
  });
  const image = (id: number): TakeImage => ({
    id,
    seg: 'S1',
    version: 'v6',
    flip: false,
    file: `img_0${id}.png`,
    mime: 'image/png',
    p0: id,
    video_t: id,
    box: [0.1, 0.1, 0.2, 0.2],
    url: 'blob:x',
    blob: new Blob([]),
    upload: 'pending',
    stored: Promise.resolve(),
  });
  /** Undo like Z does, returning what it undid. */
  const z = (t: Take) => {
    const last = lastUndoable(t);
    if (last) last.item.undone = true;
    return last && `${last.kind} ${last.item.id}`;
  };

  it('undoes whichever came last, stroke or image, back to the start', () => {
    const t = newTake('v6', 4);
    t.strokes.push(stroke(1), stroke(3), stroke(4));
    t.images.push(image(2), image(5));
    expect([z(t), z(t), z(t), z(t), z(t), z(t)]).toEqual([
      'image 5',
      'stroke 4',
      'stroke 3',
      'image 2',
      'stroke 1',
      null,
    ]);
  });

  it('skips what is already undone, and works with only images', () => {
    const t = newTake('v6', 4);
    t.images.push(image(1), image(2));
    t.images[1].undone = true;
    expect(z(t)).toBe('image 1');
    expect(z(t)).toBeNull();
  });
});

describe('drops and pastes', () => {
  const file = (name: string, type: string) => new File([new Uint8Array([1, 2, 3])], name, { type });
  const dt = (files: File[], items: { kind: string; type: string; getAsFile: () => File | null }[] = []) =>
    ({ files, items }) as unknown as DataTransfer;

  it('takes the first image file of a drop or paste, else an image item', () => {
    const png = file('a.png', 'image/png');
    expect(firstImageFile(dt([file('a.txt', 'text/plain'), png]))).toBe(png);
    const gif = file('b.gif', 'image/gif');
    expect(
      firstImageFile(
        dt(
          [],
          [
            { kind: 'string', type: 'text/html', getAsFile: () => null },
            { kind: 'file', type: 'image/gif', getAsFile: () => gif },
          ],
        ),
      ),
    ).toBe(gif);
    expect(firstImageFile(dt([file('a.txt', 'text/plain')]))).toBeNull();
    expect(firstImageFile(null)).toBeNull();
  });

  it('a drag may hold an image when it has files or a URL', () => {
    expect(dragMayHoldImage(['Files'])).toBe(true);
    expect(dragMayHoldImage(['text/uri-list', 'text/html'])).toBe(true);
    expect(dragMayHoldImage(['text/plain'])).toBe(false);
    expect(dragMayHoldImage(undefined)).toBe(false);
  });

  it("finds the image URL of a drag from another tab: the <img> src, else the uri-list's first URL", () => {
    const data = (d: Record<string, string>) => (t: string) => d[t] ?? '';
    expect(
      droppedImageUrl(
        data({
          'text/uri-list': 'https://example.org/page\r\n',
          'text/html': '<a href="/x"><img alt="" src="https://example.org/pic.png"></a>',
        }),
      ),
    ).toBe('https://example.org/pic.png');
    expect(droppedImageUrl(data({ 'text/uri-list': '# comment\nhttps://example.org/b.jpg' }))).toBe(
      'https://example.org/b.jpg',
    );
    expect(droppedImageUrl(data({ 'text/uri-list': 'javascript:alert(1)' }))).toBeNull();
    expect(droppedImageUrl(data({}))).toBeNull();
  });
});
