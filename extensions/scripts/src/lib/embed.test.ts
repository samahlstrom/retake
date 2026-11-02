import { describe, expect, it } from 'vitest';
import { aloneUrl, cardFallback, embedFor, embedOfLine, FRAME_ATTRS, rawFrame, seconds, SITES } from './embed';

const PAGE = 'retake.example';
const src = (url: string): string | null => {
  const e = embedFor(url, PAGE);
  return e?.kind === 'frame' ? e.src : null;
};

describe('an address alone on its line', () => {
  it('is the line, with or without <>, and nothing else', () => {
    expect(aloneUrl('https://youtu.be/dQw4w9WgXcQ')).toBe('https://youtu.be/dQw4w9WgXcQ');
    expect(aloneUrl('  <https://example.com/a?b=1>  ')).toBe('https://example.com/a?b=1');
    expect(aloneUrl('http://example.com')).toBe('http://example.com');
    for (const line of [
      'See https://example.com',
      'https://example.com and more',
      '- https://example.com',
      '> https://example.com',
      '[a](https://example.com)',
      '<https://example.com',
      'https://example.com>',
      'ftp://example.com/x',
      'javascript:alert(1)',
      'https://',
      '',
    ])
      expect(aloneUrl(line), line).toBeNull();
  });
});

describe('known sites become their embed address', () => {
  it.each([
    ['https://www.youtube.com/watch?v=dQw4w9WgXcQ', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ'],
    ['https://youtube.com/watch?v=dQw4w9WgXcQ&t=1m30s', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?start=90'],
    [
      'https://m.youtube.com/watch?v=dQw4w9WgXcQ&start=42',
      'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?start=42',
    ],
    ['https://youtu.be/dQw4w9WgXcQ?t=75', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?start=75'],
    ['https://youtu.be/dQw4w9WgXcQ?si=abc', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ'],
    ['https://www.youtube.com/shorts/aBcDeFgHiJk', 'https://www.youtube-nocookie.com/embed/aBcDeFgHiJk'],
    ['https://www.youtube.com/live/aBcDeFgHiJk', 'https://www.youtube-nocookie.com/embed/aBcDeFgHiJk'],
    ['https://www.youtube.com/embed/aBcDeFgHiJk', 'https://www.youtube-nocookie.com/embed/aBcDeFgHiJk'],
    ['https://vimeo.com/76979871', 'https://player.vimeo.com/video/76979871'],
    ['https://vimeo.com/76979871/1a2b3c4d5e', 'https://player.vimeo.com/video/76979871?h=1a2b3c4d5e'],
    ['https://vimeo.com/channels/staffpicks/76979871#t=30s', 'https://player.vimeo.com/video/76979871#t=30s'],
    ['https://player.vimeo.com/video/76979871?h=abcdef12', 'https://player.vimeo.com/video/76979871?h=abcdef12'],
    [
      'https://www.loom.com/share/0281766fa2d04bb788eaf19e65135184',
      'https://www.loom.com/embed/0281766fa2d04bb788eaf19e65135184',
    ],
    [
      'https://clips.twitch.tv/AwkwardHelplessSalamanderSwiftRage',
      `https://clips.twitch.tv/embed?clip=AwkwardHelplessSalamanderSwiftRage&parent=${PAGE}`,
    ],
    [
      'https://www.twitch.tv/somechannel/clip/FunnyClip-abc_123',
      `https://clips.twitch.tv/embed?clip=FunnyClip-abc_123&parent=${PAGE}`,
    ],
    [
      'https://www.twitch.tv/videos/2903906005?t=1h2m3s',
      `https://player.twitch.tv/?video=2903906005&parent=${PAGE}&autoplay=false&time=1h2m3s`,
    ],
    [
      'https://x.com/someone/status/1790000000000000000',
      'https://platform.twitter.com/embed/Tweet.html?id=1790000000000000000',
    ],
    ['https://twitter.com/someone/status/20?s=20', 'https://platform.twitter.com/embed/Tweet.html?id=20'],
    ['https://www.instagram.com/p/C1a2b3c4d5e/', 'https://www.instagram.com/p/C1a2b3c4d5e/embed'],
    ['https://www.instagram.com/reel/C1a2b3c4d5e/?igsh=x', 'https://www.instagram.com/reel/C1a2b3c4d5e/embed'],
    ['https://www.instagram.com/reels/C1a2b3c4d5e/', 'https://www.instagram.com/reel/C1a2b3c4d5e/embed'],
    [
      'https://www.tiktok.com/@someone/video/7301234567890123456',
      'https://www.tiktok.com/embed/v2/7301234567890123456',
    ],
    [
      'https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC',
      'https://open.spotify.com/embed/track/4uLU6hMCjMI75M1A2tKUQC',
    ],
    [
      'https://open.spotify.com/intl-de/playlist/37i9dQZF1DXcBWIGoYBM5M?si=1',
      'https://open.spotify.com/embed/playlist/37i9dQZF1DXcBWIGoYBM5M',
    ],
    [
      'https://soundcloud.com/some-artist/a-track',
      `https://w.soundcloud.com/player/?url=${encodeURIComponent('https://soundcloud.com/some-artist/a-track')}&visual=false`,
    ],
    [
      'https://www.figma.com/design/AbCdEfGhIjKlMnOp/My-board?node-id=1-2',
      `https://www.figma.com/embed?embed_host=retake&url=${encodeURIComponent('https://www.figma.com/design/AbCdEfGhIjKlMnOp/My-board?node-id=1-2')}`,
    ],
    [
      'https://docs.google.com/document/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdef/edit?usp=sharing',
      'https://docs.google.com/document/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdef/preview',
    ],
    [
      'https://docs.google.com/presentation/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdef/edit#slide=id.p',
      'https://docs.google.com/presentation/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdef/preview',
    ],
    [
      'https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdef/edit#gid=0',
      'https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdef/preview',
    ],
  ])('%s', (url, want) => {
    expect(src(url)).toBe(want);
  });

  it('keeps each box: wide players 16:9, portrait ones narrow, posts and audio a fixed height', () => {
    const box = (url: string) => {
      const e = embedFor(url, PAGE);
      return e?.kind === 'frame' ? { ratio: e.ratio, height: e.height, maxWidth: e.maxWidth, site: e.site } : e;
    };
    expect(box('https://youtu.be/dQw4w9WgXcQ')).toEqual({
      ratio: 16 / 9,
      height: undefined,
      maxWidth: undefined,
      site: 'YouTube',
    });
    expect(box('https://www.youtube.com/shorts/aBcDeFgHiJk')).toMatchObject({ ratio: 9 / 16, maxWidth: 340 });
    expect(box('https://x.com/a/status/1')).toMatchObject({ height: 560, maxWidth: 550, site: 'X' });
    expect(box('https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC')).toMatchObject({ height: 152 });
    expect(box('https://open.spotify.com/album/4uLU6hMCjMI75M1A2tKUQC')).toMatchObject({ height: 352 });
    expect(box('https://soundcloud.com/some-artist/sets/a-set')).toMatchObject({ height: 450 });
    expect(box('https://docs.google.com/document/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123/edit')).toMatchObject({
      height: 560,
    });
  });

  it('Twitch names this page as its parent', () => {
    expect(embedFor('https://clips.twitch.tv/SomeClip', 'phone.example.net')).toMatchObject({
      src: 'https://clips.twitch.tv/embed?clip=SomeClip&parent=phone.example.net',
    });
  });

  it('every other address, and a site address that isn’t a player or a post, is a card', () => {
    for (const url of [
      'https://example.com/an/article',
      'http://example.com/',
      'https://www.youtube.com/@somechannel',
      'https://www.youtube.com/watch?v=short',
      'https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ',
      'https://notyoutube.com/watch?v=dQw4w9WgXcQ',
      'https://x.com/someone',
      'https://vm.tiktok.com/ZMabc/',
      'https://soundcloud.com/discover/sets/x',
      'https://open.spotify.com/user/someone',
      'https://www.twitch.tv/somechannel',
      'https://docs.google.com/forms/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123/edit',
    ])
      expect(embedFor(url, PAGE), url).toEqual({ kind: 'card', url: new URL(url).href });
    expect(embedFor('not a url', PAGE)).toBeNull();
    expect(embedFor('ftp://example.com/', PAGE)).toBeNull();
  });

  it('has one entry per site of the spec’s table', () => {
    expect(SITES.map((s) => s.name)).toEqual([
      'YouTube',
      'Vimeo',
      'Loom',
      'Twitch',
      'X',
      'Instagram',
      'TikTok',
      'Spotify',
      'SoundCloud',
      'Figma',
      'Google',
    ]);
  });
});

describe('a raw frame', () => {
  it('keeps only its src, size and title', () => {
    const f = rawFrame(
      '<iframe width="560" height="315" src="https://www.youtube.com/embed/x?si=a&amp;b=2" title="A &quot;clip&quot;" ' +
        'frameborder="0" allow="camera; microphone; autoplay" sandbox="allow-top-navigation" allowfullscreen ' +
        'onload="alert(1)" srcdoc="alert(1)" style="position:fixed" referrerpolicy="unsafe-url"></iframe>',
      PAGE,
    );
    expect(f).toEqual({
      kind: 'frame',
      site: 'www.youtube.com',
      src: 'https://www.youtube.com/embed/x?si=a&b=2',
      title: 'A "clip"',
      ratio: 560 / 315,
    });
  });

  it('sizes: a ratio from both, a height alone, else 16:9; clamped', () => {
    expect(rawFrame('<iframe src="https://e.example/a" width="100%" height="352"></iframe>', PAGE)).toMatchObject({
      height: 352,
    });
    expect(rawFrame("<iframe src='https://e.example/a'></iframe>", PAGE)).toMatchObject({ ratio: 16 / 9 });
    expect(rawFrame('<iframe src=https://e.example/a width=10 height=5000>', PAGE)).toMatchObject({ ratio: 1 / 3 });
    expect(rawFrame('<iframe src="https://e.example/a" height="9999px" />', PAGE)).toMatchObject({ height: 1200 });
    expect(rawFrame('<iframe src="https://e.example/a"></iframe>', PAGE)!.title).toBe('Embedded from e.example');
  });

  it('refuses what isn’t one safe frame', () => {
    for (const line of [
      '<iframe src="http://e.example/a"></iframe>', // not https
      '<iframe src="javascript:alert(1)"></iframe>',
      '<iframe src="data:text/html,<b>x</b>"></iframe>',
      '<iframe srcdoc="<script>alert(1)</script>"></iframe>', // no src
      `<iframe src="https://${PAGE}/p/x/"></iframe>`, // Retake itself
      `<iframe src="https://${PAGE.toUpperCase()}:8443/"></iframe>`,
      '<iframe src="https://user:pw@e.example/"></iframe>',
      '<iframe src="https://e.example/a"></iframe> and text',
      'text <iframe src="https://e.example/a"></iframe>',
      '<iframe src="https://e.example/a"><script>alert(1)</script></iframe>',
      '<iframe src="https://e.example/a" "junk"></iframe>',
      '<iframes src="https://e.example/a"></iframes>',
      '<div><iframe src="https://e.example/a"></iframe></div>',
      '<iframe src="https://e.example/a" srcdoc="<script>alert(1)</script>"></iframe>', // markup inside: stays text
    ])
      expect(rawFrame(line, PAGE), line).toBeNull();
  });

  it('every frame has the same attributes', () => {
    expect(FRAME_ATTRS).toEqual({
      sandbox: 'allow-scripts allow-same-origin allow-popups allow-presentation',
      allow: 'fullscreen; picture-in-picture; encrypted-media',
      referrerpolicy: 'strict-origin-when-cross-origin',
      loading: 'lazy',
    });
  });
});

describe('the rest', () => {
  it('a line embeds an address or a raw frame, else nothing', () => {
    expect(embedOfLine('https://youtu.be/dQw4w9WgXcQ', PAGE)).toMatchObject({ kind: 'frame', site: 'YouTube' });
    expect(embedOfLine('<iframe src="https://e.example/a"></iframe>', PAGE)).toMatchObject({ kind: 'frame' });
    expect(embedOfLine('https://example.com/', PAGE)).toEqual({ kind: 'card', url: 'https://example.com/' });
    expect(embedOfLine('Some words.', PAGE)).toBeNull();
  });

  it('seconds', () => {
    expect([
      seconds('90'),
      seconds('90s'),
      seconds('1m30s'),
      seconds('1h2m3s'),
      seconds('2m'),
      seconds('x'),
      seconds(''),
    ]).toEqual([90, 90, 90, 3723, 120, 0, 0]);
  });

  it('a card shows the host and the path until its preview comes', () => {
    expect(cardFallback('https://www.example.com/a/b/?q=1')).toEqual({
      title: 'example.com/a/b/?q=1',
      site: 'example.com',
    });
    expect(cardFallback('https://example.com/')).toEqual({ title: 'example.com', site: 'example.com' });
  });
});
