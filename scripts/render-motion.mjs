#!/usr/bin/env node
// Renders docs/media/<name>.svg into a still, an MP4 and a GIF, in headless Chromium on the CPU.
//
//   node scripts/render-motion.mjs [name...] [--chromium PATH] [--fps 30] [--stills-only] [--keep-frames]
//
// The still is the SVG with prefers-reduced-motion (each graphic's complete state): docs/media/stills/<name>.png,
// 1920×1080. For the videos, every CSS animation is paused and stepped with the Web Animations API (currentTime),
// one screenshot per frame, so the frames are exact whatever the machine's speed; ffmpeg (x264, no GPU) makes
// docs/media/<name>.mp4 (1920×1080) and a small docs/media/<name>.gif (800 wide, 15 fps). Frames go to
// .retake-work/motion-frames/ and are deleted afterwards. The browser is the plain Chromium binary
// (/usr/lib/chromium/chromium, or CHROMIUM_PATH), headless with --disable-gpu, and always closed.
// Run it after `node scripts/build-motion.mjs`. Needs e2e's node_modules (playwright-core) and ffmpeg.
import { createRequire } from 'node:module';
import { existsSync, mkdirSync, readdirSync, rmSync, statSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const media = join(root, 'docs/media');
const args = process.argv.slice(2);
const opt = (name, fallback) => {
  const i = args.indexOf(name);
  if (i < 0) return fallback;
  const v = args[i + 1];
  args.splice(i, 2);
  return v;
};
const has = (name) => {
  const i = args.indexOf(name);
  if (i >= 0) args.splice(i, 1);
  return i >= 0;
};
const executablePath = opt('--chromium', process.env.CHROMIUM_PATH || '/usr/lib/chromium/chromium');
const fps = Number(opt('--fps', '30'));
const stillsOnly = has('--stills-only');
const keepFrames = has('--keep-frames');
const W = 1920, H = 1080;
const GIF_MAX = 3 * 1024 * 1024;

const names = args.length ? args : readdirSync(media).filter((f) => f.endsWith('.svg')).map((f) => f.slice(0, -4)).sort();
const require = createRequire(join(root, 'e2e/package.json'));
const { chromium } = require('playwright-core');

const ffmpeg = (a) => execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...a], { stdio: 'inherit' });

// The loop length: every animation repeats on it (scripts/build-motion.mjs checks that), so the longest one is it.
async function open(page, name, reduced) {
  await page.emulateMedia({ reducedMotion: reduced ? 'reduce' : 'no-preference' });
  await page.goto(pathToFileURL(join(media, `${name}.svg`)).href);
  return page.evaluate(async ([w, h]) => {
    const svg = document.documentElement;
    svg.setAttribute('width', String(w));
    svg.setAttribute('height', String(h));
    await document.fonts.ready;
    const anims = document.getAnimations();
    for (const a of anims) a.pause();
    const loop = Math.max(0, ...anims.map((a) => Number(a.effect.getTiming().duration)));
    const faces = [...document.fonts].filter((f) => f.status === 'loaded').length;
    return { loop, count: anims.length, faces };
  }, [W, H]);
}

const browser = await chromium.launch({ executablePath, headless: true, args: ['--disable-gpu', '--hide-scrollbars', '--mute-audio'] });
try {
  const page = await browser.newPage({ viewport: { width: W, height: H }, deviceScaleFactor: 1 });
  mkdirSync(join(media, 'stills'), { recursive: true });
  for (const name of names) {
    const still = await open(page, name, true);
    if (still.faces < 2) throw new Error(`${name}: the embedded fonts didn't load (${still.faces})`);
    await page.screenshot({ path: join(media, 'stills', `${name}.png`) });
    console.log(`docs/media/stills/${name}.png`);
    if (stillsOnly) continue;

    const { loop, count } = await open(page, name, false);
    const frames = Math.round((loop / 1000) * fps);
    const dir = join(root, '.retake-work/motion-frames', name);
    rmSync(dir, { recursive: true, force: true });
    mkdirSync(dir, { recursive: true });
    for (let f = 0; f < frames; f++) {
      const t = (f * 1000) / fps;
      await page.evaluate((ms) => new Promise((done) => {
        for (const a of document.getAnimations()) a.currentTime = ms;
        requestAnimationFrame(() => done());
      }), t);
      await page.screenshot({ path: join(dir, `${String(f).padStart(4, '0')}.png`) });
    }
    const mp4 = join(media, `${name}.mp4`);
    ffmpeg(['-framerate', String(fps), '-i', join(dir, '%04d.png'), '-c:v', 'libx264', '-preset', 'slow', '-crf', '20', '-tune', 'animation',
      '-pix_fmt', 'yuv420p', '-threads', '6', '-movflags', '+faststart', mp4]);
    const gif = join(media, `${name}.gif`);
    ffmpeg(['-framerate', String(fps), '-i', join(dir, '%04d.png'), '-threads', '6', '-vf',
      'fps=15,scale=800:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=48:stats_mode=full[p];[b][p]paletteuse=dither=none:diff_mode=rectangle',
      '-loop', '0', gif]);
    const kb = (p) => `${Math.round(statSync(p).size / 1024)} KB`;
    console.log(`docs/media/${name}.mp4 ${kb(mp4)}, ${name}.gif ${kb(gif)} (${frames} frames, ${count} animations, ${loop / 1000}s loop)`);
    if (statSync(gif).size > GIF_MAX) console.warn(`${name}.gif is over 3 MB`);
    if (!keepFrames) rmSync(dir, { recursive: true, force: true });
  }
} finally {
  await browser.close();
}
if (!keepFrames && existsSync(join(root, '.retake-work/motion-frames'))) rmSync(join(root, '.retake-work/motion-frames'), { recursive: true, force: true });
