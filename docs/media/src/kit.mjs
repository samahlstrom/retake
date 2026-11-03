// The kit behind docs/media/*.svg: the paper, the shapes, the text and the motion. Each graphic (one module per
// file here) returns its body and its Motion; scripts/build-motion.mjs writes the animated SVGs.
//
// Style: white paper with a light dot grid, white shapes with black outlines, one accent (the record red), Inter
// (SIL OFL 1.1, fonts/OFL.txt) embedded as a small subset. Motion is CSS keyframes inside the SVG, so a README shows
// it inline. Every animation shares the graphic's loop, so the loop is seamless, and with prefers-reduced-motion
// the SVG shows the complete still (each element's state at `stillAt`).
import { readFileSync } from 'node:fs';

const here = new URL('./', import.meta.url);
const metrics = JSON.parse(readFileSync(new URL('fonts/metrics.json', here), 'utf8'));

export const W = 1200;
export const H = 675;
export const INK = '#111111';
export const PAPER = '#ffffff';
export const RED = '#ff453a';
export const MUTED = '#6b6b6b';
export const SOFT = '#ececec';
export const SW = 3; // the outline

// Easing: fast start, long settle (moves inside the frame and arrivals); exits accelerate; pops overshoot a little.
export const EASE = 'cubic-bezier(.3,0,.2,1)';
export const OUT = 'cubic-bezier(.05,.7,.1,1)';
export const IN = 'cubic-bezier(.4,0,.8,.2)';
export const POP = 'cubic-bezier(.34,1.56,.64,1)';
export const LINEAR = 'linear';
export const STEP = 'steps(1,end)';

const r3 = (n) => {
  const v = Math.round(n * 1000) / 1000;
  return Object.is(v, -0) ? 0 : v;
};
export const n = r3;

export function esc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// Width of a line of text in Inter at this size and weight (advances only, no kerning: a few px generous).
export function textWidth(s, size, weight = 600) {
  const m = metrics[String(weight)];
  let u = 0;
  for (const ch of s) u += m.adv[ch] ?? m.adv['n'];
  return (u / m.upm) * size;
}

function attrs(o) {
  return Object.entries(o)
    .filter(([, v]) => v !== undefined && v !== null && v !== false)
    .map(([k, v]) => ` ${k}="${typeof v === 'number' ? r3(v) : esc(v)}"`)
    .join('');
}

// ---------- motion ----------

function bezier(x1, y1, x2, y2) {
  const cx = 3 * x1, bx = 3 * (x2 - x1) - cx, ax = 1 - cx - bx;
  const cy = 3 * y1, by = 3 * (y2 - y1) - cy, ay = 1 - cy - by;
  const sx = (t) => ((ax * t + bx) * t + cx) * t;
  const sy = (t) => ((ay * t + by) * t + cy) * t;
  const dx = (t) => (3 * ax * t + 2 * bx) * t + cx;
  return (x) => {
    let t = x;
    for (let i = 0; i < 8; i++) {
      const e = sx(t) - x;
      const d = dx(t);
      if (Math.abs(e) < 1e-6) break;
      if (Math.abs(d) < 1e-6) break;
      t -= e / d;
    }
    if (t < 0 || t > 1 || Math.abs(sx(t) - x) > 1e-4) {
      let lo = 0, hi = 1;
      t = x;
      for (let i = 0; i < 40; i++) {
        if (sx(t) < x) lo = t;
        else hi = t;
        t = (lo + hi) / 2;
      }
    }
    return sy(t);
  };
}

function easing(name) {
  if (name === LINEAR) return (p) => p;
  if (name === STEP) return (p) => (p >= 1 ? 1 : 0);
  const m = /cubic-bezier\(([^)]+)\)/.exec(name);
  if (!m) throw new Error(`unknown easing ${name}`);
  const [a, b, c, d] = m[1].split(',').map(Number);
  return bezier(a, b, c, d);
}

