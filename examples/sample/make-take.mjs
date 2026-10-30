// Writes the JSON files of the sample's pre-made take (take/*.json), as the review page would have saved them: one
// segment on v2, paused at 11.0 s in step two, with a red loop around the step label. @SAMPLE@ is the sample folder,
// which scripts/make-sample.sh fills in. The audio and transcript come from make-voice.sh.
//
// usage: node examples/sample/make-take.mjs
import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const out = join(dirname(fileURLToPath(import.meta.url)), 'take');
const take = '2026-01-15_10-00-00';
const segDur = 6.171; // seg_01.webm (ffprobe)
const vt = 11.0;
const t0 = 1.0, t1 = +(t0 + segDur).toFixed(3);
const dur = 22.166667;
const w = (f, v) => writeFileSync(join(out, f), JSON.stringify(v, null, 1) + '\n');

w('session.json', {
  format: 3, version: 'v2', session: take, created: '2026-01-15T10:00:00Z', saved: '2026-01-15T10:00:08Z',
  clock: 'session_t', recorded: segDur,
  video: { path: '@SAMPLE@/how-to/v2/how-to_v2.mp4', name: 'how-to_v2.mp4', duration: dur, fps: 30.0, width: 1280, height: 720 },
  display: { show_within: 1.5 },
  versions: [
    { label: 'v1', path: '@SAMPLE@/how-to/v1/how-to_v1.mp4', duration: dur },
    { label: 'v2', path: '@SAMPLE@/how-to/v2/how-to_v2.mp4', duration: dur },
  ],
  author: { name: 'Sample Reviewer' },
  closed: true,
});
w('segments.json', [{
  id: 'S1', file: 'seg_01.webm', mime: 'audio/webm;codecs=opus', version: 'v2', flip: false,
  t_start: t0, t_end: t1, recording: false, video_start: vt, video_end: vt, video_min: vt, video_max: vt,
}]);
// A loop around the label bar (x 40-560 px, y 618-682 px of 1280x720), drawn while paused.
const pts = [];
for (let i = 0; i <= 28; i++) {
  const a = -Math.PI / 2 + (i / 26) * 2 * Math.PI; // a little past one turn, like a hand-drawn loop
  pts.push([+(0.234 + 0.225 * Math.cos(a)).toFixed(4), +(0.903 + 0.07 * Math.sin(a)).toFixed(4)]);
}
const d0 = 3.2, d1 = 4.0;
w('strokes.json', [{
  id: 1, seg: 'S1', version: 'v2', flip: false, color: 'red', session_t: d0, session_t_end: d1,
  video_t: vt, video_t_end: vt, paused: true, points: pts,
}]);
w('events.json', [
  { session_t: 0.0, video_t: vt, event: 'start', playing: false, rate: 1, version: 'v2', base: 'v2', flip: false },
  { session_t: t0, video_t: vt, event: 'rec_start', seg: 'S1', playing: false, version: 'v2', flip: false },
  { session_t: d0, video_t: vt, event: 'draw_start', id: 1, seg: 'S1', color: 'red' },
  { session_t: d1, video_t: vt, event: 'draw_end', id: 1, seg: 'S1' },
  { session_t: t1, video_t: vt, event: 'rec_stop', seg: 'S1' },
]);
// Voice coverage on the video timeline (0.25 s bins): everything was said while paused at 11.0 s.
w('voice.json', { bin: 0.25, bins: [[44, 0.8]] });
