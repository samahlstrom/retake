<script lang="ts">
  // A comment's drawing, small: its pen lines and images over the frame they were drawn on (sketchFrame), so a card
  // shows what you circled without playing to it. Click it to go to that moment.
  import { COLORS } from '../../lib/constants';
  import type { Box, HistoryStroke } from '../../lib/types';

  let {
    strokes,
    images,
    aspect,
    frame = null,
    onclick,
  }: {
    strokes: HistoryStroke[];
    images: { url: string; box: Box }[];
    aspect: number;
    /** The picture under the lines; none: the dark box alone. */
    frame?: string | null;
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

<button class="sketch" data-part="sketch" title="Go to this moment" aria-label="Drawing: go to this moment" {onclick}>
  <svg viewBox="0 0 {W} {H}" aria-hidden="true">
    {#if frame}<image href={frame} width={W} height={H} opacity="0.8" preserveAspectRatio="xMidYMid slice" />{/if}
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
  @layer retake {
    .sketch {
      display: block;
      width: 120px;
      margin: 10px 0 0;
      padding: 0;
      border: 1px solid color-mix(in srgb, var(--overlay) 8%, transparent);
      border-radius: 7px;
      overflow: hidden;
      background: var(--surface-control);
      cursor: pointer;
      transition: border-color 0.15s ease;
    }
    .sketch:hover {
      border-color: color-mix(in srgb, var(--overlay) 24%, transparent);
    }
    svg {
      display: block;
      width: 100%;
      height: auto;
    }
  }
</style>
