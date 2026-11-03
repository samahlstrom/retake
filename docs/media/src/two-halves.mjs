// 2. Two halves: the browser (anywhere, a phone too), the Retake server (the page and the comments), and the editing
// machine, where the footage, the agent and the renderer are: your computer, or a box you host. The footage stays on
// that machine and renders there; the browser gets the rendered stream through the server. Local: the server and the
// editing machine are one program on your computer. Hosted: the server is online and the editing machine connects
// out to it.
import {
  Motion, INK, PAPER, MUTED, EASE, IN, LINEAR,
  rect, circle, path, g, at, text, arrow, travelKeys, heading, icon, wordmark,
} from './kit.mjs';

export default function twoHalves() {
  const m = new Motion({ duration: 10, stillAt: 8.6 });
  const TO_HOSTED = 3.6, TO_LOCAL = 9.0;
  const out = [];
  out.push(heading('Footage stays put and renders there.'));

  const hostedOnly = (on = TO_HOSTED + 0.2, fade = 0.35) => m.a([[0, { opacity: 0 }], [on, { opacity: 0 }, EASE], [on + fade, { opacity: 1 }], [TO_LOCAL, { opacity: 1 }, IN], [TO_LOCAL + 0.25, { opacity: 0 }]]);
  const localOnly = (back = TO_LOCAL + 0.2) => m.a([[0, { opacity: 1 }], [TO_HOSTED, { opacity: 1 }, IN], [TO_HOSTED + 0.25, { opacity: 0 }], [back, { opacity: 0 }, EASE], [back + 0.35, { opacity: 1 }]]);

  // ---- the toggle ----
  const T = { x: 904, y: 46, w: 240, h: 46 };
  out.push(rect(T.x, T.y, T.w, T.h, { r: 23 }));
  out.push(at(T.x + 5, T.y + 5, rect(0, 0, 112, 36, { r: 18, fill: INK, stroke: 'none' }),
    m.a([[0, { transform: 'translate(0px,0px)' }], [TO_HOSTED, { transform: 'translate(0px,0px)' }, EASE], [TO_HOSTED + 0.4, { transform: 'translate(118px,0px)' }], [TO_LOCAL, { transform: 'translate(118px,0px)' }, EASE], [TO_LOCAL + 0.4, { transform: 'translate(0px,0px)' }]])));
  const label = (s, cx, cls, fill) => g(text(cx, T.y + 30, s, { size: 21, anchor: 'middle', fill }), { cls });
  out.push(label('Local', T.x + 61, localOnly(TO_LOCAL + 0.1), PAPER), label('Local', T.x + 61, hostedOnly(TO_HOSTED + 0.1, 0.2), INK));
  out.push(label('Hosted', T.x + 179, hostedOnly(TO_HOSTED + 0.1, 0.2), PAPER), label('Hosted', T.x + 179, localOnly(TO_LOCAL + 0.1), INK));

  // ---- local: the server and the editing machine are one program on your computer ----
  out.push(g(rect(334, 146, 830, 412, { r: 24, dash: '1 9' }).replace('<rect', '<rect stroke-linecap="round"')
    + text(356, 178, 'One program on your computer', { size: 20, weight: 500, fill: MUTED }), { cls: localOnly() }));

  // ---- the browser ----
  const B = { x: 56, y: 248, w: 236, h: 230 };
  out.push(rect(B.x, B.y, B.w, B.h, { r: 18 }));
  out.push(icon.laptop(B.x + 28, B.y + 40, 116) + icon.phone(B.x + 162, B.y + 48, 42, 74));
  out.push(text(B.x + 26, B.y + 172, 'Browser', { size: 26 }) + text(B.x + 26, B.y + 204, 'laptop or phone', { size: 20, weight: 500, fill: MUTED }));

  // ---- the Retake server: the page and the comments ----
  const S = { x: 356, y: 302, w: 232, h: 124 };
  out.push(rect(S.x, S.y, S.w, S.h, { r: 16 }) + wordmark(S.x + S.w / 2, S.y + 56, 34, 'middle') + text(S.x + S.w / 2, S.y + 94, 'page + comments', { size: 19, weight: 500, fill: MUTED, anchor: 'middle' }));
  out.push(g(text(S.x + S.w / 2, S.y - 18, 'Online', { size: 20, weight: 500, fill: MUTED, anchor: 'middle' }), { cls: hostedOnly() }));

  // ---- the editing machine: footage, agent, render ----
  const M = { x: 724, y: 200, w: 420, h: 336 };
  out.push(rect(M.x, M.y, M.w, M.h, { r: 22 }));
  out.push(text(M.x + 26, M.y + 44, 'Editing machine', { size: 26 }) + text(M.x + 26, M.y + 74, 'your computer, or a box you host', { size: 19, weight: 500, fill: MUTED }));
  const rowY = M.y + 186;
  const ax = M.x + 80, rx = M.x + 210, fx = M.x + 340;
  out.push(icon.terminal(ax - 36, rowY - 27, 72, 54) + text(ax, rowY + 76, 'Agent', { size: 22, anchor: 'middle' }));
  out.push(icon.video(fx - 36, rowY - 26, 72, 52) + text(fx, rowY + 76, 'Footage', { size: 22, anchor: 'middle' }));
  // The renderer, next to the footage: its parts fill as it renders what someone watches.
  const cells = [-40, -12, 16];
  const renders = [[1.4, 2.0, 3.3], [5.6, 6.2, TO_LOCAL]];
  cells.forEach((dx, i) => {
    out.push(rect(rx + dx, rowY - 20, 24, 40, { r: 5, sw: 2.5 }));
    const keys = [[0, { transform: 'scale(0,1)' }]];
    for (const [r0, r1, hold] of renders) {
      const t0 = r0 + i * ((r1 - r0) / 3);
      keys.push([t0, { transform: 'scale(0,1)' }, LINEAR], [t0 + (r1 - r0) / 3, { transform: 'scale(1,1)' }], [hold, { transform: 'scale(1,1)' }, IN], [hold + 0.3, { transform: 'scale(0,1)' }]);
    }
    out.push(at(rx + dx, rowY - 20, rect(0, 0, 24, 40, { r: 5, fill: INK, stroke: INK, sw: 2.5 }), m.a(keys)));
  });
  out.push(text(rx, rowY + 76, 'Render', { size: 22, anchor: 'middle' }));
  out.push(arrow([[ax + 46, rowY], [rx - 54, rowY]]) + arrow([[fx - 46, rowY], [rx + 50, rowY]]));

  // ---- the connections ----
  const cy = S.y + S.h / 2;
  out.push(arrow([[B.x + B.w + 10, cy], [S.x - 10, cy]], { both: true }));
  // Local: one program, so a plain link. Hosted: the editing machine connects out to the server.
  out.push(g(path(`M${S.x + S.w + 10} ${cy}H${M.x - 10}`), { cls: localOnly() }));
  out.push(g(arrow([[M.x - 10, cy], [S.x + S.w + 10, cy]]) + text((S.x + S.w + M.x) / 2, cy + 34, 'connects out', { size: 18, weight: 500, fill: MUTED, anchor: 'middle' }), { cls: hostedOnly() }));

  // ---- someone watches: the browser asks, the machine renders, the stream comes back through the server ----
  const ask = circle(0, 0, 8, { fill: INK, stroke: PAPER, sw: 2.5 });
  const chip = rect(-15, -11, 30, 22, { r: 4, fill: INK, stroke: PAPER, sw: 2 }) + path('M-4 -6L6 0L-4 6Z', { fill: PAPER, stroke: 'none' });
  const lane = (pts, t0, t1, body) => at(0, 0, body, m.a(travelKeys(pts, t0, t1, { fade: 0.06 })));
  const toServer = [[B.x + B.w + 14, cy], [S.x - 8, cy]];
  const toMachine = [[S.x + S.w + 8, cy], [M.x + 6, cy], [rx, rowY - 34]];
  for (const [r0, r1] of renders) {
    out.push(lane(toServer, r0 - 0.75, r0 - 0.45, ask), lane(toMachine, r0 - 0.4, r0, ask));
    out.push(lane([...toMachine].reverse(), r1, r1 + 0.45, chip), lane([...toServer].reverse(), r1 + 0.5, r1 + 0.85, chip));
  }

  // ---- the caption ----
  out.push(g(text(56, 618, 'Local: one program on your computer, footage and all.', { size: 24, weight: 500 }), { cls: localOnly() }));
  out.push(g(text(56, 618, 'Hosted: the page is online. The editing machine connects out.', { size: 24, weight: 500 }), { cls: hostedOnly() }));

  return {
    name: 'two-halves',
    title: 'Local or hosted: the browser, the Retake server and the editing machine',
    desc: 'Three parts: the browser on a laptop or phone, the Retake server with the page and the comments, and the editing machine (your computer, or a box you host) with the footage, the agent and the renderer. The footage stays on the editing machine and renders there; the browser asks for what it plays and gets the rendered stream through the server. Local: the server and the editing machine are one program on your computer. Hosted: the server is online and the editing machine connects out to it.',
    motion: m,
    body: out.join('\n'),
  };
}
