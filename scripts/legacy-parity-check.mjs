// Checks one run of the legacy e2e driver (tests/e2e_headless.mjs): every assertion of the legacy
// tests/check_e2e.py that doesn't need transcription or notes, plus the take the server saved on disk.
// usage: node scripts/legacy-parity-check.mjs <fixtures dir> <e2e out dir> [--author NAME]
import { execFileSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const [, , fixtures, outDir, ...rest] = process.argv;
if (!fixtures || !outDir) {
  console.error('usage: legacy-parity-check.mjs <fixtures dir> <e2e out dir> [--author NAME]');
  process.exit(2);
}
const author = rest[0] === '--author' ? rest[1] : null;

let passed = 0;
const failed = [];
function check(name, ok, detail) {
  if (ok) {
    passed++;
    console.log(`  ok    ${name}`);
  } else {
    failed.push(name);
    console.log(`  FAIL  ${name}: ${JSON.stringify(detail ?? null).slice(0, 500)}`);
  }
}
const json = p => JSON.parse(readFileSync(p, 'utf8'));
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

const rep = json(join(outDir, 'e2e_report.json'));
const c = rep.checks || {};
console.log(`checks of ${outDir}`);

// --- check_e2e.py, in its order (transcript and notes checks left out)
check('driver finished without an error', !rep.error, rep.error);
check('no exceptions in the page', !(rep.logs || []).some(x => x.includes('exception')), rep.logs);
const fam = Object.fromEntries((c.family || []).map(([label, name]) => [label, name]));
check('family is v5/v6/v7, draft decoy skipped',
  same(fam, { v5: 'clip_v5.mp4', v6: 'clip_v6.mp4', v7: 'clip_v7_0-010_preview.mp4' }), fam);

const a = c.afterUnrecordedDrag || {};
check('pen draws nothing while not recording',
  a.take === null && a.strokes === 0 && !a.rec && String(a.toast).includes('Press R'), a);
const lp = c.afterUnrecordedLoopDrag || {};
check('unrecorded loop drag adds no stroke', lp.strokes === 1 && !lp.rec, lp);

const f0 = c.flip?.before || {}, f1 = c.flip?.after || {};
check('C flips to v7 at the same frame, paused',
  f1.shown === 'v7' && f1.flipped && f1.base === 'v6' && f1.paused && Math.abs(f1.t - f0.t) < 0.04, c.flip);
check('C flips back to v6', c.flipBack?.shown === 'v6' && !c.flipBack?.flipped, c.flipBack);

const s0 = c.stepPrev?.before || {}, s1 = c.stepPrev?.after || {};
check('[ steps to v5 keeping time and play state',
  s1.base === 'v5' && s1.shown === 'v5' && !s1.paused && Math.abs(s1.t - s0.t) < 0.8, c.stepPrev);
check('] steps back to v6', c.stepBack?.base === 'v6', c.stepBack);
check('] past the v7 preview end is refused',
  c.stepPastEnd?.shown === 'v6' && String(c.stepPastEnd?.toast).includes('only covers'), c.stepPastEnd);
check('] inside the preview switches, [ comes back',
  c.stepIntoPreview?.shown === 'v7' && c.stepBackFromPreview?.base === 'v6', [c.stepIntoPreview, c.stepBackFromPreview]);
const fp = c.flipPastEnd || {};
check('C past the preview end is refused',
  fp.shown === 'v6' && !fp.flipped && String(fp.toast).includes('only covers'), fp);

const sv = c.saved || {};
const segs = sv.segments || [];
check('4 segments S1-S4', same(segs.map(s => s.id), ['S1', 'S2', 'S3', 'S4']), segs.map(s => s.id));
check('S3 recorded while flipped to v7, the rest on v6',
  segs.length === 4 && segs[2].version === 'v7' && segs[2].flip &&
  [segs[0], segs[1], segs[3]].every(s => s.version === 'v6' && !s.flip), segs.map(s => [s.id, s.version, s.flip]));
check('2 strokes: red in S1, green in S4',
  same(sv.strokes, [[1, 'S1', 'red', 'v6', false], [2, 'S4', 'green', 'v6', false]]), sv.strokes);
check('nothing saved for v5 or v7',
  !existsSync(join(fixtures, 'v5', 'clip_v5.review')) && !existsSync(join(fixtures, 'v7', 'clip_v7_0-010_preview.review')));
const rd = join(fixtures, 'v6', 'clip_v6.review');
const takes = existsSync(rd) ? readdirSync(rd).filter(n => n.startsWith('20')).sort() : [];
check('exactly one take on v6', takes.length === 1, takes);
const take = takes.length ? join(rd, takes[0]) : null;

function audioSeconds(p) {
  let raw = '';
  try {
    raw = execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', p],
      { encoding: 'utf8' }).trim();
  } catch { /* no duration header */ }
  let d = /^\d+\.?\d*$/.test(raw) ? parseFloat(raw) : 0;
  if (!d) { // MediaRecorder webm has no duration header: decode it
    const pcm = execFileSync('ffmpeg', ['-v', 'error', '-i', p, '-f', 's16le', '-ac', '1', '-ar', '16000', '-'],
      { maxBuffer: 1 << 28 });
    d = pcm.length / 32000;
  }
  return d;
}
if (take) {
  for (const s of segs) {
    const p = join(take, s.file || '');
    const d = existsSync(p) ? audioSeconds(p) : -1;
    check(`${s.id} audio (${s.file}) is as long as the segment`, Math.abs(d - (s.t_end - s.t_start)) < 0.6,
      { file: s.file, audio: d, segment: s.t_end - s.t_start });
  }
}
check('reload shows S1-S4 strokes as history', c.history?.strokes === 2 && c.history?.take === null, c.history);

