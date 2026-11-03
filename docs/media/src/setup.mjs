// 8. Setup: install (npm i -g retake-cli), run retake, pick your videos folder, it finds the agents you have (or helps
// you sign in), and you review in the browser. Optional: a share link for a phone or a teammate.
import {
  Motion, INK, PAPER, RED, MUTED, EASE, IN, STEP, POP, rect, circle, path, g, at, text, textWidth, arrow, heading, icon,
  popKeys, showKeys,
} from './kit.mjs';

export default function setup() {
  const m = new Motion({ duration: 10, stillAt: 9 });
  const RESET = 9.3;
  const out = [];
  out.push(heading('Install it, run it, review.'));

  const CW = 200, GAP = 22, Y = 128, CH = 300;
  const cx = (i) => 56 + i * (CW + GAP);
  const T = [0.6, 2.0, 3.3, 4.6, 5.9];
  const steps = [
    ['Install', ''],
    ['Run retake', ''],
    ['Pick your', 'videos folder'],
    ['Finds your', 'agents'],
    ['Review in', 'the browser'],
  ];

  steps.forEach(([l1, l2], i) => {
    const x = cx(i), mid = x + CW / 2;
    out.push(rect(x, Y, CW, CH, { r: 18 }));
    // The step lights up: a thicker outline while it happens, a check once it's done.
    out.push(g(rect(x, Y, CW, CH, { r: 18, fill: 'none', sw: 5.5 }), { cls: m.a(showKeys(T[i], i < 4 ? T[i + 1] : RESET, 0.15, 0.2)) }));
    out.push(circle(x + 30, Y + 30, 15, { fill: INK, stroke: 'none' }) + text(x + 30, Y + 36, String(i + 1), { size: 17, anchor: 'middle', fill: PAPER }));
    out.push(at(x + CW - 30, Y + 30, circle(0, 0, 14, { fill: PAPER, sw: 2.5 }) + icon.check(0, 0.5, 6.5, { sw: 2.5 }), m.a(popKeys((i < 4 ? T[i + 1] : 7.0) - 0.1, RESET))));
    out.push(text(mid, Y + CH - (l2 ? 54 : 36), l1, { size: 24, anchor: 'middle' }));
    if (l2) out.push(text(mid, Y + CH - 24, l2, { size: 24, anchor: 'middle' }));
    if (i < 4) out.push(path(`M${x + CW + 9} ${Y + CH / 2 - 8}L${x + CW + 17} ${Y + CH / 2}L${x + CW + 9} ${Y + CH / 2 + 8}`, { sw: 3 }));
  });
  const midY = Y + 122;

  // A command on a dark pill, as typed in a terminal.
  const command = (mid, y, cmd, size) => {
    const w = textWidth(cmd, size) + 36;
    return rect(mid - w / 2, y - 24, w, 48, { r: 12, fill: INK, stroke: 'none' })
      + text(mid, y + size * 0.36, cmd, { size, anchor: 'middle', fill: PAPER });
  };
  // 1. Install: the one command, and the download coming down.
  {
    const mid = cx(0) + CW / 2;
    out.push(command(mid, midY, 'npm i -g retake-cli', 15));
    out.push(path(`M${mid - 66} ${midY + 56}H${mid + 66}`, { sw: 4, stroke: '#e2e2e2' }));
    out.push(at(mid - 66, midY + 56, path('M0 0H132', { sw: 4 }), m.a([[0, { transform: 'scale(0,1)' }], [T[0] + 0.2, { transform: 'scale(0,1)' }, EASE], [T[1] - 0.2, { transform: 'scale(1,1)' }], [RESET, { transform: 'scale(1,1)' }, IN], [RESET + 0.3, { transform: 'scale(0,1)' }]])));
  }
  // 2. Run retake.
  {
    const mid = cx(1) + CW / 2;
    out.push(at(mid, midY + 10, command(0, 0, 'retake', 20), m.a([[0, { transform: 'scale(1)' }], [T[1], { transform: 'scale(1)' }, EASE], [T[1] + 0.12, { transform: 'scale(.88)' }, POP], [T[1] + 0.45, { transform: 'scale(1)' }]])));
  }
  // 3. Pick your videos folder.
  {
    const mid = cx(2) + CW / 2;
    out.push(icon.folder(mid - 52, midY - 30, 104, 78) + text(mid, midY + 30, 'Videos', { size: 19, anchor: 'middle' }));
    out.push(g(rect(mid - 66, midY - 44, 132, 108, { r: 14, fill: 'none', sw: 2.5, dash: '7 6' }), { cls: m.a(showKeys(T[2] + 0.2, RESET, 0.2)) }));
  }
  // 4. It finds your agents.
  {
    const x = cx(3) + 22;
    ['Claude Code', 'Codex'].forEach((s, k) => {
      const y = midY - 26 + k * 50;
      out.push(icon.terminal(x - 4, y - 12, 26, 22, { sw: 2.2 }) + text(x + 30, y + 6, s, { size: 17 }));
      out.push(at(x + 158, y, circle(0, 0, 11, { fill: INK, stroke: 'none' }) + icon.check(0, 0.5, 5, { sw: 2.2, stroke: PAPER }), m.a(popKeys(T[3] + 0.3 + k * 0.3, RESET))));
    });
    out.push(text(cx(3) + CW / 2, midY + 66, 'or helps you sign in', { size: 17, weight: 500, fill: MUTED, anchor: 'middle' }));
  }
  // 5. Review in the browser.
  {
    const x = cx(4) + 28, w = CW - 56;
    out.push(icon.browser(x, midY - 50, w, 104, { r: 10, bar: 26 }).replace(/r="4.5"/g, 'r="3.5"'));
    out.push(path(`M${x + w / 2 - 10} ${midY - 4}L${x + w / 2 + 14} ${midY + 12}L${x + w / 2 - 10} ${midY + 28}Z`, { fill: INK, stroke: 'none' }));
    out.push(at(x + w - 18, midY - 37, circle(0, 0, 6, { fill: RED, stroke: 'none' }), m.a(showKeys(T[4] + 0.2, RESET, 0.2))));
  }

  // The one click.
  const cursor = path('M0 0L0 27L7 21L12 32L17 30L12 19L21 19Z', { fill: PAPER, sw: 2.5 });
  const p = (x, y) => ({ transform: `translate(${x}px,${y}px)` });
  const dl = [cx(0) + CW / 2 + 20, midY + 4];
  out.push(g(at(0, 0, cursor, m.a([[0, p(dl[0] + 60, Y + CH + 40)], [0.1, p(dl[0] + 60, Y + CH + 40), EASE], [T[0] - 0.05, p(...dl)], [T[0] + 0.25, p(...dl), EASE], [T[0] + 0.9, p(dl[0] + 40, Y + CH + 40)], [RESET, p(dl[0] + 40, Y + CH + 40), EASE], [RESET + 0.5, p(dl[0] + 60, Y + CH + 40)]])),
    { cls: m.a([[0, { opacity: 1 }], [T[0] + 0.9, { opacity: 1 }, EASE], [T[0] + 1.2, { opacity: 0 }], [RESET, { opacity: 0 }, EASE], [RESET + 0.3, { opacity: 1 }]]) }));
  out.push(at(...dl, circle(0, 0, 20, { fill: 'none', sw: 2.5 }), m.a([[0, { opacity: 0, transform: 'scale(.3)' }], [T[0], { opacity: 0, transform: 'scale(.3)' }, STEP], [T[0] + 0.01, { opacity: 1, transform: 'scale(.3)' }, EASE], [T[0] + 0.4, { opacity: 0, transform: 'scale(1.2)' }]])));

  // Optional: a share link for a phone or a teammate.
  const share = m.a(showKeys(7.0, RESET, 0.35));
  const px = cx(4) + CW / 2;
  const note = 'Optional: a share link for your phone or a teammate';
  out.push(g(arrow([[px, Y + CH + 12], [px, 500]], { dash: '2 8' }) + icon.phone(px - 22, 508, 44, 76) + icon.person(px - 70, 554, 0.9)
    + text(px - 108, 562, note, { size: 22, weight: 500, anchor: 'end' }), { cls: share }));

  return {
    name: 'setup',
    title: 'Setting up Retake',
    desc: 'Five steps: download Retake with one button, open it, pick your videos folder, it finds the agents you have (Claude Code, Codex) or helps you sign in, and you review in the browser. Optionally, a share link opens the review on your phone or a teammate\'s.',
    motion: m,
    body: out.join('\n'),
  };
}
