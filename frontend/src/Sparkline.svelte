<script lang="ts">
  // Traffic sparkline: the last N down/up rate samples, autoscaled, right-aligned so
  // the newest sample is always at the right edge.
  interface Props {
    down: number[]
    up: number[]
  }

  let { down, up }: Props = $props()

  const W = 300
  const H = 46
  const N = 60

  const max = $derived(Math.max(1, ...down, ...up))

  function line(data: number[], peak: number): string {
    if (data.length < 2) return ''
    const step = W / (N - 1)
    const offset = W - step * (data.length - 1)
    return data
      .map(
        (value, index) =>
          `${index === 0 ? 'M' : 'L'}${(offset + index * step).toFixed(1)},${(H - 2 - (value / peak) * (H - 6)).toFixed(1)}`,
      )
      .join(' ')
  }

  const downLine = $derived(line(down, max))
  const upLine = $derived(line(up, max))
  const downArea = $derived(
    downLine
      ? `${downLine} L${W},${H} L${(W - (W / (N - 1)) * (down.length - 1)).toFixed(1)},${H} Z`
      : '',
  )
</script>

{#if downLine}
  <div class="spark">
    <svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" aria-hidden="true">
      <path d={downArea} class="area" />
      <path d={downLine} class="down" />
      {#if upLine}<path d={upLine} class="up" />{/if}
    </svg>
    <div class="legend">
      <span class="k down">↓ приём</span>
      <span class="k up">↑ отдача</span>
    </div>
  </div>
{/if}

<style>
  .spark {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .spark svg {
    width: 100%;
    height: 46px;
    display: block;
  }

  .spark path {
    fill: none;
    stroke-width: 1.5;
    vector-effect: non-scaling-stroke;
  }

  .spark .down {
    stroke: var(--accent);
  }

  .spark .up {
    stroke: var(--text-dim);
    stroke-dasharray: 4 3;
    opacity: 0.8;
  }

  .spark .area {
    fill: color-mix(in srgb, var(--accent) 15%, transparent);
    stroke: none;
  }

  .legend {
    display: flex;
    gap: 12px;
    font-size: 11px;
    color: var(--text-dim);
  }

  .legend .down {
    color: var(--accent);
  }
</style>
