<script lang="ts">
  // A comment's drawing, small: its pen lines and images in the frame's shape, so a card shows what you circled
  // without playing to it. Click it to go to that moment.
  import { COLORS } from '../../lib/constants';
  import type { Box, HistoryStroke } from '../../lib/types';

  let {
    strokes,
    images,
    aspect,
    onclick,
  }: {
    strokes: HistoryStroke[];
    images: { url: string; box: Box }[];
    aspect: number;
    onclick: () => void;
  } = $props();

  const W = 160;
  const H = $derived(Math.round(W / (aspect > 0.2 && aspect < 5 ? aspect : 16 / 9)));
  const lines = $derived(
    strokes
      .filter((st) => st.points.length)
      .map((st) => ({
        color: COLORS[st.color as keyof typeof COLORS] ?? COLORS.red,
        d:
          st.points.length === 1
            ? `M${st.points[0]![0] * W} ${st.points[0]![1] * H}h0.01`
            : st.points.map(([x, y], i) => `${i ? 'L' : 'M'}${(x * W).toFixed(1)} ${(y * H).toFixed(1)}`).join(''),
      })),
  );
</script>

<button class="sketch" title="Go to this moment" aria-label="Drawing: go to this moment" {onclick}>
  <svg viewBox="0 0 {W} {H}" aria-hidden="true">
    {#each images as im, i (i)}<image
        href={im.url}
        x={im.box[0] * W}
        y={im.box[1] * H}
        width={im.box[2] * W}
        height={im.box[3] * H}
        preserveAspectRatio="xMidYMid slice"
      />{/each}
    {#each lines as l, i (i)}<path
        d={l.d}
        stroke={l.color}
        fill="none"
        stroke-width="2.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      />{/each}
  </svg>
</button>

<style>
  .sketch {
    display: block;
    width: 120px;
    margin: 10px 0 0;
    padding: 0;
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 7px;
    overflow: hidden;
    background: #18181c;
    cursor: pointer;
    transition: border-color 0.15s ease;
  }
  .sketch:hover {
    border-color: rgba(255, 255, 255, 0.24);
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
  }
</style>
