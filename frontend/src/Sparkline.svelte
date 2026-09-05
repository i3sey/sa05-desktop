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
    const pts: Array<[number, number]> = data.map(
      (value, index): [number, number] => [
        offset + index * step,
        H - 2 - (value / peak) * (H - 6),
      ],
    )
    // Catmull-Rom spline through the samples: straight segments jump with every
    // new sample, a curve reads as one continuous flow.
    let d = `M${pts[0][0].toFixed(1)},${pts[0][1].toFixed(1)}`
    for (let i = 0; i < pts.length - 1; i++) {
      const p0 = pts[Math.max(0, i - 1)]
      const p1 = pts[i]
      const p2 = pts[i + 1]
      const p3 = pts[Math.min(pts.length - 1, i + 2)]
      const c1x = p1[0] + (p2[0] - p0[0]) / 6
      const c1y = p1[1] + (p2[1] - p0[1]) / 6
      const c2x = p2[0] - (p3[0] - p1[0]) / 6
      const c2y = p2[1] - (p3[1] - p1[1]) / 6
      d += ` C${c1x.toFixed(1)},${c1y.toFixed(1)} ${c2x.toFixed(1)},${c2y.toFixed(1)} ${p2[0].toFixed(1)},${p2[1].toFixed(1)}`
    }
    return d
  }

  const downLine = $derived(line(down, max))
  const upLine = $derived(line(up, max))

  // Continuous drift: new samples render one step to the right, then the group
  // glides back over the sampling interval instead of jumping left in steps.
  const step = W / (N - 1)
  let shift = $state(0)
  let sliding = $state(false)
  let seen = $state('')
  $effect(() => {
    const key = `${down.length}:${up.length}:${down[down.length - 1] ?? ''}:${up[up.length - 1] ?? ''}`
    if (seen === '') {
      seen = key
      return
    }
    if (key === seen) return
    seen = key
    sliding = false
    shift = step
    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        sliding = true
        shift = 0
      }),
    )
  })
  const downArea = $derived(
    downLine
      ? `${downLine} L${W},${H} L${(W - (W / (N - 1)) * (down.length - 1)).toFixed(1)},${H} Z`
      : '',
  )
</script>

{#if downLine}
  <div class="spark">
    <svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" aria-hidden="true">
      <g class="glide" class:sliding={sliding} style="transform: translateX({shift.toFixed(2)}px)">
        <path d={downArea} class="area" />
        <path d={downLine} class="down" />
        {#if upLine}<path d={upLine} class="up" />{/if}
      </g>
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
    overflow: hidden;
  }

  .spark .glide.sliding {
    transition: transform 1s linear;
  }

  .spark path {
    fill: none;
    stroke-width: 1.5;
    stroke-linecap: round;
    stroke-linejoin: round;
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