const NUM = /-?\d*\.?\d+(?:e-?\d+)?/g;
function lerpValue(a, b, p) {
  if (typeof a === 'number' && typeof b === 'number') return a + (b - a) * p;
  const sa = String(a), sb = String(b);
  const na = sa.match(NUM) ?? [], nb = sb.match(NUM) ?? [];
  if (sa.replace(NUM, '#') !== sb.replace(NUM, '#') || na.length !== nb.length) {
    return p < 1 ? a : b; // different shapes: discrete
  }
  let i = 0;
  return sa.replace(NUM, () => {
    const v = Number(na[i]) + (Number(nb[i]) - Number(na[i])) * p;
    i++;
    return String(r3(v));
  });
}

function fmt(v) {
  return typeof v === 'number' ? String(r3(v)) : v;
}

// A Motion is one graphic's clock. `a(keys)` makes an animation: keys are [seconds, {css props}, easing to the next
// key]. Every animation runs for the whole loop (or `period`, which must divide it) and repeats.
export class Motion {
  constructor({ duration, stillAt }) {
    this.duration = duration;
    this.stillAt = stillAt;
    this.list = [];
  }

  a(keys, { period, origin } = {}) {
    const d = period ?? this.duration;
    if (Math.abs(this.duration / d - Math.round(this.duration / d)) > 1e-9) {
      throw new Error(`period ${d} doesn't divide the loop ${this.duration}`);
    }
    const props = [...new Set(keys.flatMap(([, p]) => Object.keys(p)))];
    // Fill every key with every prop: carry values forward, and back-fill the first ones.
    const filled = keys.map(([t, p, e]) => [t, { ...p }, e ?? EASE]);
    for (const prop of props) {
      let last;
      for (const k of filled) {
        if (prop in k[1]) last = k[1][prop];
        else if (last !== undefined) k[1][prop] = last;
      }
      const first = filled.find((k) => k[1][prop] !== undefined)[1][prop];
      for (const k of filled) if (k[1][prop] === undefined) k[1][prop] = first;
    }
    filled.sort((x, y) => x[0] - y[0]);
    if (filled[0][0] > 0) filled.unshift([0, { ...filled[0][1] }, filled[0][2]]);
    const lastKey = filled[filled.length - 1];
    if (lastKey[0] < d) filled.push([d, { ...lastKey[1] }, EASE]);
    for (const [t] of filled) if (t < 0 || t > d + 1e-9) throw new Error(`key at ${t}s is outside 0..${d}s`);
    const id = `a${this.list.length}`;
    this.list.push({ id, d, keys: filled, props, origin });
    return `m ${id}`;
  }

  // The state of one animation at time t (seconds on the loop).
  valueAt(anim, t) {
    const tt = ((t % anim.d) + anim.d) % anim.d;
    const ks = anim.keys;
    let i = 0;
    while (i < ks.length - 2 && ks[i + 1][0] <= tt) i++;
    const [t0, p0, e] = ks[i];
    const [t1, p1] = ks[i + 1];
    const raw = t1 > t0 ? Math.min(1, Math.max(0, (tt - t0) / (t1 - t0))) : 1;
    const p = easing(e)(raw);
    const out = {};
    for (const prop of anim.props) out[prop] = lerpValue(p0[prop], p1[prop], p);
    return out;
  }

  // CSS for the animated SVG: keyframes, plus each element's still state as its base style.
  css() {
    const out = [];
    for (const an of this.list) {
      const still = this.valueAt(an, this.stillAt);
      const base = Object.entries(still).map(([k, v]) => `${k}:${fmt(v)}`);
      if (an.origin) base.push(`transform-origin:${an.origin}`);
      out.push(`.${an.id}{${base.join(';')};animation:${an.id} ${r3(an.d)}s infinite}`);
      const frames = an.keys
        .map(([t, p, e], i) => {
          const pct = r3((t / an.d) * 100);
          const body = Object.entries(p).map(([k, v]) => `${k}:${fmt(v)}`);
          if (i < an.keys.length - 1) body.push(`animation-timing-function:${e}`);
          return `${pct}%{${body.join(';')}}`;
        })
        .join('');
      out.push(`@keyframes ${an.id}{${frames}}`);
    }
    out.push('@media (prefers-reduced-motion:reduce){.m{animation:none!important}}');
    return out.join('\n');
  }

