// 3. Bring your own agent: Retake has one socket, and any agent fits it: Claude Code, Codex or any command-line
// agent. You pick which: the default in Settings, another for one video in its ⋯ menu, or another for one Send by
// holding Send. One agent works on a video's notes at a time.
import {
  Motion, INK, PAPER, RED, MUTED, EASE, IN, OUT, POP,
  rect, circle, path, g, at, text, arrow, travelKeys, heading, icon, wordmark, textWidth,
} from './kit.mjs';

export default function anyAgent() {
  const m = new Motion({ duration: 10, stillAt: 1.7 });
  const RESET = 9.3;
  const out = [];
  out.push(heading('Bring your own agent.'));

  const SY = 330; // the socket's height
  const P = { x: 420, y: 196, w: 180, h: 268 };
  const SEAT = P.x + P.w + 2; // a plugged-in plug's left edge
  const HOME_X = 930, STAGE_X = 650; // waiting plugs; where a plug lines up before it goes in
  const PLUG_W = 220, PLUG_H = 76;
  const agents = [
    { name: 'Claude Code', home: SY - 120 },
    { name: 'Codex', home: SY },
    { name: 'Any CLI agent', home: SY + 120 },
  ];
  const B = (k) => 0.3 + k * 3.0; // each agent's turn
  const tr = (x, y) => ({ transform: `translate(${x}px,${y}px)` });

  // ---- the video and its notes ----
  const V = { x: 56, y: SY - 48, w: 262, h: 96 };
  out.push(rect(V.x, V.y, V.w, V.h, { r: 16 }) + icon.video(V.x + 18, SY - 21, 58, 42));
  out.push(text(V.x + 92, SY - 4, 'intro', { size: 24 }) + text(V.x + 92, SY + 22, '3 notes', { size: 18, weight: 500, fill: MUTED }));
  // Its version goes up by one each time an agent finishes.
  const bx = V.x + V.w - 70, by = SY - 16;
  const vers = ['v4', 'v5', 'v6', 'v7'];
  const shown = [[0, B(0) + 2.2], [B(0) + 2.2, B(1) + 2.2], [B(1) + 2.2, B(2) + 2.2], [B(2) + 2.2, RESET]];
  vers.forEach((v, i) => {
    const [on, off] = shown[i];
    const keys = i === 0
      ? [[0, { opacity: 1, transform: 'scale(1)' }], [off, { opacity: 1, transform: 'scale(1)' }, IN], [off + 0.05, { opacity: 0, transform: 'scale(1)' }], [RESET + 0.1, { opacity: 0, transform: 'scale(1)' }, EASE], [RESET + 0.4, { opacity: 1, transform: 'scale(1)' }]]
      : [[0, { opacity: 0, transform: 'scale(.7)' }], [on, { opacity: 0, transform: 'scale(.7)' }, POP], [on + 0.3, { opacity: 1, transform: 'scale(1)' }], [off, { opacity: 1, transform: 'scale(1)' }, IN], [off + 0.05, { opacity: 0, transform: 'scale(1)' }]];
    const filled = i > 0;
    out.push(at(bx + 27, by + 16, rect(-27, -16, 54, 32, { r: 16, fill: filled ? INK : PAPER, stroke: INK, sw: 2.5 }) + text(0, 7, v, { size: 19, anchor: 'middle', fill: filled ? PAPER : INK }), m.a(keys)));
  });
  out.push(arrow([[V.x + V.w + 12, SY], [P.x - 12, SY]]));
  // The notes go in, and the new version comes back (under the panel, so they pass into Retake).
  for (let k = 0; k < 3; k++) {
    for (let j = 0; j < 3; j++) {
      out.push(at(0, 0, rect(-9, -9, 18, 18, { r: 4, fill: PAPER, stroke: INK, sw: 2.5 }), m.a(travelKeys([[V.x + V.w + 16, SY], [P.x + 40, SY]], B(k) + 0.7 + j * 0.12, B(k) + 1.1 + j * 0.12, { fade: 0.04 }))));
    }
    out.push(at(0, 0, circle(0, 0, 8, { fill: INK, stroke: PAPER, sw: 2.5 }), m.a(travelKeys([[P.x + 30, SY], [bx - 8, SY]], B(k) + 1.8, B(k) + 2.2, { fade: 0.04 }))));
  }

  // ---- Retake, with its one socket ----
  out.push(rect(P.x, P.y, P.w, P.h, { r: 18 }) + wordmark(P.x + P.w / 2, P.y + 46, 28, 'middle'));
  const sx = P.x + P.w; // the socket opens on the panel's right edge
  out.push(rect(sx - 48, SY - 46, 48, 92, { r: 12, sw: 2.5 }));
  out.push(rect(sx - 36, SY - 23, 40, 14, { r: 3, fill: INK, stroke: 'none' }) + rect(sx - 36, SY + 9, 40, 14, { r: 3, fill: INK, stroke: 'none' }));
  // Its light: on while an agent is plugged in.
  out.push(circle(P.x + 40, SY, 8, { sw: 2.5 }));
  const lit = [[0, { opacity: 0 }]];
  for (let k = 0; k < 3; k++) lit.push([B(k) + 0.55, { opacity: 0 }, EASE], [B(k) + 0.7, { opacity: 1 }], [B(k) + 2.4, { opacity: 1 }, IN], [B(k) + 2.55, { opacity: 0 }]);
  out.push(at(P.x + 40, SY, circle(0, 0, 8, { fill: RED, stroke: RED, sw: 2.5 }), m.a(lit)));

  // ---- the agents: plugs that take turns in the one socket ----
  agents.forEach((a, k) => {
    const b = B(k);
    const prong = (y) => rect(-34, y, 36, 8, { r: 2, fill: PAPER, stroke: INK, sw: 2.2 });
    const body = prong(-20) + prong(12) + rect(0, -PLUG_H / 2, PLUG_W, PLUG_H, { r: 16 }) + icon.terminal(16, -14, 34, 28, { sw: 2.5 }) + text(62, 8, a.name, { size: 22 });
    // Along its own row, then to the socket's height, then in; back the same way.
    const home = tr(HOME_X, a.home), seat = tr(SEAT, SY);
    const keys = a.home === SY
      ? [[0, home], [b, home, EASE], [b + 0.7, seat], [b + 2.3, seat, EASE], [b + 2.95, home]]
      : [[0, home], [b, home, EASE], [b + 0.3, tr(STAGE_X, a.home)], [b + 0.32, tr(STAGE_X, a.home), EASE], [b + 0.55, tr(STAGE_X, SY)], [b + 0.57, tr(STAGE_X, SY), EASE], [b + 0.75, seat],
        [b + 2.3, seat, EASE], [b + 2.45, tr(STAGE_X, SY)], [b + 2.47, tr(STAGE_X, SY), EASE], [b + 2.67, tr(STAGE_X, a.home)], [b + 2.69, tr(STAGE_X, a.home), EASE], [b + 2.95, home]];
    out.push(at(0, 0, body, m.a(keys)));
  });

  // ---- how the agent is picked ----
  const PY = 534;
  out.push(text(56, PY + 7, 'Picked in', { size: 20, weight: 500, fill: MUTED }));
  const picks = [{ s: 'Settings: default' }, { s: 'on a video', dots: true }, { s: 'Hold Send: one send' }];
  let px = 56 + textWidth('Picked in', 20, 500) + 18;
  picks.forEach((p, k) => {
    const lw = textWidth(p.s, 20) + (p.dots ? 30 : 0);
    const w = lw + 36, x = px, h = 40;
    const content = (fill) => (p.dots ? icon.more(x + 26, PY, fill) : '') + text(x + 18 + (p.dots ? 30 : 0), PY + 7, p.s, { size: 20, fill });
    out.push(rect(x, PY - h / 2, w, h, { r: h / 2, sw: 2.5 }) + content(INK));
    const b = B(k);
    out.push(g(rect(x, PY - h / 2, w, h, { r: h / 2, fill: INK, stroke: INK, sw: 2.5 }) + content(PAPER),
      { cls: m.a([[0, { opacity: 0 }], [b, { opacity: 0 }, OUT], [b + 0.25, { opacity: 1 }], [b + 2.6, { opacity: 1 }, IN], [b + 2.85, { opacity: 0 }]]) }));
    px += w + 12;
  });

  out.push(text(56, 618, 'Any agent fits the same socket. One works on a video at a time.', { size: 24, weight: 500 }));

  return {
    name: 'any-agent',
    title: 'Bring your own agent',
    desc: 'Retake has one socket and any agent fits it. In turn, Claude Code (the default from Settings), Codex (picked for this video in its ⋯ menu) and any command-line agent (picked for one Send by holding Send) plug into the same socket, take the video\'s notes and send back the next version: v5, v6, v7.',
    motion: m,
    body: out.join('\n'),
  };
}
