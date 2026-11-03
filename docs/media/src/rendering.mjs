// 7. The rendering engine: the timeline is cut into parts; an edit makes only its parts stale; the cache keeps the
// rest; several videos render at once on one computer; the browser plays the result while it renders.
import {
  Motion, INK, PAPER, RED, MUTED, EASE, IN, LINEAR, STEP,
  rect, circle, path, g, at, text, arrow, heading, icon, showKeys,
} from './kit.mjs';

export default function rendering() {
  const m = new Motion({ duration: 10, stillAt: 4.6 });
  const RESET = 9.3;
  const out = [];
  out.push(heading('Renders only what changed.'));
  out.unshift(`<defs><pattern id="hatch" width="9" height="9" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><path d="M0 0V9" stroke="${INK}" stroke-width="2.5"/></pattern></defs>`);

  // A row of parts; `stale` maps a part to [when it goes stale, render start, render end].
  const row = ({ name, x, y, count, w, h, gap, stale, labels }) => {
    out.push(text(x, y - 14, name, { size: 22 }));
    for (let i = 0; i < count; i++) {
      const cx = x + i * (w + gap);
      out.push(rect(cx, y, w, h, { r: 8, sw: 2.5 }));
      const s = stale[i];
      if (s) {
        const [t0, r0, r1] = s;
        out.push(g(rect(cx, y, w, h, { r: 8, fill: 'url(#hatch)', stroke: INK, sw: 2.5 }), { cls: m.a(showKeys(t0, r1, 0.15, 0.05)) }));
        out.push(at(cx, y, rect(0, 0, w, h, { r: 8, fill: INK, stroke: INK, sw: 2.5 }),
          m.a([[0, { transform: 'scale(0,1)', opacity: 1 }], [r0, { transform: 'scale(0,1)', opacity: 1 }, LINEAR], [r1, { transform: 'scale(1,1)', opacity: 1 }], [RESET, { transform: 'scale(1,1)', opacity: 1 }, IN], [RESET + 0.3, { transform: 'scale(1,1)', opacity: 0 }]])));
      }
      if (labels) {
        const word = s ? 'render' : 'cache';
        out.push(g(text(cx + w / 2, y + h + 30, word, { size: 18, weight: s ? 600 : 500, fill: s ? INK : MUTED, anchor: 'middle' }), { cls: m.a(showKeys(1.0 + i * 0.04, RESET)) }));
      }
    }
  };

  // The video you edited: parts 3 and 4 change.
  out.push(rect(52, 112, 728, 440, { r: 22, dash: '1 9' }).replace('<rect', '<rect stroke-linecap="round"') + text(76, 146, 'Your computer', { size: 20, weight: 500, fill: MUTED }));
  const A = { x: 76, y: 204, w: 78, h: 64, gap: 8 };
  row({ name: 'intro', ...A, count: 8, stale: { 2: [0.8, 1.3, 2.3], 3: [0.8, 2.3, 3.3] }, labels: true });
  // The pencil: the agent's edit lands on those parts.
  const px = A.x + 3 * (A.w + A.gap) - A.gap / 2;
  out.push(at(px, A.y - 6, path('M-6 -40L6 -40L6 -8L0 0L-6 -8Z', { fill: PAPER, sw: 2.5 }) + path('M-6 -32H6', { sw: 2.5 }),
    m.a([[0, { opacity: 0, transform: 'translate(0px,-24px)' }], [0.3, { opacity: 0, transform: 'translate(0px,-24px)' }, EASE], [0.75, { opacity: 1, transform: 'translate(0px,0px)' }], [1.3, { opacity: 1, transform: 'translate(0px,0px)' }, IN], [1.6, { opacity: 0, transform: 'translate(0px,-10px)' }]])));

  // Other videos render at the same time.
  const small = { w: 60, h: 40, gap: 8, count: 10 };
  row({ name: 'promo', x: 76, y: 382, ...small, stale: { 4: [0.9, 1.4, 2.6], 5: [0.9, 2.6, 3.6] } });
  row({ name: 'tutorial', x: 76, y: 480, ...small, stale: { 1: [1.0, 1.5, 2.5], 7: [1.0, 2.8, 3.8] } });

  // The browser plays it while it renders.
  const B = { x: 824, y: 166, w: 320, h: 340 };
  out.push(icon.browser(B.x, B.y, B.w, B.h, { r: 16, bar: 40 }));
  const live = m.a(showKeys(1.2, 3.8, 0.2, 0.3));
  out.push(g(circle(B.x + B.w - 74, B.y + 20, 6, { fill: RED, stroke: 'none' }) + text(B.x + B.w - 62, B.y + 27, 'live', { size: 18 }), { cls: live }));
  const F = { x: B.x + 18, y: B.y + 58, w: B.w - 36, h: 166 };
  out.push(rect(F.x, F.y, F.w, F.h, { r: 8 }));
  out.push(`<clipPath id="pf"><rect x="${F.x}" y="${F.y}" width="${F.w}" height="${F.h}" rx="8"/></clipPath>`);
  out.push(`<g clip-path="url(#pf)">${circle(F.x + F.w - 60, F.y + 52, 18)}${path(`M${F.x} ${F.y + 130}Q${F.x + 80} ${F.y + 86} ${F.x + 150} ${F.y + 118}Q${F.x + 220} ${F.y + 146} ${F.x + F.w} ${F.y + 104}V${F.y + F.h}H${F.x}Z`, { fill: PAPER })}</g>`);
  // The bar: grey up to what has rendered, black up to the playhead.
  const by = F.y + F.h + 40, bw = F.w;
  out.push(path(`M${F.x} ${by}H${F.x + bw}`, { sw: 4, stroke: '#e2e2e2' }));
  const buf = (f) => ({ transform: `scale(${f},1)` });
  out.push(at(F.x, by, path(`M0 0H${bw}`, { sw: 4, stroke: '#9c9c9c' }),
    m.a([[0, buf(0)], [1.2, buf(0), STEP], [1.21, buf(0.25), LINEAR], [2.3, buf(0.375), LINEAR], [3.3, buf(0.5), STEP], [3.31, buf(1)], [RESET, buf(1), EASE], [RESET + 0.4, buf(0)]])));
  const PLAY0 = 1.3, PLAY1 = 8.6;
  out.push(at(F.x, by, path(`M0 0H${bw}`, { sw: 4 }),
    m.a([[0, buf(0)], [PLAY0, buf(0), LINEAR], [PLAY1, buf(1)], [RESET, buf(1), EASE], [RESET + 0.4, buf(0)]])));
  out.push(at(F.x, by, circle(0, 0, 8, { fill: INK, stroke: 'none' }),
    m.a([[0, { transform: 'translate(0px,0px)' }], [PLAY0, { transform: 'translate(0px,0px)' }, LINEAR], [PLAY1, { transform: `translate(${bw}px,0px)` }], [RESET, { transform: `translate(${bw}px,0px)` }, EASE], [RESET + 0.4, { transform: 'translate(0px,0px)' }]])));
  out.push(text(B.x + B.w / 2, by + 44, 'plays while it renders', { size: 19, weight: 500, fill: MUTED, anchor: 'middle' }));
  out.push(arrow([[A.x + 8 * (A.w + A.gap) - A.gap + 10, A.y + A.h / 2], [B.x - 10, A.y + A.h / 2]]));

  out.push(text(56, 624, 'An edit re-renders only its parts. Several videos render at once.', { size: 24, weight: 500 }));

  return {
    name: 'rendering',
    title: 'The rendering engine',
    desc: 'The video\'s timeline is cut into parts. An edit lands on parts 3 and 4, so only they render again; the cache keeps the other six. Two other videos render their own changed parts at the same time on the same computer. The browser plays the new version while it renders, with the rendered part of the bar staying ahead of the playhead.',
    motion: m,
    body: out.join('\n'),
  };
}
