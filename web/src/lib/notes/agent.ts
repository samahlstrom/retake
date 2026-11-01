// The agent's visuals on a question (spec/automation.md, "What the page shows"): one style of their own, never a pen
// colour, so nobody mistakes them for a reviewer's drawing. Pure, so it is unit-tested; AgentDrawing.svelte only
// draws what agentShapes returns.
import type { AgentVisuals, TaskAsk } from './types';

/** The agent's style: dashed, with a chip on each box and arrow. Its colours are the theme's --agent (the Question
 * violet) and --agent-ink (theme/tokens.css), set in AgentDrawing.svelte's style. */
export const AGENT = {
  dash: '6 4',
  width: 2,
  /** A box or arrow without a label says whose it is. */
  label: 'Agent',
} as const;

export interface AgentRect {
  x: number;
  y: number;
  w: number;
  h: number;
  label: string;
}
export interface AgentArrowShape {
  /** The shaft, and the head as a closed path. */
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  head: string;
  label: string;
  /** Where its chip goes: at the tail. */
  lx: number;
  ly: number;
}
export interface AgentShapes {
  rects: AgentRect[];
  arrows: AgentArrowShape[];
  lines: string[];
  /** Images placed on the frame. */
  images: { href: string; x: number; y: number; w: number; h: number }[];
}

const clamp01 = (v: number) => Math.min(1, Math.max(0, Number.isFinite(v) ? v : 0));

/** The agent's visuals as shapes in a w×h frame. Labels default to "Agent"; nothing is drawn in a pen colour. */
export function agentShapes(v: AgentVisuals | null | undefined, w: number, h: number): AgentShapes {
  const out: AgentShapes = { rects: [], arrows: [], lines: [], images: [] };
  if (!v) return out;
  for (const b of v.boxes ?? []) {
    const [x, y, bw, bh] = b.box.map(clamp01) as [number, number, number, number];
    out.rects.push({ x: x * w, y: y * h, w: bw * w, h: bh * h, label: b.label?.trim() || AGENT.label });
  }
  const head = 8 + Math.min(w, h) * 0.01;
  for (const a of v.arrows ?? []) {
    const x1 = clamp01(a.from[0]) * w,
      y1 = clamp01(a.from[1]) * h,
      x2 = clamp01(a.to[0]) * w,
      y2 = clamp01(a.to[1]) * h;
    const ang = Math.atan2(y2 - y1, x2 - x1),
      p = (d: number) => `${(x2 - head * Math.cos(ang + d)).toFixed(1)} ${(y2 - head * Math.sin(ang + d)).toFixed(1)}`;
    out.arrows.push({
      x1,
      y1,
      // The shaft stops at the head's base, so the dashes don't run through its point.
      x2: x2 - head * 0.8 * Math.cos(ang),
      y2: y2 - head * 0.8 * Math.sin(ang),
      head: `M${x2.toFixed(1)} ${y2.toFixed(1)}L${p(0.45)}L${p(-0.45)}Z`,
      label: a.label?.trim() || AGENT.label,
      lx: x1,
      ly: y1,
    });
  }
  for (const s of v.strokes ?? []) {
    const pts = s.points ?? [];
    if (!pts.length) continue;
    out.lines.push(
      pts.length === 1
        ? `M${clamp01(pts[0]![0]) * w} ${clamp01(pts[0]![1]) * h}h0.01`
        : pts
            .map(([x, y], i) => `${i ? 'L' : 'M'}${(clamp01(x) * w).toFixed(1)} ${(clamp01(y) * h).toFixed(1)}`)
            .join(''),
    );
  }
  for (const im of v.images ?? []) {
    if (!im.box) continue;
    const [x, y, bw, bh] = im.box.map(clamp01) as [number, number, number, number];
    out.images.push({ href: im.url, x: x * w, y: y * h, w: bw * w, h: bh * h });
  }
  return out;
}

/** The images that aren't placed on the frame: they show in the question's card. */
export const loose = (v: AgentVisuals | null | undefined): { url: string }[] =>
  (v?.images ?? []).filter((im) => !im.box);

/** Whether a question has anything to draw on the video. */
export const drawsOnVideo = (v: AgentVisuals | null | undefined): boolean => {
  const s = agentShapes(v, 1, 1);
  return s.rects.length + s.arrows.length + s.lines.length + s.images.length > 0;
};

/** How long around its moment a question without a range shows, in seconds (like strokes and images). */
export const ASK_SHOW = 1.5;

/** The stretch of the video a question's visuals show in: its range, else ASK_SHOW around its moment. */
export function askRange(ask: Pick<TaskAsk, 't' | 'from' | 'to'>): [number, number] {
  if (typeof ask.from === 'number' && typeof ask.to === 'number' && ask.to > ask.from) return [ask.from, ask.to];
  return [ask.t - ASK_SHOW, ask.t + ASK_SHOW];
}

export const inRange = (ask: Pick<TaskAsk, 't' | 'from' | 'to'>, vt: number): boolean => {
  const [a, b] = askRange(ask);
  return vt >= a && vt <= b;
};

/** A chip's width for its text, roughly (the SVG has no layout). */
export const chipWidth = (label: string, size = 11): number => Math.ceil(label.length * size * 0.58 + 12);
