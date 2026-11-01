<script lang="ts">
  // The agent's visuals on a question, in their own style (lib/notes/agent.ts): violet, dashed, a chip on each box
  // and arrow, placed images in a framed card. The same drawing goes over the video and on the question's thumbnail.
  import { AGENT, agentShapes, chipWidth } from '../lib/notes/agent';
  import type { AgentVisuals } from '../lib/notes/types';

  let {
    visuals,
    width,
    height,
    chip = 11,
  }: { visuals: AgentVisuals | null | undefined; width: number; height: number; chip?: number } = $props();

  const s = $derived(agentShapes(visuals, width, height));
  const line = $derived(Math.max(1.2, (AGENT.width * Math.min(width, height)) / 360));
  const ch = $derived(chip + 6);
</script>

<g class="agent-drawing" data-agent="true">
  {#each s.images as im, i (i)}
    <image href={im.href} x={im.x} y={im.y} width={im.w} height={im.h} preserveAspectRatio="xMidYMid slice" />
    <rect
      class="agent-frame"
      x={im.x}
      y={im.y}
      width={im.w}
      height={im.h}
      fill="none"
      stroke={AGENT.color}
      stroke-width={line}
      rx="3"
    />
  {/each}
  {#each s.lines as d, i (i)}
    <path
      class="agent-line"
      {d}
      fill="none"
      stroke={AGENT.color}
      stroke-width={line * 1.3}
      stroke-dasharray={AGENT.dash}
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  {/each}
  {#each s.rects as r, i (i)}
    {@const cw = chipWidth(r.label, chip)}
    <rect
      class="agent-box"
      x={r.x}
      y={r.y}
      width={r.w}
      height={r.h}
      fill="rgba(166, 147, 250, 0.08)"
      stroke={AGENT.color}
      stroke-width={line}
      stroke-dasharray={AGENT.dash}
      rx="3"
    />
    <g class="agent-chip" transform="translate({Math.min(r.x, width - cw)} {r.y >= ch ? r.y - ch - 2 : r.y + 2})">
      <rect width={cw} height={ch} rx={ch / 2} fill={AGENT.color} />
      <text
        x={cw / 2}
        y={ch / 2}
        fill={AGENT.ink}
        font-size={chip}
        font-weight="600"
        text-anchor="middle"
        dominant-baseline="central">{r.label}</text
      >
    </g>
  {/each}
  {#each s.arrows as a, i (i)}
    {@const cw = chipWidth(a.label, chip)}
    <line
      class="agent-arrow"
      x1={a.x1}
      y1={a.y1}
      x2={a.x2}
      y2={a.y2}
      stroke={AGENT.color}
      stroke-width={line * 1.2}
      stroke-dasharray={AGENT.dash}
      stroke-linecap="round"
    />
    <path d={a.head} fill={AGENT.color} />
    <g
      class="agent-chip"
      transform="translate({Math.max(0, Math.min(a.lx - cw / 2, width - cw))} {Math.max(
        0,
        Math.min(a.ly + (a.y2 >= a.y1 ? -ch - 4 : 4), height - ch),
      )})"
    >
      <rect width={cw} height={ch} rx={ch / 2} fill={AGENT.color} />
      <text
        x={cw / 2}
        y={ch / 2}
        fill={AGENT.ink}
        font-size={chip}
        font-weight="600"
        text-anchor="middle"
        dominant-baseline="central">{a.label}</text
      >
    </g>
  {/each}
</g>