  // CSS that freezes every element at time t (previews without a browser).
  frozenCss(t) {
    return this.list
      .map((an) => {
        const v = this.valueAt(an, t);
        const body = Object.entries(v).map(([k, x]) => `${k}:${fmt(x)}`);
        if (an.origin) body.push(`transform-origin:${an.origin}`);
        return `.${an.id}{${body.join(';')}}`;
      })
      .join('\n');
  }
}

// ---------- path lengths (so a stroke can draw on without pathLength, which not every renderer scales) ----------

export function pathLength(d) {
  const tok = d.match(/[MLHVQCZ]|-?\d*\.?\d+(?:e-?\d+)?/gi);
  let i = 0, cmd = '', x = 0, y = 0, sx = 0, sy = 0, len = 0;
  const num = () => Number(tok[i++]);
  const seg = (fn, steps = 24) => {
    let px = x, py = y;
    for (let s = 1; s <= steps; s++) {
      const [qx, qy] = fn(s / steps);
      len += Math.hypot(qx - px, qy - py);
      px = qx;
      py = qy;
    }
    x = px;
    y = py;
  };
  while (i < tok.length) {
    if (/[A-Za-z]/.test(tok[i])) cmd = tok[i++];
    switch (cmd) {
      case 'M': x = sx = num(); y = sy = num(); cmd = 'L'; break;
      case 'L': { const nx = num(), ny = num(); len += Math.hypot(nx - x, ny - y); x = nx; y = ny; break; }
      case 'H': { const nx = num(); len += Math.abs(nx - x); x = nx; break; }
      case 'V': { const ny = num(); len += Math.abs(ny - y); y = ny; break; }
      case 'Q': {
        const x0 = x, y0 = y, x1 = num(), y1 = num(), x2 = num(), y2 = num();
        seg((t) => [(1 - t) ** 2 * x0 + 2 * (1 - t) * t * x1 + t * t * x2, (1 - t) ** 2 * y0 + 2 * (1 - t) * t * y1 + t * t * y2]);
        break;
      }
      case 'C': {
        const x0 = x, y0 = y, x1 = num(), y1 = num(), x2 = num(), y2 = num(), x3 = num(), y3 = num();
        seg((t) => {
          const u = 1 - t;
          return [u ** 3 * x0 + 3 * u * u * t * x1 + 3 * u * t * t * x2 + t ** 3 * x3,
            u ** 3 * y0 + 3 * u * u * t * y1 + 3 * u * t * t * y2 + t ** 3 * y3];
        });
        break;
      }
      case 'Z': case 'z': len += Math.hypot(sx - x, sy - y); x = sx; y = sy; cmd = ''; break;
      default: throw new Error(`pathLength: unsupported command ${cmd} in ${d}`);
    }
  }
  return len;
}

// ---------- shapes ----------

export function rectD(x, y, w, h, r = 16) {
  r = Math.min(r, w / 2, h / 2);
  const k = 0.5523 * r;
  return `M${r3(x + r)} ${r3(y)}H${r3(x + w - r)}C${r3(x + w - r + k)} ${r3(y)} ${r3(x + w)} ${r3(y + r - k)} ${r3(x + w)} ${r3(y + r)}`
    + `V${r3(y + h - r)}C${r3(x + w)} ${r3(y + h - r + k)} ${r3(x + w - r + k)} ${r3(y + h)} ${r3(x + w - r)} ${r3(y + h)}`
    + `H${r3(x + r)}C${r3(x + r - k)} ${r3(y + h)} ${r3(x)} ${r3(y + h - r + k)} ${r3(x)} ${r3(y + h - r)}`
    + `V${r3(y + r)}C${r3(x)} ${r3(y + r - k)} ${r3(x + r - k)} ${r3(y)} ${r3(x + r)} ${r3(y)}Z`;
}

