// 2. Two halves: the browser (anywhere, a phone too), the Retake server, and your computer with the footage and the
// agent. Local: Retake is one program on your computer. Hosted: the server is online and your computer connects
// out; only the video someone watches is relayed, and the footage never uploads.
import {
  Motion, INK, PAPER, MUTED, EASE, IN,
  rect, circle, path, g, at, text, arrow, travelKeys, heading, icon, wordmark, n,
} from './kit.mjs';

export default function twoHalves() {
  const m = new Motion({ duration: 10, stillAt: 8.6 });
  const TO_HOSTED = 3.6, TO_LOCAL = 9.0, MOVE = 0.7;
  const out = [];
  out.push(heading('Review anywhere. Footage stays home.'));

  // Which mode: 1 while hosted, with the moves eased.
  const hosted = (v0, v1) => m.a([[0, v0], [TO_HOSTED, v0, EASE], [TO_HOSTED + MOVE, v1], [TO_LOCAL, v1, EASE], [TO_LOCAL + MOVE, v0]]);
  const hostedOnly = (on = TO_HOSTED + MOVE, fade = 0.3) => m.a([[0, { opacity: 0 }], [on, { opacity: 0 }, EASE], [on + fade, { opacity: 1 }], [TO_LOCAL, { opacity: 1 }, IN], [TO_LOCAL + 0.25, { opacity: 0 }]]);
  const localOnly = (back = TO_LOCAL + 0.5) => m.a([[0, { opacity: 1 }], [TO_HOSTED, { opacity: 1 }, IN], [TO_HOSTED + 0.25, { opacity: 0 }], [back, { opacity: 0 }, EASE], [back + 0.3, { opacity: 1 }]]);

  // ---- the toggle ----
  const T = { x: 904, y: 46, w: 240, h: 46 };
  out.push(rect(T.x, T.y, T.w, T.h, { r: 23 }));
  out.push(at(T.x + 5, T.y + 5, rect(0, 0, 112, 36, { r: 18, fill: INK, stroke: 'none' }), hosted({ transform: 'translate(0px,0px)' }, { transform: 'translate(118px,0px)' })));
  const label = (s, cx, cls, fill) => g(text(cx, T.y + 30, s, { size: 21, anchor: 'middle', fill }), { cls });
  out.push(label('Local', T.x + 61, localOnly(TO_LOCAL + 0.3), PAPER), label('Local', T.x + 61, hostedOnly(TO_HOSTED + 0.3, 0.2), INK));
  out.push(label('Hosted', T.x + 179, hostedOnly(TO_HOSTED + 0.3, 0.2), PAPER), label('Hosted', T.x + 179, localOnly(TO_LOCAL + 0.3), INK));

  // ---- the browser ----
  const B = { x: 56, y: 250, w: 244, h: 250 };
  out.push(rect(B.x, B.y, B.w, B.h, { r: 18 }));
  out.push(icon.laptop(B.x + 34, B.y + 46, 120) + icon.phone(B.x + 170, B.y + 54, 44, 76));
  out.push(text(B.x + 28, B.y + 186, 'Browser', { size: 26 }) + text(B.x + 28, B.y + 218, 'laptop or phone', { size: 20, weight: 500, fill: MUTED }));

  // ---- your computer ----
  const C = { x: 740, y: 200, w: 404, h: 350 };
  out.push(rect(C.x, C.y, C.w, C.h, { r: 22 }));
  out.push(text(C.x + 28, C.y + 46, 'Your computer', { size: 26 }));
  const iy = C.y + 216;
  out.push(icon.video(C.x + 60, iy, 76, 54) + text(C.x + 98, iy + 90, 'Footage', { size: 22, anchor: 'middle' })
    + text(C.x + 98, iy + 116, 'never uploads', { size: 18, weight: 500, fill: MUTED, anchor: 'middle' }));
  out.push(icon.terminal(C.x + 268, iy, 76, 54) + text(C.x + 306, iy + 90, 'Agent', { size: 22, anchor: 'middle' }));

  // ---- Retake: inside your computer (local), or online (hosted) ----
  const R = { w: 236, h: 124 };
  const local = { x: C.x + C.w / 2 - R.w / 2, y: C.y + 70 };
  const online = { x: 362, y: 196 };
  const box = rect(0, 0, R.w, R.h, { r: 16 }) + wordmark(R.w / 2, 56, 34, 'middle') + text(R.w / 2, 94, 'page + comments', { size: 19, weight: 500, fill: MUTED, anchor: 'middle' });
  out.push(at(local.x, local.y, box, hosted({ transform: 'translate(0px,0px)' }, { transform: `translate(${n(online.x - local.x)}px,${n(online.y - local.y)}px)` })));
  out.push(g(text(online.x + R.w / 2, online.y - 18, 'On a server, online', { size: 19, weight: 500, fill: MUTED, anchor: 'middle' }), { cls: hostedOnly() }));

  // Local: the browser opens Retake on this computer.
  const ly = local.y + R.h / 2;
  out.push(g(arrow([[B.x + B.w + 10, ly], [local.x - 10, ly]], { both: true }), { cls: localOnly() }));
  // Hosted: your computer connects out; the browser talks to the server.
  const c0 = [C.x - 10, C.y + 120], c1 = [online.x + R.w + 10, online.y + 84];
  const b0 = [B.x + B.w + 10, B.y + 60], b1 = [online.x - 10, online.y + 84];
  out.push(g(arrow([c0, c1]), { cls: hostedOnly() }));
  out.push(g(text((c0[0] + c1[0]) / 2, c0[1] + 34, 'connects out', { size: 19, weight: 500, fill: MUTED, anchor: 'middle' }), { cls: hostedOnly() }));
  out.push(g(arrow([b0, b1], { both: true }), { cls: hostedOnly() }));

  // The video someone watches: asked for, then sent from your computer (local), or relayed by the server (hosted).
  const chip = rect(-15, -11, 30, 22, { r: 4, fill: INK, stroke: PAPER, sw: 2 }) + path('M-4 -6L6 0L-4 6Z', { fill: PAPER, stroke: 'none' });
  const ask = circle(0, 0, 8, { fill: INK, stroke: PAPER, sw: 2.5 });
  const lane = (a, b, t0, t1, body) => at(0, 0, body, m.a(travelKeys([a, b], t0, t1)));
  out.push(lane([B.x + B.w + 14, ly], [local.x - 14, ly], 0.8, 1.4, ask));
  out.push(lane([local.x - 14, ly], [B.x + B.w + 14, ly], 1.6, 2.3, chip));
  out.push(lane(b0, b1, 5.0, 5.4, ask), lane(c1, c0, 5.5, 5.9, ask));
  out.push(lane(c0, c1, 6.2, 6.6, chip), lane(b1, b0, 6.7, 7.1, chip));

  // ---- the caption ----
  out.push(g(text(56, 618, 'Local: Retake runs on your computer. Nothing to connect.', { size: 24, weight: 500 }), { cls: localOnly() }));
  out.push(g(text(56, 618, 'Hosted: the server is online. Your computer connects out.', { size: 24, weight: 500 }), { cls: hostedOnly() }));

  return {
    name: 'two-halves',
    title: 'Local or hosted: the browser, the Retake server and your computer',
    desc: 'Three parts: the browser on a laptop or phone, the Retake server with the page and the comments, and your computer with the footage and the agent. Local: Retake runs on your computer, nothing to connect. Hosted: the server is online, your computer connects out to it, and only the video someone watches is relayed; the footage never uploads.',
    motion: m,
    body: out.join('\n'),
  };
}
