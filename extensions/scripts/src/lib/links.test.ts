// Links and images in the document: where a tapped link may go, the owned file an image names, and a pasted image's
// name.
import { describe, expect, it } from 'vitest';
import { imageMarkdown, imageName, imagePath, imageUrl, isImage, safeHref } from './links';

describe('safeHref', () => {
  it('opens the web and mail only', () => {
    expect(safeHref('https://example.com/a?b=1')).toBe('https://example.com/a?b=1');
    expect(safeHref('<http://example.com>')).toBe('http://example.com');
    expect(safeHref('mailto:a@example.com')).toBe('mailto:a@example.com');
    expect(safeHref('www.example.com')).toBe('https://www.example.com');
    for (const bad of ['javascript:alert(1)', 'JaVaScRiPt:x', 'data:text/html,x', 'notes.md', '../x', ''])
      expect(safeHref(bad), bad).toBeNull();
  });
});

describe('images', () => {
  it('name owned files relative to v0/, never out of it', () => {
    expect(imagePath('images/a.png')).toBe('v0/images/a.png');
    expect(imagePath('./images/../shop.png')).toBe('v0/shop.png');
    expect(imagePath('<images/my pic.png>')).toBe('v0/images/my pic.png');
    expect(imagePath('images/my%20pic.png')).toBe('v0/images/my pic.png');
    for (const bad of ['../footage/a.png', '/etc/passwd', 'https://example.com/a.png', 'javascript:x', ''])
      expect(imagePath(bad), bad).toBeNull();
  });
  it('show owned files by their URL and https addresses as they are', () => {
    const url = (p: string) => `ext/scripts/files/${p}`;
    expect(imageUrl('images/a.png', url)).toBe('ext/scripts/files/v0/images/a.png');
    expect(imageUrl('https://example.com/a.png', url)).toBe('https://example.com/a.png');
    expect(imageUrl('http://example.com/a.png', url)).toBeNull();
  });
  it('a pasted image gets its own name, the stamp and its type’s extension', () => {
    expect(imageName({ name: 'image.png', type: 'image/png' }, '20261101-190211')).toBe('image-20261101-190211.png');
    expect(imageName({ name: 'Shop Front (final).JPG', type: 'image/jpeg' }, '20261101-190211', 2)).toBe(
      'shop-front-final-20261101-190211-2.jpg',
    );
    expect(imageName({ type: 'image/webp' }, 's')).toBe('image-s.webp');
    expect(isImage({ type: 'image/png' })).toBe(true);
    expect(isImage({ type: 'image/svg+xml' })).toBe(false);
    expect(isImage({ type: 'text/plain' })).toBe(false);
    expect(imageMarkdown('a.png')).toBe('![](images/a.png)');
    expect(imageMarkdown('a b.png')).toBe('![](<images/a b.png>)');
  });
});
