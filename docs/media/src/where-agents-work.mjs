// 5. Where agents work: herdr (bundled), tmux, or in the background (one per video, ten at once). Jump into a
// session and talk to the agent; its questions come back to the video at the exact shot.
import {
  Motion, INK, PAPER, MUTED, EASE, IN, LINEAR, STEP, rect, circle, path, g, at, text, heading, icon, popKeys,
  travelKeys, textWidth, n,
} from './kit.mjs';

export default function whereAgentsWork() {
  const m = new Motion({ duration: 10, stillAt: 3.4 });
  const TO_TMUX = 3.9, TO_BG = 6.4, TO_HERDR = 9.3, X = 0.35;
  const RESET = 9.3;
  const out = [];
  out.push(heading('Agents run where you like.'));

  // Which tab: 0 herdr, 1 tmux, 2 background.
  const phase = (v0, v1, v2) => m.a([[0, v0], [TO_TMUX, v0, EASE], [TO_TMUX + X, v1], [TO_BG, v1, EASE], [TO_BG + X, v2], [TO_HERDR, v2, EASE], [TO_HERDR + X, v0]]);
  // One window's content at a time: the old one fades out, then the new one fades in.
  const only = (i) => {
    const switches = [[TO_TMUX, 0, 1], [TO_BG, 1, 2], [TO_HERDR, 2, 0]];
    const k = [[0, { opacity: i === 0 ? 1 : 0 }]];
    for (const [t, from, to] of switches) {
      if (from === i) k.push([t, { opacity: 1 }, IN], [t + 0.18, { opacity: 0 }]);
      if (to === i) k.push([t + 0.15, { opacity: 0 }, EASE], [t + 0.45, { opacity: 1 }]);
    }
    return m.a(k);
  };

  // ---- the tabs ----
  const T = { x: 500, y: 120, w: 644, h: 50 };
  const segW = (T.w - 10) / 3;
  out.push(rect(T.x, T.y, T.w, T.h, { r: 25 }));
  out.push(at(T.x + 5, T.y + 5, rect(0, 0, segW, T.h - 10, { r: 20, fill: INK, stroke: 'none' }),
    phase(...[0, 1, 2].map((k) => ({ transform: `translate(${n(k * segW)}px,0px)` })))));
  ['herdr · bundled', 'tmux', 'Background'].forEach((s, i) => {
    const cx = T.x + 5 + segW * i + segW / 2;
    out.push(g(text(cx, T.y + 32, s, { size: 21, anchor: 'middle', fill: PAPER }), { cls: only(i) }));
    out.push(g(text(cx, T.y + 32, s, { size: 21, anchor: 'middle' }), { cls: phase(...[0, 1, 2].map((k) => ({ opacity: k === i ? 0 : 1 }))) }));
  });

  // ---- the window the agents run in ----
  const Wd = { x: 500, y: 196, w: 644, h: 364 };
  out.push(rect(Wd.x, Wd.y, Wd.w, Wd.h, { r: 18 }));
  const line = (x, y, w, cls) => at(x, y, rect(0, -4, w, 8, { r: 4, fill: INK, stroke: 'none', opacity: 0.85 }), cls);
  const grow = (t) => m.a([[0, { transform: 'scale(0,1)' }], [t, { transform: 'scale(0,1)' }, LINEAR], [t + 0.25, { transform: 'scale(1,1)' }], [RESET, { transform: 'scale(1,1)' }, STEP], [RESET + 0.01, { transform: 'scale(0,1)' }]]);

  // herdr: workspaces on the left, the agent's session on the right.
  let herdr = path(`M${Wd.x + 200} ${Wd.y}V${Wd.y + Wd.h}`, { sw: 2.5 });
  ['intro', 'promo', 'tutorial'].forEach((s, i) => {
    const y = Wd.y + 22 + i * 54;
    if (i === 0) herdr += rect(Wd.x + 12, y, 176, 42, { r: 10, fill: INK, stroke: 'none' });
    herdr += text(Wd.x + 28, y + 28, `retake · ${s}`, { size: 18, fill: i === 0 ? PAPER : INK });
  });
  const P = { x: Wd.x + 224, y: Wd.y + 40 };
  herdr += icon.terminal(P.x, P.y - 22, 36, 30, { sw: 2.5 }) + text(P.x + 50, P.y, 'Claude Code · intro', { size: 20, weight: 500, fill: MUTED });
  herdr += line(P.x, P.y + 40, 300, '') + line(P.x, P.y + 64, 220, '') + line(P.x, P.y + 88, 260, '');
  // You type into it.
  const msg = 'make the intro shorter';
  herdr += rect(P.x - 4, P.y + 112, 396, 46, { r: 10, sw: 2.5 }) + text(P.x + 14, P.y + 142, `› ${msg}`, { size: 20 });
  const tw = textWidth(msg, 20) + 14;
  herdr += at(P.x + 28 + tw, P.y + 116, rect(-tw, 0, tw, 38, { fill: PAPER, stroke: 'none', r: 0 }),
    m.a([[0, { transform: 'scale(1,1)' }], [1.2, { transform: 'scale(1,1)' }, LINEAR], [2.1, { transform: 'scale(0,1)' }], [RESET, { transform: 'scale(0,1)' }, STEP], [RESET + 0.01, { transform: 'scale(1,1)' }]]));
  // It answers by asking about a shot.
  herdr += g(text(P.x, P.y + 200, 'retake ask --at 1:23', { size: 20 }) + text(P.x, P.y + 228, '"Which logo?"', { size: 20, weight: 500, fill: MUTED }),
    { cls: m.a([[0, { opacity: 0 }], [2.3, { opacity: 0 }, EASE], [2.5, { opacity: 1 }], [RESET, { opacity: 1 }, STEP], [RESET + 0.01, { opacity: 0 }]]) });
  out.push(g(herdr, { cls: only(0) }));

  // tmux: one window per video, the status bar along the bottom.
  let tmux = rect(Wd.x, Wd.y + Wd.h - 44, Wd.w, 44, { r: 0, fill: INK, stroke: 'none' }).replace('<rect', `<rect clip-path="url(#win)"`);
  tmux += text(Wd.x + 20, Wd.y + Wd.h - 15, '[retake]  0:intro  1:promo*  2:tutorial', { size: 19, fill: PAPER });
  tmux += icon.terminal(Wd.x + 24, Wd.y + 22, 36, 30, { sw: 2.5 }) + text(Wd.x + 74, Wd.y + 44, 'Codex · promo', { size: 20, weight: 500, fill: MUTED });
  [340, 260, 300, 200, 280].forEach((w, i) => { tmux += line(Wd.x + 26, Wd.y + 92 + i * 30, w, grow(TO_TMUX + 0.4 + i * 0.25)); });
  out.push(g(tmux, { cls: only(1) }));

  // Background: one agent per video, ten at once.
  let bg = '';
  const names = ['intro', 'promo', 'tutorial', 'trailer', 'recap', 'teaser', 'vlog', 'ad', 'short', 'demo'];
  names.forEach((s, i) => {
    const col = i % 5, row = Math.floor(i / 5);
    const x = Wd.x + 18 + col * 124, y = Wd.y + 22 + row * 166;
    bg += rect(x, y, 112, 150, { r: 12, sw: 2.5 }) + icon.terminal(x + 14, y + 14, 30, 24, { sw: 2.2 }) + text(x + 14, y + 66, s, { size: 18 });
    const tDone = TO_BG + 0.8 + ((i * 7) % 10) * 0.18;
    bg += line(x + 14, y + 92, 72, '') + line(x + 14, y + 110, 54, '');
    const spin = m.a([[0, { transform: 'rotate(0deg)' }], [1, { transform: 'rotate(360deg)' }, LINEAR]], { period: 1 });
    bg += g(at(x + 92, y + 130, path('M0 -7C3.9 -7 7 -3.9 7 0C7 3.9 3.9 7 0 7C-3.9 7 -7 3.9 -7 0', { sw: 2.2 }), spin),
      { cls: m.a([[0, { opacity: 1 }], [tDone, { opacity: 1 }, STEP], [tDone + 0.01, { opacity: 0 }]]) });
    bg += at(x + 92, y + 130, circle(0, 0, 9, { fill: INK, stroke: 'none' }) + icon.check(0, 0.5, 4.5, { sw: 2, stroke: PAPER }), m.a(popKeys(tDone, RESET, 0.25)));
  });
  out.push(g(bg, { cls: only(2) }));
  out.unshift(`<defs><clipPath id="win"><rect x="${Wd.x}" y="${Wd.y}" width="${Wd.w}" height="${Wd.h}" rx="18"/></clipPath></defs>`);
  out.push(rect(Wd.x, Wd.y, Wd.w, Wd.h, { r: 18, fill: 'none' }));

  // ---- the video, where the question lands ----
  const V = { x: 56, y: 196, w: 396, h: 364 };
  out.push(rect(V.x, V.y, V.w, V.h, { r: 18 }) + text(V.x + 24, V.y + 40, 'intro', { size: 24 }));
  const F = { x: V.x + 22, y: V.y + 60, w: 352, h: 198 };
  out.push(rect(F.x, F.y, F.w, F.h, { r: 10 }));
  // The frame at 1:23: two logos.
  out.push(rect(F.x + 40, F.y + 60, 90, 70, { r: 10, sw: 2.5 }) + circle(F.x + 85, F.y + 95, 18, { fill: INK, stroke: 'none' }));
  out.push(rect(F.x + 220, F.y + 60, 90, 70, { r: 10, sw: 2.5 }) + path(`M${F.x + 247} ${F.y + 112}L${F.x + 265} ${F.y + 78}L${F.x + 283} ${F.y + 112}Z`, { fill: INK, stroke: 'none' }));
  const LAND = 3.15;
  const landed = m.a([[0, { opacity: 0 }], [LAND, { opacity: 0 }, EASE], [LAND + 0.25, { opacity: 1 }], [RESET, { opacity: 1 }, IN], [RESET + 0.25, { opacity: 0 }]]);
  out.push(g(rect(F.x + 30, F.y + 50, 110, 90, { r: 12, fill: 'none', dash: '8 7', sw: 3 }) + rect(F.x + 210, F.y + 50, 110, 90, { r: 12, fill: 'none', dash: '8 7', sw: 3 }), { cls: landed }));
  const sy = F.y + F.h + 30;
  out.push(path(`M${F.x} ${sy}H${F.x + F.w}`, { sw: 3, stroke: '#d4d4d4' }) + path(`M${F.x} ${sy}H${F.x + 230}`, { sw: 3 }) + circle(F.x + 230, sy, 8, { fill: INK, stroke: 'none' }));
  out.push(at(F.x + 230, sy - 20, path('M0 -9L9 0L0 9L-9 0Z', { fill: INK, stroke: 'none' }), m.a(popKeys(LAND, RESET))));
  out.push(g(rect(F.x, sy + 18, F.w, 46, { r: 12, sw: 2.5 }) + circle(F.x + 24, sy + 41, 12, { fill: INK, stroke: 'none' }) + text(F.x + 24, sy + 47, '?', { size: 17, anchor: 'middle', fill: PAPER })
    + text(F.x + 46, sy + 48, 'Which logo?', { size: 20 }) + text(F.x + F.w - 16, sy + 48, '1:23', { size: 19, weight: 500, fill: MUTED, anchor: 'end' }), { cls: landed }));
  // The question travels from the agent's session to the shot.
  out.push(at(0, 0, circle(0, 0, 13, { fill: INK, stroke: PAPER, sw: 2.5 }) + text(0, 6, '?', { size: 17, anchor: 'middle', fill: PAPER }),
    m.a(travelKeys([[P.x - 10, P.y + 196], [Wd.x - 24, F.y + F.h + 10], [F.x + 230, sy - 20]], 2.55, LAND))));

  out.push(text(56, 618, 'In herdr or tmux, jump in and talk to any agent. Questions land on the shot.', { size: 24, weight: 500 }));

  return {
    name: 'where-agents-work',
    title: 'Where the agents run: herdr, tmux or the background',
    desc: 'Agents run in herdr (bundled), in tmux, or in the background, one per video and many at once. In herdr or tmux you can open an agent\'s session and type to it. When an agent is unsure it asks about a moment, and the question lands on the video at that shot (1:23) with the agent\'s dashed boxes around the two logos.',
    motion: m,
    body: out.join('\n'),
  };
}
