// 4. Pick the model: what powers the agent (a Claude or ChatGPT subscription, an API key, OpenRouter for any model)
// is set once in Settings, and holding Send picks another agent for one send. Everyone uses their own account.
import {
  Motion, INK, PAPER, MUTED, EASE, IN, STEP,
  rect, circle, path, g, at, text, heading, icon, n,
} from './kit.mjs';

export default function pickModel() {
  const m = new Motion({ duration: 10, stillAt: 4.8 });
  const RESET = 9.2;
  const out = [];
  out.push(heading('Any model. Your own account.'));

  // ---- Settings ----
  const S = { x: 56, y: 128, w: 560, h: 432 };
  out.push(rect(S.x, S.y, S.w, S.h, { r: 20 }));
  out.push(icon.gear(S.x + 40, S.y + 46, 17) + text(S.x + 70, S.y + 56, 'Settings', { size: 28 }));
  out.push(path(`M${S.x} ${S.y + 88}H${S.x + S.w}`, { sw: 2.5 }));
  out.push(text(S.x + 28, S.y + 128, 'What powers your agent', { size: 20, weight: 500, fill: MUTED }));
  const rows = [['Claude subscription', 'Claude Code'], ['ChatGPT subscription', 'Codex'], ['API key', 'any provider'], ['OpenRouter', 'any model']];
  const rowY = (i) => S.y + 150 + i * 66;
  const RH = 56;
  // The chosen row: outlined, and its radio filled; it moves from the first row to the second when clicked.
  const PICK = 1.25;
  const sel = (keysFor) => m.a([[0, keysFor(0)], [PICK, keysFor(0), EASE], [PICK + 0.35, keysFor(1)], [RESET, keysFor(1), EASE], [RESET + 0.5, keysFor(0)]]);
  out.push(at(S.x + 16, rowY(0), rect(0, 0, S.w - 32, RH, { r: 12, sw: 2.5 }), sel((i) => ({ transform: `translate(0px,${i * 66}px)` }))));
  rows.forEach(([main, sub], i) => {
    const cy = rowY(i) + RH / 2;
    out.push(circle(S.x + 46, cy, 11, { sw: 2.5 }) + text(S.x + 72, cy + 8, main, { size: 24 }) + text(S.x + S.w - 36, cy + 7, sub, { size: 20, weight: 500, fill: MUTED, anchor: 'end' }));
  });
  out.push(at(S.x + 46, rowY(0) + RH / 2, circle(0, 0, 6, { fill: INK, stroke: 'none' }), sel((i) => ({ transform: `translate(0px,${i * 66}px)` }))));

  // ---- Send, held: pick an agent for this one send ----
  const B = { x: 780, y: 440, w: 300, h: 60 };
  const HOLD = 3.4, OPEN = 3.95, CLICK = 5.0, SENT = 5.25, BACK = 8.2;
  out.push(rect(B.x, B.y, B.w, B.h, { r: 30 }));
  const label = (s, cls, fill = INK) => g(text(B.x + B.w / 2, B.y + 39, s, { size: 24, anchor: 'middle', fill }), { cls });
  out.push(label('Send', m.a([[0, { opacity: 1 }], [SENT, { opacity: 1 }, EASE], [SENT + 0.2, { opacity: 0 }], [BACK, { opacity: 0 }, EASE], [BACK + 0.3, { opacity: 1 }]])));
  out.push(g(rect(B.x, B.y, B.w, B.h, { r: 30, fill: INK }) + text(B.x + B.w / 2, B.y + 38, 'Sent with Claude Code', { size: 22, anchor: 'middle', fill: PAPER }),
    { cls: m.a([[0, { opacity: 0 }], [SENT, { opacity: 0 }, EASE], [SENT + 0.2, { opacity: 1 }], [BACK, { opacity: 1 }, EASE], [BACK + 0.3, { opacity: 0 }]]) }));
  // The hold: a ring fills around the button for half a second.
  const ringD = (() => {
    const x = B.x - 8, y = B.y - 8, w = B.w + 16, h = B.h + 16, r = h / 2;
    return `M${x + w / 2} ${y}H${x + w - r}C${x + w - r + 0.5523 * r} ${y} ${x + w} ${y + r - 0.5523 * r} ${x + w} ${y + r}C${x + w} ${y + r + 0.5523 * r} ${x + w - r + 0.5523 * r} ${y + h} ${x + w - r} ${y + h}H${x + r}C${x + r - 0.5523 * r} ${y + h} ${x} ${y + r + 0.5523 * r} ${x} ${y + r}C${x} ${y + r - 0.5523 * r} ${x + r - 0.5523 * r} ${y} ${x + r} ${y}Z`;
  })();
  const L = 2 * (B.w + 16 - (B.h + 16)) + Math.PI * (B.h + 16) + 4;
  out.push(path(ringD, { sw: 4, cls: m.a([[0, { 'stroke-dashoffset': n(L), opacity: 0 }], [HOLD, { 'stroke-dashoffset': n(L), opacity: 1 }, 'linear'], [OPEN, { 'stroke-dashoffset': 0, opacity: 1 }], [OPEN + 0.15, { 'stroke-dashoffset': 0, opacity: 0 }]]) })
    .replace('<path', `<path stroke-dasharray="${n(L)} ${n(L)}"`));
  out.push(text(B.x + B.w / 2, B.y + B.h + 46, 'Hold Send to pick for one send', { size: 20, weight: 500, fill: MUTED, anchor: 'middle' }));

  // The list of agents this computer offers.
  const items = ['Codex', 'Claude Code', 'OpenRouter'];
  const M = { x: B.x, y: 196, w: B.w, h: 220 };
  const pop = m.a([[0, { opacity: 0, transform: 'translate(0px,10px)' }], [OPEN, { opacity: 0, transform: 'translate(0px,10px)' }, 'cubic-bezier(.05,.7,.1,1)'], [OPEN + 0.3, { opacity: 1, transform: 'translate(0px,0px)' }], [CLICK + 0.1, { opacity: 1, transform: 'translate(0px,0px)' }, IN], [CLICK + 0.3, { opacity: 0, transform: 'translate(0px,0px)' }]]);
  let menu = rect(M.x, M.y, M.w, M.h, { r: 18 });
  const hi = m.a([[0, { transform: 'translate(0px,0px)' }], [OPEN + 0.5, { transform: 'translate(0px,0px)' }, EASE], [OPEN + 0.85, { transform: 'translate(0px,64px)' }]]);
  menu += at(M.x + 12, M.y + 14, rect(0, 0, M.w - 24, 56, { r: 12, fill: INK, stroke: 'none' }), hi).replace('<rect', '<rect opacity=".08"');
  items.forEach((s, i) => {
    const y = M.y + 14 + i * 64;
    menu += icon.terminal(M.x + 28, y + 13, 36, 30, { sw: 2.5 }) + text(M.x + 80, y + 37, s, { size: 23 });
  });
  menu += icon.check(M.x + M.w - 40, M.y + 14 + 28, 9, { sw: 3 });
  out.push(g(menu, { cls: pop }));

  // The pointer: picks the second row in Settings, then holds Send and picks Claude Code.
  const cursor = path('M0 0L0 27L7 21L12 32L17 30L12 19L21 19Z', { fill: PAPER, sw: 2.5 });
  const p = (x, y) => ({ transform: `translate(${x}px,${y}px)` });
  const r1 = rowY(1) + 34;
  out.push(at(0, 0, cursor, m.a([
    [0, p(700, 250)], [0.4, p(700, 250), EASE], [PICK - 0.05, p(330, r1)], [PICK + 0.6, p(330, r1), EASE],
    [HOLD - 0.1, p(930, B.y + 34)], [OPEN + 0.25, p(930, B.y + 34), EASE], [CLICK - 0.1, p(970, M.y + 14 + 64 + 34)],
    [SENT + 0.4, p(970, M.y + 14 + 64 + 34), EASE], [6.2, p(1120, 330)], [RESET, p(1120, 330), EASE], [RESET + 0.7, p(700, 250)],
  ])));
  // Its clicks.
  const click = (t, x, y) => at(x, y, circle(0, 0, 18, { fill: 'none', sw: 2.5 }), m.a([[0, { opacity: 0, transform: 'scale(.3)' }], [t, { opacity: 0, transform: 'scale(.3)' }, STEP], [t + 0.01, { opacity: 1, transform: 'scale(.3)' }, EASE], [t + 0.35, { opacity: 0, transform: 'scale(1.2)' }]]));
  out.push(click(PICK, 330, r1), click(HOLD, 930, B.y + 34), click(CLICK, 970, M.y + 14 + 64 + 34));

  out.push(icon.key(56, 600) + text(118, 619, 'Set it once in Settings. It runs on your own subscription or key.', { size: 24, weight: 500 }));

  return {
    name: 'pick-model',
    title: 'Pick what powers the agent',
    desc: 'Settings lists what powers your agent: a Claude subscription (Claude Code), a ChatGPT subscription (Codex), an API key, or OpenRouter for any model. You set it once. Holding Send opens a short list of agents to pick another one for that send. It runs on your own subscription or key.',
    motion: m,
    body: out.join('\n'),
  };
}
