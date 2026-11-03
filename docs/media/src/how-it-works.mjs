// 1. How it works: watch v4, press R, talk and draw, Send; one agent edits the files, only the changed parts
// render, and the notes land in v5 one by one.
import {
  Motion, INK, PAPER, RED, MUTED, EASE, IN, LINEAR, STEP, POP, rect, circle, path, g, at, text, arrow, drawOn,
  showKeys, riseKeys, popKeys, travelKeys, heading, icon, textWidth, n,
} from './kit.mjs';

export default function howItWorks() {
  const m = new Motion({ duration: 10, stillAt: 9 });
  const RESET = 9.3;
  const out = [];
  out.push(heading('Talk and draw. Get the next version.'));

  // ---- the review page ----
  const B = { x: 56, y: 120, w: 720, h: 430 };
  out.push(icon.browser(B.x, B.y, B.w, B.h, { r: 18, bar: 44 }));

  // The version pill: v4, then v5 with the live dot.
  const pillX = 676, pillY = 127, pillW = 80, pillH = 32;
  out.push(g(rect(pillX, pillY, pillW, pillH, { r: 16 }) + text(pillX + pillW / 2, pillY + 23, 'v4', { size: 20, anchor: 'middle' }),
    { cls: m.a([[0, { opacity: 1 }], [5.45, { opacity: 1 }, EASE], [5.6, { opacity: 0 }], [RESET + 0.2, { opacity: 0 }, EASE], [RESET + 0.5, { opacity: 1 }]]) }));
  out.push(g(rect(pillX, pillY, pillW, pillH, { r: 16 }) + circle(pillX + 24, pillY + 16, 6, { fill: RED, stroke: 'none' }) + text(pillX + 50, pillY + 23, 'v5', { size: 20, anchor: 'middle' }),
    { cls: m.a([[0, { opacity: 0 }], [5.45, { opacity: 0 }, EASE], [5.6, { opacity: 1 }], [RESET + 0.2, { opacity: 1 }, IN], [RESET + 0.5, { opacity: 0 }]]) }));

  // R pressed: the key, the red recording dot and a voice level while recording.
  const keyX = 560, keyY = 128;
  out.push(at(keyX, keyY, rect(0, 0, 30, 30, { r: 7, sw: 2.5 }) + text(15, 22, 'R', { size: 18, anchor: 'middle' }),
    m.a([[0, { transform: 'translate(0px,0px)' }], [0.6, { transform: 'translate(0px,0px)' }, EASE], [0.7, { transform: 'translate(0px,3px)' }, EASE], [0.85, { transform: 'translate(0px,0px)' }], [3.3, { transform: 'translate(0px,0px)' }, EASE], [3.4, { transform: 'translate(0px,3px)' }, EASE], [3.55, { transform: 'translate(0px,0px)' }]])));
  const rec = m.a(showKeys(0.65, 3.4, 0.15, 0.25));
  const pulse = m.a([[0, { opacity: 1 }], [0.5, { opacity: 0.35 }], [1, { opacity: 1 }]], { period: 1 });
  const bars = [0, 1, 2, 3].map((i) => {
    const cls = m.a([[0, { transform: 'scale(1,.3)' }], [0.25, { transform: 'scale(1,1)' }], [0.5, { transform: 'scale(1,.45)' }]], { period: 0.5 });
    return at(624 + i * 10, 143, g(rect(-2, -10, 4, 20, { r: 2, fill: INK, stroke: 'none' }), { cls }).replace('<g class', `<g style="animation-delay:${-0.12 * i}s" class`));
  });
  out.push(g(at(606, 143, circle(0, 0, 7, { fill: RED, stroke: 'none' }), pulse) + bars.join(''), { cls: rec }));

  // The video frame: hills, the sun, a small title (v4), a big title, a logo (v5).
  const V = { x: 80, y: 184, w: 416, h: 234 };
  out.push(rect(V.x, V.y, V.w, V.h, { r: 10 }));
  out.push(`<clipPath id="frame"><rect x="${V.x}" y="${V.y}" width="${V.w}" height="${V.h}" rx="10"/></clipPath>`);
  const sunRays = [...Array(8)].map((_, i) => {
    const a = (i / 8) * Math.PI * 2;
    return `M${n(31 * Math.cos(a))} ${n(31 * Math.sin(a))}L${n(40 * Math.cos(a))} ${n(40 * Math.sin(a))}`;
  }).join('');
  const sun = at(436, 246, circle(0, 0, 20, { sw: 3 }) + path(sunRays, { sw: 3 }),
    m.a([[0, { transform: 'translate(0px,0px)' }], [6.85, { transform: 'translate(0px,0px)' }, EASE], [7.45, { transform: 'translate(-300px,0px)' }], [RESET, { transform: 'translate(-300px,0px)' }, EASE], [RESET + 0.6, { transform: 'translate(0px,0px)' }]]));
  const hills = path(`M${V.x} 372Q160 318 250 352Q330 382 400 340Q450 312 ${V.x + V.w} 330V${V.y + V.h}H${V.x}Z`, { fill: PAPER });
  out.push(`<g clip-path="url(#frame)">${sun}${hills}</g>`);
  // Titles.
  out.push(g(rect(98, 378, 120, 26, { r: 5, fill: INK, stroke: 'none' }) + text(110, 396, 'Title', { size: 15, fill: PAPER }),
    { cls: m.a([[0, { opacity: 1 }], [5.45, { opacity: 1 }, EASE], [5.6, { opacity: 0 }], [RESET + 0.1, { opacity: 0 }, EASE], [RESET + 0.4, { opacity: 1 }]]) }));
  out.push(at(98, 404, rect(0, -46, 200, 46, { r: 7, fill: INK, stroke: 'none' }) + text(16, -14, 'Title', { size: 28, fill: PAPER }),
    m.a([[0, { opacity: 0, transform: 'scale(.6)' }], [5.45, { opacity: 0, transform: 'scale(.6)' }, POP], [5.8, { opacity: 1, transform: 'scale(1)' }], [RESET, { opacity: 1, transform: 'scale(1)' }, IN], [RESET + 0.3, { opacity: 0, transform: 'scale(1)' }]])));
  // The logo, top right.
  out.push(at(466, 214, rect(-17, -17, 34, 34, { r: 8, fill: INK, stroke: 'none' }) + circle(0, 0, 7, { fill: PAPER, stroke: 'none' }),
    m.a(popKeys(8.25, RESET))));

  // The scrub bar: plays a moment, pauses for the review; the three notes mark it.
  out.push(path(`M${V.x} 448H${V.x + V.w}`, { sw: 3, stroke: '#d4d4d4' }));
  out.push(at(V.x, 448, path(`M0 0H${V.w}`, { sw: 3 }),
    m.a([[0, { transform: 'scale(.3,1)' }], [0.6, { transform: 'scale(.4,1)' }, LINEAR], [RESET + 0.2, { transform: 'scale(.4,1)' }, EASE], [RESET + 0.6, { transform: 'scale(.3,1)' }]])));
  out.push(at(0, 448, circle(V.x + V.w * 0.3, 0, 8, { fill: INK, stroke: 'none' }),
    m.a([[0, { transform: 'translate(0px,0px)' }], [0.6, { transform: `translate(${n(V.w * 0.1)}px,0px)` }, LINEAR], [RESET + 0.2, { transform: `translate(${n(V.w * 0.1)}px,0px)` }, EASE], [RESET + 0.6, { transform: 'translate(0px,0px)' }]])));

  // ---- the pen: three notes drawn over the frame ----
  const loop = (cx, cy, rx, ry, a0 = -0.3, turn = 1.12) => {
    const pts = [];
    for (let i = 0; i <= 40; i++) {
      const a = a0 + (i / 40) * Math.PI * 2 * turn;
      const wob = 1 + 0.05 * Math.sin(i * 0.7);
      pts.push(`${i ? 'L' : 'M'}${n(cx + rx * wob * Math.cos(a))} ${n(cy + ry * wob * Math.sin(a))}`);
    }
    return pts.join('');
  };
  const DONE = [5.45, 6.85, 8.25];
  out.push(drawOn(m, loop(158, 391, 78, 25, -0.4), 0.9, 1.45, { sw: 4, out: DONE[0] }));
  out.push(drawOn(m, 'M398 252Q352 266 300 256', 1.8, 2.15, { sw: 4, out: DONE[1] }));
  out.push(drawOn(m, 'M314 242L298 256L316 268', 2.15, 2.3, { sw: 4, out: DONE[1] }));
  out.push(drawOn(m, 'M446 194L488 192L490 234L446 236Z', 2.6, 3.0, { sw: 4, out: DONE[2] }));

  // ---- the comments ----
  const notes = ['Bigger title', 'Sun on the left', 'Add a logo'];
  const NOTE_ON = [1.45, 2.3, 3.0];
  const NX = 516, NW = 240, NH = 60;
  notes.forEach((label, i) => {
    const y = 184 + i * 72;
    const sx = NX + NW - 26, sy = y + NH / 2;
    const card = rect(NX, y, NW, NH, { r: 12 }) + circle(NX + 24, sy, 13, { sw: 2.5 }) + text(NX + 24, sy + 6, String(i + 1), { size: 16, anchor: 'middle' })
      + text(NX + 46, sy + 7, label, { size: 21 });
    const open = circle(sx, sy, 10, { sw: 2.5 });
    const spin = m.a([[0, { transform: 'rotate(0deg)' }], [1, { transform: 'rotate(360deg)' }, LINEAR]], { period: 1 });
    const working = at(sx, sy, path('M0 -10C5.5 -10 10 -5.5 10 0C10 5.5 5.5 10 0 10C-5.5 10 -10 5.5 -10 0', { sw: 2.5 }), spin);
    const done = at(sx, sy, circle(0, 0, 12, { fill: INK, stroke: 'none' }) + icon.check(0, 0.5, 6, { sw: 2.5, stroke: PAPER }), m.a(popKeys(DONE[i], RESET)));
    out.push(g(card + g(open, { cls: m.a([[0, { opacity: 1 }], [3.8, { opacity: 1 }, STEP], [3.81, { opacity: 0 }], [RESET + 0.3, { opacity: 0 }, STEP], [RESET + 0.31, { opacity: 1 }]]) })
      + g(working, { cls: m.a([[0, { opacity: 0 }], [3.8, { opacity: 0 }, EASE], [3.9, { opacity: 1 }], [DONE[i], { opacity: 1 }, EASE], [DONE[i] + 0.1, { opacity: 0 }]]) }) + done,
    { cls: m.a(riseKeys(NOTE_ON[i], RESET)) }));
  });

  // Send.
  const S = { x: NX, y: 470, w: NW, h: 52 };
  out.push(rect(S.x, S.y, S.w, S.h, { r: 26 }) + text(S.x + S.w / 2, S.y + 34, 'Send', { size: 24, anchor: 'middle' }));
  out.push(g(rect(S.x, S.y, S.w, S.h, { r: 26, fill: INK }) + text(S.x + S.w / 2, S.y + 34, 'Send', { size: 24, anchor: 'middle', fill: PAPER }),
    { cls: m.a([[0, { opacity: 0 }], [3.55, { opacity: 0 }, EASE], [3.65, { opacity: 1 }], [3.95, { opacity: 1 }, EASE], [4.3, { opacity: 0 }]]) }));

  // ---- your computer: the agent writes the project files; the renderer renders from them ----
  const C = { x: 848, y: 120, w: 296, h: 430 };
  out.push(rect(C.x, C.y, C.w, C.h, { r: 22, dash: '1 9', sw: 3 }).replace('<rect', '<rect stroke-linecap="round"'));
  out.push(text(C.x + 20, C.y + 32, 'Your computer', { size: 20, weight: 500, fill: MUTED }));
  // Each note's cycle: the agent edits a file, the renderer redraws that file's part, the part plays in v5.
  const CYCLE = [4.2, 5.6, 7.0];

  // Render: the video's parts, three of them redrawn.
  const R = { x: 866, y: 164, w: 260, h: 104 };
  out.push(rect(R.x, R.y, R.w, R.h, { r: 14 }) + text(R.x + 20, R.y + 32, 'Render', { size: 22 }));
  const cellW = 32, gap = 8, cx0 = R.x + 20, cy = R.y + 48, ch = 40;
  const changed = { 1: 0, 3: 1, 4: 2 };
  for (let c = 0; c < 6; c++) {
    const x = cx0 + c * (cellW + gap);
    out.push(rect(x, cy, cellW, ch, { r: 6, sw: 2.5 }));
    if (c in changed) {
      const t = CYCLE[changed[c]];
      const f0 = t + 0.65;
      out.push(g(rect(x, cy, cellW, ch, { r: 6, fill: 'url(#hatch)', stroke: 'none' }), { cls: m.a(showKeys(t + 0.3, f0 + 0.3, 0.15, 0.1)) }));
      out.push(at(x, cy, rect(0, 0, cellW, ch, { r: 6, fill: INK, stroke: INK, sw: 2.5 }),
        m.a([[0, { transform: 'scale(0,1)', opacity: 0 }], [f0, { transform: 'scale(0,1)', opacity: 1 }, LINEAR], [f0 + 0.3, { transform: 'scale(1,1)', opacity: 1 }], [RESET, { transform: 'scale(1,1)', opacity: 1 }, IN], [RESET + 0.3, { transform: 'scale(1,1)', opacity: 0 }]])));
    }
  }

  // The project files: the timeline, code, whatever format the project uses. The agent's edits land here.
  const F = { x: 866, y: 300, w: 260, h: 116 };
  const doc = (x, y) => path(`M${x} ${y - 14}H${x + 14}L${x + 21} ${y - 7}V${y + 14}H${x}Z`, { sw: 2.2, fill: PAPER });
  out.push(rect(F.x, F.y, F.w, F.h, { r: 14 }) + doc(F.x + 20, F.y + 28) + text(F.x + 54, F.y + 36, 'Project files', { size: 22 }));
  [150, 118, 176].forEach((w, i) => {
    const y = F.y + 62 + i * 19;
    const t = CYCLE[i] + 0.2;
    out.push(g(path(`M${F.x + 22} ${y}H${F.x + 32}M${F.x + 27} ${y - 5}V${y + 5}`, { sw: 2.2 }), { cls: m.a(showKeys(t, RESET, 0.1)) }));
    out.push(at(F.x + 44, y, rect(0, -4, w, 8, { r: 4, fill: INK, stroke: 'none' }),
      m.a([[0, { transform: 'scale(0,1)', opacity: 1 }], [t, { transform: 'scale(0,1)', opacity: 1 }, LINEAR], [t + 0.25, { transform: 'scale(1,1)', opacity: 1 }], [RESET, { transform: 'scale(1,1)', opacity: 1 }, IN], [RESET + 0.3, { transform: 'scale(1,1)', opacity: 0 }]])));
  });

  // The agent: busy from Send until the last note lands.
  const A = { x: 866, y: 448, w: 260, h: 84 };
  out.push(rect(A.x, A.y, A.w, A.h, { r: 14 }) + icon.terminal(A.x + 20, A.y + 25, 42, 34, { sw: 2.5 }) + text(A.x + 76, A.y + 51, 'Agent', { size: 24 }));
  const spinA = m.a([[0, { transform: 'rotate(0deg)' }], [1, { transform: 'rotate(360deg)' }, LINEAR]], { period: 1 });
  out.push(g(at(A.x + A.w - 32, A.y + A.h / 2, path('M0 -10C5.5 -10 10 -5.5 10 0C10 5.5 5.5 10 0 10C-5.5 10 -10 5.5 -10 0', { sw: 2.5 }), spinA),
    { cls: m.a([[0, { opacity: 0 }], [4.1, { opacity: 0 }, EASE], [4.25, { opacity: 1 }], [8.25, { opacity: 1 }, EASE], [8.4, { opacity: 0 }]]) }));

  // ---- the arrows, and what travels on them ----
  const ay = A.y + A.h / 2;
  out.push(arrow([[786, ay], [856, ay]]));
  out.push(arrow([[996, A.y - 6], [996, F.y + F.h + 8]]));
  out.push(arrow([[996, F.y - 6], [996, R.y + R.h + 8]]));
  out.push(arrow([[856, cy + ch / 2], [786, cy + ch / 2]]));
  const dot = (pts, t0, t1) => at(0, 0, circle(0, 0, 8, { fill: INK, stroke: PAPER, sw: 2.5 }), m.a(travelKeys(pts, t0, t1)));
  out.push(dot([[786, ay], [852, ay]], 3.8, 4.2));
  for (const t of CYCLE) {
    out.push(dot([[996, A.y - 8], [996, F.y + F.h + 10]], t, t + 0.2));
    out.push(dot([[996, F.y - 8], [996, R.y + R.h + 10]], t + 0.45, t + 0.65));
    out.push(dot([[852, cy + ch / 2], [786, cy + ch / 2]], t + 0.95, t + 1.25));
  }

  // ---- the steps, lit as they happen ----
  const steps = [['Talk and draw', 0.6], ['Send', 3.6], ['Agent edits files', 4.2], ['Changed parts render', 4.85], ['Notes land in v5', 5.45]];
  const size = 22, badge = 15;
  const widths = steps.map(([s]) => badge * 2 + 10 + textWidth(s, size, 600));
  const free = (1200 - 112 - widths.reduce((a, b) => a + b, 0)) / (steps.length - 1);
  let x = 56;
  steps.forEach(([s, t], i) => {
    const body = circle(x + badge, 606, badge, { fill: INK, stroke: 'none' }) + text(x + badge, 612, String(i + 1), { size: 17, anchor: 'middle', fill: PAPER })
      + text(x + badge * 2 + 10, 614, s, { size });
    out.push(g(body, { cls: m.a([[0, { opacity: 0.25 }], [t, { opacity: 0.25 }, EASE], [t + 0.25, { opacity: 1 }], [RESET, { opacity: 1 }, EASE], [RESET + 0.4, { opacity: 0.25 }]]) }));
    x += widths[i] + free;
  });

  out.unshift(`<defs><pattern id="hatch" width="8" height="8" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><path d="M0 0V8" stroke="${INK}" stroke-width="2.5"/></pattern></defs>`);
  return {
    name: 'how-it-works',
    title: 'How Retake works',
    desc: 'You watch v4, press R, talk and draw three notes over the video, and press Send. One agent on your computer edits the project files (the timeline, code, whatever format the project uses), the renderer redraws only the parts of the video those edits changed, and each note turns done as its change lands in v5.',
    motion: m,
    body: out.join('\n'),
  };
}
