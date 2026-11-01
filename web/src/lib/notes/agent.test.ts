import { describe, expect, it } from 'vitest';
import { COLORS } from '../constants';
import { AGENT, agentShapes, askRange, drawsOnVideo, inRange, loose } from './agent';
import type { AgentVisuals } from './types';

const visuals: AgentVisuals = {
  boxes: [{ box: [0.1, 0.2, 0.3, 0.4], label: 'this logo' }, { box: [0.5, 0.5, 0.2, 0.2] }],
  arrows: [{ from: [0.9, 0.1], to: [0.6, 0.4], label: 'or this?' }],
  strokes: [
    {
      points: [
        [0.1, 0.1],
        [0.2, 0.15],
        [0.3, 0.1],
      ],
    },
  ],
  images: [{ url: '/api/questions/7/img_1.png', box: [0.6, 0.6, 0.3, 0.3] }, { url: '/api/questions/7/img_2.png' }],
  options: [{ text: 'Centered', image: { url: '/api/questions/7/option_1.png' } }, { text: 'Left' }],
  frame: '/api/questions/7/frame.jpg',
};

describe("the agent's visuals: their own style", () => {
  it('are never in a pen colour', () => {
    expect(Object.values(COLORS)).not.toContain(AGENT.color);
    expect(AGENT.color).toBe('#a693fa'); // the Question violet (ui.css --st-question)
    expect(AGENT.dash).toMatch(/\d+ \d+/); // dashed
  });
  it('scale to the frame and label every box and arrow, "Agent" when the agent gave none', () => {
    const s = agentShapes(visuals, 1000, 500);
    expect(s.rects).toEqual([
      { x: 100, y: 100, w: 300, h: 200, label: 'this logo' },
      { x: 500, y: 250, w: 200, h: 100, label: 'Agent' },
    ]);
    expect(s.arrows).toHaveLength(1);
    expect(s.arrows[0]).toMatchObject({ x1: 900, y1: 50, label: 'or this?', lx: 900, ly: 50 });
    expect(s.arrows[0]!.head).toMatch(/^M600\.0 200\.0L.*Z$/); // the head's point is the arrow's end
    expect(s.lines).toEqual(['M100.0 50.0L200.0 75.0L300.0 50.0']);
    expect(s.images).toEqual([{ href: '/api/questions/7/img_1.png', x: 600, y: 300, w: 300, h: 150 }]);
  });
  it('keep out-of-frame numbers inside it, and draw nothing for nothing', () => {
    expect(agentShapes({ boxes: [{ box: [-1, 0, 2, 0.5] }] }, 100, 100).rects[0]).toMatchObject({ x: 0, w: 100 });
    expect(agentShapes(null, 100, 100)).toEqual({ rects: [], arrows: [], lines: [], images: [] });
    expect(drawsOnVideo({ options: [{ text: 'A' }] })).toBe(false);
    expect(drawsOnVideo(visuals)).toBe(true);
  });
  it('show unplaced images in the card, not on the video', () => {
    expect(loose(visuals)).toEqual([{ url: '/api/questions/7/img_2.png' }]);
  });
  it('show during the question’s shot, else 1.5 s around its moment', () => {
    expect(askRange({ t: 83, from: 80, to: 86 })).toEqual([80, 86]);
    expect(askRange({ t: 10 })).toEqual([8.5, 11.5]);
    expect(inRange({ t: 83, from: 80, to: 86 }, 85.9)).toBe(true);
    expect(inRange({ t: 83, from: 80, to: 86 }, 86.1)).toBe(false);
    expect(inRange({ t: 10 }, 11.4)).toBe(true);
  });
});
