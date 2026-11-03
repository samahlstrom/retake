// 6. Any tool: an agent reaches any tool it can use, through MCP, a command line or an API (ElevenLabs voices, image
// generation, Blender, ffmpeg). Retake only watches the files it changes.
import {
  Motion, INK, PAPER, MUTED, EASE, IN,
  rect, circle, path, g, at, text, arrow, heading, icon, riseKeys, travelKeys, pill,
} from './kit.mjs';

export default function anyTool() {
  const m = new Motion({ duration: 10, stillAt: 9 });
  const RESET = 9.3;
  const out = [];
  out.push(heading('Works with any tool.'));

  const tools = [
    { name: 'ElevenLabs', sub: 'voices', via: 'API', file: 'voice.wav' },
    { name: 'Image generation', sub: 'pictures', via: 'MCP', file: 'cover.png' },
    { name: 'Blender', sub: '3D scenes', via: 'CLI', file: 'intro.blend' },
    { name: 'ffmpeg', sub: 'audio, cuts', via: 'CLI', file: 'teaser.mp4' },
  ];
  const glyph = [
    (x, y) => icon.mic(x, y + 2, 1.15),
    (x, y) => rect(x - 18, y - 14, 36, 28, { r: 5, sw: 2.5 }) + path(`M${x - 18} ${y + 10}L${x - 6} ${y - 2}L${x + 4} ${y + 8}L${x + 9} ${y + 3}L${x + 18} ${y + 12}`, { sw: 2.5 }) + circle(x + 8, y - 6, 3.5, { fill: INK, stroke: 'none' }),
    (x, y) => path(`M${x} ${y - 18}L${x + 16} ${y - 9}V${y + 9}L${x} ${y + 18}L${x - 16} ${y + 9}V${y - 9}Z M${x - 16} ${y - 9}L${x} ${y}L${x + 16} ${y - 9}M${x} ${y}V${y + 18}`, { sw: 2.5, fill: PAPER }),
    (x, y) => icon.video(x - 19, y - 14, 38, 28),
  ];
  const AG = { x: 520, y: 300, w: 200, h: 140 };
  const acy = AG.y + AG.h / 2;
  const T0 = (i) => 0.4 + i * 1.95;

  tools.forEach((t, i) => {
    const y = 192 + i * 100, cy = y + 37;
    const a = [356 + 8, cy], b = [AG.x - 8, acy + (i - 1.5) * 22];
    // The connector, and how the agent reaches the tool.
    out.push(path(`M${a[0]} ${a[1]}L${b[0]} ${b[1]}`, { sw: 2.5 }));
    const px = a[0] + (b[0] - a[0]) * 0.42, py = a[1] + (b[1] - a[1]) * 0.42;
    const p = pill(0, 0, t.via, { size: 17, h: 30, padX: 12, sw: 2.5 });
    out.push(`<g transform="translate(${(px - p.w / 2).toFixed(1)} ${py - 15})">${p.svg}</g>`);
    // The tool.
    out.push(rect(56, y, 300, 74, { r: 14 }) + glyph[i](96, cy) + text(136, cy - 3, t.name, { size: 22 }) + text(136, cy + 22, t.sub, { size: 18, weight: 500, fill: MUTED }));
    // Busy while it works.
    out.push(g(rect(56, y, 300, 74, { r: 14, fill: 'none', sw: 5 }), { cls: m.a([[0, { opacity: 0 }], [T0(i) + 0.45, { opacity: 0 }, EASE], [T0(i) + 0.55, { opacity: 1 }], [T0(i) + 1.0, { opacity: 1 }, EASE], [T0(i) + 1.15, { opacity: 0 }]]) }));
    // Ask, then the result comes back.
    out.push(at(0, 0, circle(0, 0, 8, { fill: INK, stroke: PAPER, sw: 2.5 }), m.a(travelKeys([b, a], T0(i), T0(i) + 0.45))));
    out.push(at(0, 0, rect(-9, -11, 18, 22, { r: 3, fill: PAPER, stroke: INK, sw: 2.5 }), m.a(travelKeys([a, b], T0(i) + 1.05, T0(i) + 1.45))));
  });

  // The agent.
  out.push(rect(AG.x, AG.y, AG.w, AG.h, { r: 18 }) + icon.terminal(AG.x + 64, AG.y + 26, 72, 54) + text(AG.x + AG.w / 2, AG.y + 116, 'Agent', { size: 24, anchor: 'middle' }));

  // Your project: the files the agent writes, and Retake watching them.
  const F = { x: 840, y: 192, w: 304, h: 374 };
  out.push(rect(F.x, F.y, F.w, F.h, { r: 18 }) + icon.folder(F.x + 22, F.y + 22, 46, 34) + text(F.x + 82, F.y + 48, 'Your project', { size: 22 }));
  out.push(path(`M${F.x} ${F.y + 76}H${F.x + F.w}`, { sw: 2.5 }));
  const doc = (x, y) => path(`M${x} ${y - 13}H${x + 13}L${x + 20} ${y - 6}V${y + 13}H${x}Z`, { sw: 2.2, fill: PAPER });
  const files = ['timeline.json', ...tools.map((t) => t.file)];
  files.forEach((f, i) => {
    const y = F.y + 112 + i * 50;
    const row = doc(F.x + 26, y) + text(F.x + 60, y + 7, f, { size: 20 });
    out.push(i === 0 ? row : g(row, { cls: m.a(riseKeys(T0(i - 1) + 1.85, RESET, 10, 0.3)) }));
  });
  out.push(arrow([[AG.x + AG.w + 10, acy], [F.x - 10, acy]]));
  for (let i = 0; i < 4; i++) out.push(at(0, 0, rect(-9, -11, 18, 22, { r: 3, fill: PAPER, stroke: INK, sw: 2.5 }), m.a(travelKeys([[AG.x + AG.w + 12, acy], [F.x - 12, acy]], T0(i) + 1.5, T0(i) + 1.85))));

  // Retake's eye on the folder: it blinks when a file changes.
  const blinks = [[0, { transform: 'scale(1,1)' }]];
  for (let i = 0; i < 4; i++) {
    const t = T0(i) + 1.9;
    blinks.push([t, { transform: 'scale(1,1)' }, EASE], [t + 0.08, { transform: 'scale(1,.1)' }, EASE], [t + 0.2, { transform: 'scale(1,1)' }]);
  }
  out.push(at(F.x + 26, F.y - 30, icon.eye(0, 0, 40), m.a(blinks)) + text(F.x + 56, F.y - 23, 'Retake watches', { size: 20, weight: 500, fill: MUTED }));

  out.push(text(56, 624, 'MCP, a command line or an API. Retake only watches the files.', { size: 24, weight: 500 }));

  return {
    name: 'any-tool',
    title: 'Agents work with any tool',
    desc: 'The agent reaches ElevenLabs voices through an API, image generation through MCP, and Blender and ffmpeg through their command lines. Each tool\'s result becomes a file in your project (voice.wav, cover.png, intro.blend, teaser.mp4), and Retake, watching the project folder, sees each change.',
    motion: m,
    body: out.join('\n'),
  };
}