// --- the take on disk
if (take) {
  const files = readdirSync(take).sort();
  const want = ['events.json', 'segments.json', 'session.json', 'strokes.json', 'voice.json',
    ...segs.map(s => s.file)].sort();
  check('take files', same(files, want), files);
  check('no temp files left', !files.some(n => n.endsWith('.tmp')), files);
  for (const n of ['session.json', 'segments.json', 'events.json', 'strokes.json', 'voice.json']) {
    const text = readFileSync(join(take, n), 'utf8');
    const lines = text.split('\n');
    check(`${n} is pretty JSON with 1-space indent`,
      lines.length > 1 && /^ \S/.test(lines[1]) && !text.endsWith('\n'), lines.slice(0, 3));
  }
  const meta = json(join(take, 'session.json'));
  const fixture = p => join(fixtures, p);
  check('session.json: version and video are the server\'s',
    meta.version === 'v6' && meta.video?.name === 'clip_v6.mp4' && meta.video?.path === fixture('v6/clip_v6.mp4') &&
    meta.video?.fps === 30 && meta.video?.width === 1920 && meta.video?.height === 1080 &&
    Math.abs(meta.video?.duration - 40) < 0.2, meta.video);
  check('session.json: versions are the family',
    same((meta.versions || []).map(v => [v.label, v.path]),
      [['v5', fixture('v5/clip_v5.mp4')], ['v6', fixture('v6/clip_v6.mp4')], ['v7', fixture('v7/clip_v7_0-010_preview.mp4')]]) &&
    meta.versions.every(v => typeof v.duration === 'number'), meta.versions);
  check('session.json: the page\'s own fields are kept', meta.format === 3 && meta.session === takes[0], meta);
  if (author) check(`session.json: author is ${author}`, meta.author?.name === author, meta.author);
  check('segments.json is what the page serialized', same(json(join(take, 'segments.json')), segs),
    json(join(take, 'segments.json')));
  const strokes = json(join(take, 'strokes.json'));
  check('strokes.json holds the 2 strokes',
    same(strokes.map(s => [s.id, s.seg, s.color, s.version, s.flip]), sv.strokes) &&
    strokes.every(s => Array.isArray(s.points) && s.points.length > 1), strokes.length);
  const kinds = [...new Set(json(join(take, 'events.json')).map(e => e.event))];
  check('events.json has the page\'s event kinds', same(kinds.sort(), [...(sv.kinds || [])].sort()), kinds);
  const voice = json(join(take, 'voice.json'));
  check('voice.json has bins', typeof voice.bin === 'number' && Array.isArray(voice.bins), voice);
}

console.log(`${passed} passed, ${failed.length} failed`);
process.exit(failed.length ? 1 : 0);