export function circleD(cx, cy, r) {
  const k = 0.5523 * r;
  return `M${r3(cx)} ${r3(cy - r)}C${r3(cx + k)} ${r3(cy - r)} ${r3(cx + r)} ${r3(cy - k)} ${r3(cx + r)} ${r3(cy)}`
    + `C${r3(cx + r)} ${r3(cy + k)} ${r3(cx + k)} ${r3(cy + r)} ${r3(cx)} ${r3(cy + r)}`
    + `C${r3(cx - k)} ${r3(cy + r)} ${r3(cx - r)} ${r3(cy + k)} ${r3(cx - r)} ${r3(cy)}`
    + `C${r3(cx - r)} ${r3(cy - k)} ${r3(cx - k)} ${r3(cy - r)} ${r3(cx)} ${r3(cy - r)}Z`;
}

export function rect(x, y, w, h, { r = 16, fill = PAPER, stroke = INK, sw = SW, cls, dash, opacity } = {}) {
  return `<rect${attrs({ x, y, width: w, height: h, rx: r, fill, stroke, 'stroke-width': stroke === 'none' ? undefined : sw, 'stroke-dasharray': dash, class: cls, opacity })}/>`;
}

export function circle(cx, cy, r, { fill = PAPER, stroke = INK, sw = SW, cls, opacity } = {}) {
  return `<circle${attrs({ cx, cy, r, fill, stroke, 'stroke-width': stroke === 'none' ? undefined : sw, class: cls, opacity })}/>`;
}

export function path(d, { fill = 'none', stroke = INK, sw = SW, cls, dash, opacity, cap = 'round', join = 'round' } = {}) {
  return `<path${attrs({ d, fill, stroke, 'stroke-width': stroke === 'none' ? undefined : sw, 'stroke-linecap': stroke === 'none' ? undefined : cap, 'stroke-linejoin': stroke === 'none' ? undefined : join, 'stroke-dasharray': dash, class: cls, opacity })}/>`;
}

export function g(body, { cls, transform, opacity } = {}) {
  return `<g${attrs({ class: cls, transform, opacity })}>${Array.isArray(body) ? body.join('') : body}</g>`;
}

// Position a group, with an inner group for the animation (CSS transforms then work in the group's own frame:
// scale and rotate about its (0, 0)).
export function at(x, y, body, cls) {
  return g(cls ? g(body, { cls }) : body, { transform: `translate(${r3(x)} ${r3(y)})` });
}

export function text(x, y, s, { size = 28, weight = 600, anchor = 'start', fill = INK, cls, ls, opacity } = {}) {
  const spacing = ls ?? (size >= 36 ? -0.02 * size : size >= 26 ? -0.01 * size : 0);
  return `<text${attrs({ x, y, 'font-size': size, 'font-weight': weight, 'text-anchor': anchor === 'start' ? undefined : anchor, fill, 'letter-spacing': spacing ? r3(spacing) : undefined, class: cls, opacity })}>${esc(s)}</text>`;
}

// An arrow head at (x, y) pointing along angle a (radians).
export function headD(x, y, a, size = 14) {
  const s = 0.5;
  return `M${r3(x - size * Math.cos(a - s))} ${r3(y - size * Math.sin(a - s))}L${r3(x)} ${r3(y)}L${r3(x - size * Math.cos(a + s))} ${r3(y - size * Math.sin(a + s))}`;
}

