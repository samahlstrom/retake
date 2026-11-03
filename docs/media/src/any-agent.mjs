// 3. Bring your own agent: Claude Code, Codex or any command-line agent plugs into the same socket. One agent per
// video takes that video's notes and makes its next version.
import {
  Motion, INK, PAPER, RED, MUTED, EASE, IN, STEP, LINEAR,
  rect, circle, path, g, at, text, arrow, travelKeys, heading, icon, wordmark,
} from './kit.mjs';

export default function anyAgent() {
  const m = new Motion({ duration: 10, stillAt: 8.8 });
  const RESET = 9.25;
  const out = [];
  out.push(heading('Bring your own agent.'));

  const rows = [
    { video: 'intro', from: 'v4', to: 'v5', agent: 'Claude Code' },
    { video: 'promo', from: 'v2', to: 'v3', agent: 'Codex' },
    { video: 'tutorial', from: 'v7', to: 'v8', agent: 'Any CLI agent' },
  ];
  const Y = [250, 370, 490];
  const P = { x: 430, y: 150, w: 160, h: 410 };
  const PLUG_W = 250, PLUG_H = 76, IN_X = P.x + P.w + 10, OUT_X = IN_X + 190;

  // The plugs go under the panel, so their prongs disappear into it.
  rows.forEach((r, i) => {
    const cy = Y[i];
    const b = 0.4 + i * 2.8;
    const cable = `M${PLUG_W} 0C${PLUG_W + 90} 0 ${PLUG_W + 90} 34 ${PLUG_W + 180} 34C${PLUG_W + 260} 34 ${PLUG_W + 300} 12 ${PLUG_W + 420} 18`;
    const body = rect(-22, -16, 30, 9, { r: 3, fill: INK, stroke: 'none' }) + rect(-22, 7, 30, 9, { r: 3, fill: INK, stroke: 'none' })
      + path(cable)
      + rect(0, -PLUG_H / 2, PLUG_W, PLUG_H, { r: 16 })
      + icon.terminal(20, -16, 40, 32, { sw: 2.5 }) + text(76, 9, r.agent, { size: 24 });
    const spin = m.a([[0, { transform: 'rotate(0deg)' }], [1, { transform: 'rotate(360deg)' }, LINEAR]], { period: 1 });
    const working = at(PLUG_W - 30, 0, at(0, 0, path('M0 -10C5.5 -10 10 -5.5 10 0C10 5.5 5.5 10 0 10C-5.5 10 -10 5.5 -10 0', { sw: 2.5 }), spin),
      m.a([[0, { opacity: 0 }], [b + 1.3, { opacity: 0 }, EASE], [b + 1.45, { opacity: 1 }], [b + 1.85, { opacity: 1 }, EASE], [b + 2.0, { opacity: 0 }]]));
    out.push(at(IN_X, cy, body + working, m.a([
      [0, { transform: `translate(${OUT_X - IN_X}px,0px)` }], [b, { transform: `translate(${OUT_X - IN_X}px,0px)` }, EASE],
      [b + 0.6, { transform: 'translate(0px,0px)' }], [RESET + i * 0.08, { transform: 'translate(0px,0px)' }, IN],
      [RESET + 0.45 + i * 0.08, { transform: `translate(${OUT_X - IN_X}px,0px)` }],
    ])));
  });

  // The notes go into Retake, to this video's agent; the new version comes back out (under the panel, so they pass
  // through it).
  rows.forEach((r, i) => {
    const cy = Y[i];
    const b = 0.4 + i * 2.8;
    for (let k = 0; k < 3; k++) {
      out.push(at(0, 0, rect(-9, -9, 18, 18, { r: 4, fill: PAPER, stroke: INK, sw: 2.5 }), m.a(travelKeys([[338, cy], [P.x + 60, cy]], b + 0.75 + k * 0.12, b + 1.2 + k * 0.12, { fade: 0.04 }))));
    }
    out.push(at(0, 0, circle(0, 0, 8, { fill: INK, stroke: PAPER, sw: 2.5 }), m.a(travelKeys([[P.x + 40, cy], [270, cy]], b + 1.85, b + 2.3, { fade: 0.04 }))));
  });

  // Retake: one socket per video.
  out.push(rect(P.x, P.y, P.w, P.h, { r: 18 }) + wordmark(P.x + P.w / 2, P.y + 48, 28, 'middle'));
  rows.forEach((r, i) => {
    const cy = Y[i];
    const b = 0.4 + i * 2.8;
    out.push(rect(P.x + P.w - 44, cy - 34, 32, 68, { r: 10, sw: 2.5 }));
    out.push(rect(P.x + P.w - 36, cy - 16, 18, 9, { r: 3, fill: INK, stroke: 'none' }) + rect(P.x + P.w - 36, cy + 7, 18, 9, { r: 3, fill: INK, stroke: 'none' }));
    out.push(circle(P.x + 34, cy, 7, { sw: 2.5 }));
    out.push(at(P.x + 34, cy, circle(0, 0, 7, { fill: RED, stroke: RED, sw: 2.5 }), m.a([[0, { opacity: 0 }], [b + 0.55, { opacity: 0 }, EASE], [b + 0.7, { opacity: 1 }], [RESET, { opacity: 1 }, IN], [RESET + 0.3, { opacity: 0 }]])));
  });

  // The videos, each with its notes, and the version its agent makes.
  rows.forEach((r, i) => {
    const cy = Y[i];
    const b = 0.4 + i * 2.8;
    const x = 56, w = 270, h = 84;
    out.push(rect(x, cy - h / 2, w, h, { r: 14 }) + icon.video(x + 18, cy - 20, 56, 40));
    out.push(text(x + 90, cy - 4, r.video, { size: 24 }) + text(x + 90, cy + 22, '3 notes', { size: 18, weight: 500, fill: MUTED }));
    const bx = x + w - 70, by = cy - 16;
    out.push(g(rect(bx, by, 54, 32, { r: 16, sw: 2.5 }) + text(bx + 27, by + 23, r.from, { size: 19, anchor: 'middle' }),
      { cls: m.a([[0, { opacity: 1 }], [b + 2.3, { opacity: 1 }, STEP], [b + 2.31, { opacity: 0 }], [RESET + 0.1, { opacity: 0 }, STEP], [RESET + 0.11, { opacity: 1 }]]) }));
    out.push(at(bx + 27, by + 16, rect(-27, -16, 54, 32, { r: 16, fill: INK, stroke: INK, sw: 2.5 }) + text(0, 7, r.to, { size: 19, anchor: 'middle', fill: PAPER }),
      m.a([[0, { opacity: 0, transform: 'scale(.7)' }], [b + 2.3, { opacity: 0, transform: 'scale(.7)' }, 'cubic-bezier(.34,1.56,.64,1)'], [b + 2.6, { opacity: 1, transform: 'scale(1)' }], [RESET, { opacity: 1, transform: 'scale(1)' }, IN], [RESET + 0.25, { opacity: 0, transform: 'scale(1)' }]])));
    out.push(arrow([[x + w + 12, cy], [P.x - 12, cy]]));
  });

  out.push(text(56, 618, 'Claude Code, Codex or any command-line agent. One agent per video.', { size: 24, weight: 500 }));

  return {
    name: 'any-agent',
    title: 'Bring your own agent',
    desc: 'Three videos, each with three notes, each connected through its own socket in Retake to an agent: Claude Code, Codex and any command-line agent plug into the same kind of socket. Each agent takes its video\'s notes and sends back the next version.',
    motion: m,
    body: out.join('\n'),
  };
}