// A straight or bent arrow through points [[x,y], ...]; `curve` bends a two-point arrow sideways by that many px.
export function arrowD(pts, { curve = 0, size = 14, both = false } = {}) {
  let d, a, a0;
  if (pts.length === 2 && curve) {
    const [[x0, y0], [x1, y1]] = pts;
    const mx = (x0 + x1) / 2, my = (y0 + y1) / 2;
    const len = Math.hypot(x1 - x0, y1 - y0);
    const cx = mx - ((y1 - y0) / len) * curve, cy = my + ((x1 - x0) / len) * curve;
    d = `M${r3(x0)} ${r3(y0)}Q${r3(cx)} ${r3(cy)} ${r3(x1)} ${r3(y1)}`;
    a = Math.atan2(y1 - cy, x1 - cx);
    a0 = Math.atan2(y0 - cy, x0 - cx);
  } else {
    d = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${r3(x)} ${r3(y)}`).join('');
    const [p, q] = pts.slice(-2);
    a = Math.atan2(q[1] - p[1], q[0] - p[0]);
    a0 = Math.atan2(pts[0][1] - pts[1][1], pts[0][0] - pts[1][0]);
  }
  const [ex, ey] = pts[pts.length - 1];
  const heads = [headD(ex, ey, a, size)];
  if (both) heads.push(headD(pts[0][0], pts[0][1], a0, size));
  return { line: d, heads };
}

export function arrow(pts, opts = {}) {
  const { line, heads } = arrowD(pts, opts);
  return path(line, { ...opts, dash: opts.dash }) + heads.map((h) => path(h, { sw: opts.sw })).join('');
}

// A stroke that draws on between t0 and t1 (and is undrawn again between `out` and `out` + 0.4 s, if given).
export function drawOn(m, d, t0, t1, { out, sw = SW, stroke = INK, fill = 'none', ease = EASE, cap = 'round', back = 0.4 } = {}) {
  const L = Math.ceil(pathLength(d)) + 2;
  const keys = [[0, { 'stroke-dashoffset': L, opacity: 0 }, STEP], [t0, { 'stroke-dashoffset': L, opacity: 1 }, ease], [t1, { 'stroke-dashoffset': 0, opacity: 1 }]];
  if (out !== undefined) keys.push([out, { 'stroke-dashoffset': 0, opacity: 1 }], [out + back, { 'stroke-dashoffset': 0, opacity: 0 }]);
  const cls = m.a(keys);
  return `<path${attrs({ d, fill, stroke, 'stroke-width': sw, 'stroke-linecap': cap, 'stroke-linejoin': 'round', 'stroke-dasharray': `${L} ${L}`, class: cls })}/>`;
}

// Opacity keys: visible from `on` (fading in over `fade`) until `off` (fading out), else hidden.
export function showKeys(on, off, fade = 0.3, fadeOut = 0.3) {
  const k = [[0, { opacity: 0 }]];
  if (on > 0) k.push([on, { opacity: 0 }, EASE]);
  k.push([on + fade, { opacity: 1 }]);
  if (off !== undefined) k.push([off, { opacity: 1 }, IN], [off + fadeOut, { opacity: 0 }]);
  return k;
}

// Appear with a small rise and fade, stay until `off`.
export function riseKeys(on, off, dy = 12, dur = 0.35) {
  const k = [[0, { opacity: 0, transform: `translate(0px,${dy}px)` }]];
  if (on > 0) k.push([on, { opacity: 0, transform: `translate(0px,${dy}px)` }, OUT]);
  k.push([on + dur, { opacity: 1, transform: 'translate(0px,0px)' }]);
  if (off !== undefined) k.push([off, { opacity: 1, transform: 'translate(0px,0px)' }, IN], [off + 0.3, { opacity: 0, transform: 'translate(0px,0px)' }]);
  return k;
}

// Pop in (scale from 0.6 with a little overshoot) at `on`, until `off`. Use on a group drawn around its (0, 0).
export function popKeys(on, off, dur = 0.3) {
  const k = [[0, { opacity: 0, transform: 'scale(.6)' }]];
  if (on > 0) k.push([on, { opacity: 0, transform: 'scale(.6)' }, POP]);
  k.push([on + dur, { opacity: 1, transform: 'scale(1)' }]);
  if (off !== undefined) k.push([off, { opacity: 1, transform: 'scale(1)' }, IN], [off + 0.25, { opacity: 0, transform: 'scale(.9)' }]);
  return k;
}

// A dot that travels along straight segments [[x,y]...] from t0 to t1 (drawn at 0,0; place it with `at(0,0, …)`).
export function travelKeys(pts, t0, t1, { fade = 0.08, ease = EASE } = {}) {
  const segs = [];
  let total = 0;
  for (let i = 1; i < pts.length; i++) {
    const l = Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]);
    segs.push(l);
    total += l;
  }
  const tr = ([x, y]) => `translate(${r3(x)}px,${r3(y)}px)`;
  const k = [[0, { opacity: 0, transform: tr(pts[0]) }]];
  if (t0 > 0) k.push([t0, { opacity: 0, transform: tr(pts[0]) }, LINEAR]);
  k.push([t0 + fade, { opacity: 1, transform: tr(pts[0]) }, pts.length > 2 ? LINEAR : ease]);
  let acc = 0;
  const span = t1 - t0 - 2 * fade;
  for (let i = 1; i < pts.length; i++) {
    acc += segs[i - 1];
    k.push([t0 + fade + (span * acc) / total, { opacity: 1, transform: tr(pts[i]) }, LINEAR]);
  }
  k.push([t1, { opacity: 0, transform: tr(pts[pts.length - 1]) }]);
  return k;
}

// The paper: white with a light dot grid.
export function paper() {
  return `<defs><pattern id="dots" width="24" height="24" patternUnits="userSpaceOnUse"><circle cx="12" cy="12" r="1.5" fill="${INK}" fill-opacity=".2"/></pattern></defs>`
    + `<rect width="${W}" height="${H}" fill="${PAPER}"/><rect width="${W}" height="${H}" fill="url(#dots)"/>`;
}

export function heading(s, { x = 56, y = 82, size = 40 } = {}) {
  return text(x, y, s, { size, weight: 600 });
}

// "Retake." with the period in the record red.
export function wordmark(x, y, size = 30, anchor = 'start') {
  const w = textWidth('Retake', size, 600);
  const x0 = anchor === 'middle' ? x - (w + size * 0.28) / 2 : x;
  return text(x0, y, 'Retake', { size, weight: 600 }) + circle(x0 + w + size * 0.13, y - size * 0.07, size * 0.1, { fill: RED, stroke: 'none' });
}

// A pill with centered text; returns { svg, w }.
export function pill(x, y, label, { size = 24, weight = 600, h = 44, padX = 20, fill = PAPER, color = INK, stroke = INK, sw = SW, minW = 0 } = {}) {
  const w = Math.max(minW, textWidth(label, size, weight) + padX * 2);
  return {
    w,
    svg: rect(x, y, w, h, { r: h / 2, fill, stroke, sw }) + text(x + w / 2, y + h / 2 + size * 0.36, label, { size, weight, anchor: 'middle', fill: color }),
  };
}

// ---------- icons (drawn around their own top left, size about s) ----------

export const icon = {
  // A terminal window with a prompt.
  terminal(x, y, w = 64, h = 50, { sw = SW } = {}) {
    const u = Math.min(w, h * 1.3) / 64;
    return rect(x, y, w, h, { r: 8 * u + 2, sw }) + path(`M${n(x + 13 * u)} ${n(y + h / 2 - 9 * u)}L${n(x + 23 * u)} ${n(y + h / 2)}L${n(x + 13 * u)} ${n(y + h / 2 + 9 * u)}`, { sw })
      + path(`M${n(x + 29 * u)} ${n(y + h / 2 + 10 * u)}H${n(x + 45 * u)}`, { sw });
  },
  folder(x, y, w = 64, h = 48) {
    return path(`M${x} ${y + 8}Q${x} ${y} ${x + 8} ${y}H${x + w * 0.36}L${x + w * 0.46} ${y + 9}H${x + w - 8}Q${x + w} ${y + 9} ${x + w} ${y + 17}V${y + h - 8}Q${x + w} ${y + h} ${x + w - 8} ${y + h}H${x + 8}Q${x} ${y + h} ${x} ${y + h - 8}Z`, { fill: PAPER });
  },
  // A frame with a play triangle.
  video(x, y, w = 64, h = 44) {
    const cx = x + w / 2, cy = y + h / 2;
    return rect(x, y, w, h, { r: 7 }) + path(`M${cx - 7} ${cy - 10}L${cx + 10} ${cy}L${cx - 7} ${cy + 10}Z`, { fill: INK });
  },
  // A laptop.
  laptop(x, y, w = 84) {
    const h = w * 0.58;
    return rect(x + 8, y, w - 16, h, { r: 6 }) + path(`M${x} ${y + h + 6}H${x + w}Q${x + w - 4} ${y + h + 14} ${x + w - 12} ${y + h + 14}H${x + 12}Q${x + 4} ${y + h + 14} ${x} ${y + h + 6}Z`, { fill: PAPER });
  },
  phone(x, y, w = 40, h = 70) {
    return rect(x, y, w, h, { r: 9 }) + path(`M${x + w / 2 - 6} ${y + h - 9}H${x + w / 2 + 6}`);
  },
  // A small browser window: frame, top bar, three dots.
  browser(x, y, w, h, { r = 14, bar = 36 } = {}) {
    return rect(x, y, w, h, { r }) + path(`M${x} ${y + bar}H${x + w}`) + [0, 1, 2].map((i) => circle(x + 20 + i * 16, y + bar / 2, 4.5, { sw: 2.5 })).join('');
  },
  server(x, y, w = 72, h = 76) {
    const u = h / 3;
    return [0, 1, 2].map((i) => rect(x, y + i * u, w, u - 6, { r: 6 }) + circle(x + 14, y + i * u + (u - 6) / 2, 3.5, { fill: INK, stroke: 'none' }) + path(`M${x + w - 30} ${y + i * u + (u - 6) / 2}H${x + w - 14}`, { sw: 2.5 })).join('');
  },
  gear(cx, cy, r = 18) {
    const teeth = 8;
    let d = '';
    for (let i = 0; i < teeth * 2; i++) {
      const a0 = (i / (teeth * 2)) * Math.PI * 2, a1 = ((i + 1) / (teeth * 2)) * Math.PI * 2;
      const rr = i % 2 ? r * 0.78 : r;
      d += `${i ? 'L' : 'M'}${r3(cx + rr * Math.cos(a0))} ${r3(cy + rr * Math.sin(a0))}L${r3(cx + rr * Math.cos(a1))} ${r3(cy + rr * Math.sin(a1))}`;
    }
    return path(d + 'Z', { fill: PAPER, sw: 2.5 }) + circle(cx, cy, r * 0.32, { sw: 2.5 });
  },
  key(x, y, s = 1) {
    return circle(x + 12 * s, y + 12 * s, 10 * s, { sw: 2.5 }) + path(`M${x + 22 * s} ${y + 12 * s}H${x + 46 * s}M${x + 38 * s} ${y + 12 * s}V${y + 20 * s}M${x + 45 * s} ${y + 12 * s}V${y + 19 * s}`, { sw: 2.5 });
  },
  eye(cx, cy, w = 56) {
    const h = w * 0.36;
    return path(`M${cx - w / 2} ${cy}Q${cx} ${cy - h * 1.6} ${cx + w / 2} ${cy}Q${cx} ${cy + h * 1.6} ${cx - w / 2} ${cy}Z`, { fill: PAPER }) + circle(cx, cy, h * 0.5, { fill: INK, stroke: 'none' });
  },
  // The ⋯ menu: three dots centred on (cx, cy).
  more(cx, cy, fill = INK, r = 2.6) {
    return [-8, 0, 8].map((dx) => circle(cx + dx, cy, r, { fill, stroke: 'none' })).join('');
  },
  // A ring as a path (so it can spin or draw on).
  ring(r) {
    return circleD(0, 0, r);
  },
  check(cx, cy, s = 10, { sw = SW, stroke = INK } = {}) {
    return path(`M${cx - s} ${cy}L${cx - s * 0.3} ${cy + s * 0.7}L${cx + s} ${cy - s * 0.7}`, { sw, stroke });
  },
  download(cx, cy, s = 1) {
    return path(`M${cx} ${cy - 18 * s}V${cy + 6 * s}M${cx - 10 * s} ${cy - 4 * s}L${cx} ${cy + 6 * s}L${cx + 10 * s} ${cy - 4 * s}M${cx - 18 * s} ${cy + 8 * s}V${cy + 16 * s}H${cx + 18 * s}V${cy + 8 * s}`);
  },
  mic(cx, cy, s = 1) {
    return rect(cx - 7 * s, cy - 16 * s, 14 * s, 22 * s, { r: 7 * s, sw: 2.5 }) + path(`M${cx - 12 * s} ${cy}Q${cx - 12 * s} ${cy + 12 * s} ${cx} ${cy + 12 * s}Q${cx + 12 * s} ${cy + 12 * s} ${cx + 12 * s} ${cy}M${cx} ${cy + 12 * s}V${cy + 18 * s}`, { sw: 2.5 });
  },
  link(cx, cy) {
    return path(`M${cx - 4} ${cy + 4}L${cx + 4} ${cy - 4}`, { sw: 2.5 })
      + rect(cx - 20, cy - 2, 20, 12, { r: 6, sw: 2.5, fill: 'none' }).replace('<rect', `<rect transform="rotate(-45 ${cx - 10} ${cy + 4})"`)
      + rect(cx, cy - 10, 20, 12, { r: 6, sw: 2.5, fill: 'none' }).replace('<rect', `<rect transform="rotate(-45 ${cx + 10} ${cy - 4})"`);
  },
  person(cx, cy, s = 1) {
    return circle(cx, cy - 10 * s, 9 * s, { sw: 2.5 }) + path(`M${cx - 16 * s} ${cy + 18 * s}Q${cx - 16 * s} ${cy + 2 * s} ${cx} ${cy + 2 * s}Q${cx + 16 * s} ${cy + 2 * s} ${cx + 16 * s} ${cy + 18 * s}`, { sw: 2.5 });
  },
};

// The whole SVG. `motion` may be frozen at time `frozen` (a preview without the animation).
export function svgDoc({ title, desc, motion, body, frozen }) {
  const font = (file, weight) => `@font-face{font-family:RI;font-weight:${weight};src:url(data:font/woff2;base64,${readFileSync(new URL(`fonts/${file}`, here)).toString('base64')}) format("woff2")}`;
  const css = [
    font('Inter-Medium.woff2', 500),
    font('Inter-SemiBold.woff2', 600),
    'text{font-family:RI,Inter,sans-serif;font-kerning:normal}',
    frozen === undefined ? motion.css() : motion.frozenCss(frozen),
  ].join('\n');
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-labelledby="t d">`
    + `<title id="t">${esc(title)}</title><desc id="d">${esc(desc)}</desc>\n`
    + `<!-- Made by scripts/build-motion.mjs from docs/media/src. Text: Inter, SIL Open Font License 1.1 (docs/media/src/fonts/OFL.txt). -->\n`
    + `<style>\n${css}\n</style>\n${paper()}\n${body}\n</svg>\n`;
}
