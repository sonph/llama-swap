<script lang="ts">
  interface ScatterPoint {
    x: number;
    y: number;
    model: string;
    _jitterX?: number;
    _jitterY?: number;
  }

  interface ScatterData {
    points: ScatterPoint[];
    xMin: number;
    xMax: number;
    yMin: number;
    yMax: number;
  }

  let {
    data,
    xLabel = "Total Prompt Size (tokens)",
    yLabel = "Generation Speed (tokens/sec)",
  }: {
    data: ScatterData;
    xLabel?: string;
    yLabel?: string;
  } = $props();

  const height = 320;
  const padding = { top: 10, right: 20, bottom: 50, left: 70 };
  const viewBoxWidth = 1200;
  const chartWidth = viewBoxWidth - padding.left - padding.right;
  const chartHeight = height - padding.top - padding.bottom;

  // Separate point clusters by x position to prevent overlap
  let clustered = $derived(
    data.points.length > 0 ? clusterPoints(data.points) : []
  );

  // Collect distinct model names for the legend
  let models = $derived(
    data.points.length > 0
      ? Array.from(new Set(data.points.map((p) => p.model))).sort()
      : []
  );

  // Color palette for models — actual CSS color values (light + dark themes)
  const modelColors = [
    { light: "#3b82f6", dark: "#60a5fa" },   // blue
    { light: "#10b981", dark: "#34d399" },   // emerald
    { light: "#f59e0b", dark: "#fbbf24" },   // amber
    { light: "#8b5cf6", dark: "#a78bfa" },   // purple
    { light: "#f43f5e", dark: "#fb7185" },   // rose
    { light: "#06b6d4", dark: "#22d3ee" },   // cyan
    { light: "#6366f1", dark: "#818cf8" },   // indigo
    { light: "#f97316", dark: "#fb923c" },   // orange
  ];

  function getModelColor(model: string): { fill: string; stroke: string } {
    const idx = models.indexOf(model);
    const c = modelColors[idx % modelColors.length];
    // Use CSS var to pick light/dark based on theme
    return {
      fill: `var(--model-color-${idx % modelColors.length}, ${c.light})`,
      stroke: c.light,
    };
  }

  function getX(value: number): number {
    const range = data.xMax - data.xMin || 1;
    return padding.left + ((value - data.xMin) / range) * chartWidth;
  }

  function getY(value: number): number {
    const range = data.yMax - data.yMin || 1;
    return height - padding.bottom - ((value - data.yMin) / range) * chartHeight;
  }

  /** Cluster overlapping points by x value using a simple grid jitter. */
  function clusterPoints(points: ScatterPoint[]): ScatterPoint[] {
    // Group points by integer x bucket
    const buckets = new Map<string, ScatterPoint[]>();
    for (const p of points) {
      const key = String(Math.round(p.x));
      if (!buckets.has(key)) buckets.set(key, []);
      buckets.get(key)!.push(p);
    }

    const result: ScatterPoint[] = [];
    const jitterAmount = Math.max(chartWidth / 40, 2);

    for (const [, group] of buckets) {
      if (group.length === 1) {
        result.push(group[0]);
        continue;
      }

      // Sort y within bucket for deterministic layout
      group.sort((a, b) => a.y - b.y);

      // Place on a grid: row/col layout with jitter
      const cols = Math.ceil(Math.sqrt(group.length));
      const rows = Math.ceil(group.length / cols);
      const stepX = jitterAmount;
      const stepY = chartHeight / Math.max(rows, 1);

      for (let i = 0; i < group.length; i++) {
        const col = i % cols;
        const row = Math.floor(i / cols);
        const jitterX = (col - (cols - 1) / 2) * stepX;
        const jitterY = -(row + 0.5) * stepY;
        result.push({
          ...group[i],
          _jitterX: jitterX,
          _jitterY: jitterY,
        });
      }
    }

    return result;
  }

  function niceScale(min: number, max: number, targetTicks: number = 5): { ticks: number[]; niceMin: number; niceMax: number } {
    if (max === min) {
      max += 1;
      min -= 1;
    }
    const range = max - min;
    const roughStep = range / targetTicks;
    const magnitude = Math.pow(10, Math.floor(Math.log10(roughStep)));
    const residual = roughStep / magnitude;

    let niceStep: number;
    if (residual <= 1.5) niceStep = magnitude;
    else if (residual <= 3) niceStep = 2 * magnitude;
    else if (residual <= 7) niceStep = 5 * magnitude;
    else niceStep = 10 * magnitude;

    const niceMin = Math.floor(min / niceStep) * niceStep;
    const niceMax = Math.ceil(max / niceStep) * niceStep;

    const ticks: number[] = [];
    for (let v = niceMin; v <= niceMax + niceStep * 0.5; v += niceStep) {
      ticks.push(Math.round(v * 1e8) / 1e8);
    }

    return { ticks, niceMin, niceMax };
  }
</script>

<div class="mt-2 w-full select-none">
  <svg
    viewBox="0 0 {viewBoxWidth} {height}"
    class="w-full h-auto"
    preserveAspectRatio="xMidYMid meet"
  >
    {#if data.points.length > 0}
      {@const xScale = niceScale(data.xMin, data.xMax)}
      {@const yScale = niceScale(data.yMin, data.yMax)}

      <!-- Chart background -->
      <rect
        x={padding.left}
        y={padding.top}
        width={chartWidth}
        height={chartHeight}
        fill="currentColor"
        opacity="0.02"
        rx="4"
      />

      <!-- Y-axis -->
      <line
        x1={padding.left}
        y1={padding.top}
        x2={padding.left}
        y2={height - padding.bottom}
        stroke="currentColor"
        stroke-width="1"
        opacity="0.2"
      />

      <!-- X-axis -->
      <line
        x1={padding.left}
        y1={height - padding.bottom}
        x2={viewBoxWidth - padding.right}
        y2={height - padding.bottom}
        stroke="currentColor"
        stroke-width="1"
        opacity="0.2"
      />

      <!-- Y-axis ticks and labels -->
      {#each yScale.ticks as tick}
        {@const y = getY(tick)}
        <line
          x1={padding.left - 6}
          y1={y}
          x2={padding.left}
          y2={y}
          stroke="currentColor"
          stroke-width="1"
          opacity="0.3"
        />
        <text
          x={padding.left - 10}
          y={y + 7}
          font-size="12"
          fill="currentColor"
          opacity="0.6"
          text-anchor="end"
        >
          {tick >= 1000
            ? (tick / 1000).toFixed(1) + 'k'
            : tick < 1
              ? tick.toFixed(2)
              : Number.isInteger(tick)
                ? tick
                : tick.toFixed(1)}
        </text>
        <!-- Subtle grid line -->
        <line
          x1={padding.left}
          y1={y}
          x2={padding.left + chartWidth}
          y2={y}
          stroke="currentColor"
          stroke-width="1"
          opacity="0.06"
        />
      {/each}

      <!-- X-axis ticks and labels -->
      {#each xScale.ticks as tick}
        {@const x = getX(tick)}
        <line
          x1={x}
          y1={height - padding.bottom}
          x2={x}
          y2={height - padding.bottom + 6}
          stroke="currentColor"
          stroke-width="1"
          opacity="0.3"
        />
        <text
          x={x}
          y={height - padding.bottom + 26}
          font-size="12"
          fill="currentColor"
          opacity="0.6"
          text-anchor="middle"
        >
          {tick >= 1000
            ? (tick / 1000).toFixed(1) + 'k'
            : Number.isInteger(tick)
              ? tick
              : tick.toFixed(1)}
        </text>
        <!-- Subtle grid line -->
        <line
          x1={x}
          y1={padding.top}
          x2={x}
          y2={padding.top + chartHeight}
          stroke="currentColor"
          stroke-width="1"
          opacity="0.06"
        />
      {/each}

      <!-- Axis labels -->
      <text
        x={padding.left + chartWidth / 2}
        y={height - 6}
        font-size="14"
        fill="currentColor"
        opacity="0.7"
        text-anchor="middle"
      >
        {xLabel}
      </text>

      <!-- Rotated Y-axis label -->
      <text
        y="0"
        font-size="14"
        fill="currentColor"
        opacity="0.7"
        text-anchor="middle"
        transform={`translate(40, ${padding.top + chartHeight / 2}) rotate(-90)`}
      >
        {yLabel}
      </text>

      <!-- Scatter points (one color per model) -->
      {#each clustered as p}
        {@const cx = getX(p.x) + (p._jitterX ?? 0)}
        {@const cy = getY(p.y) + (p._jitterY ?? 0)}
        {@const color = getModelColor(p.model)}
        <circle
          cx={cx}
          cy={cy}
          r="7"
          fill={color.fill}
          stroke={color.stroke}
          stroke-width="1"
          stroke-opacity="0.5"
          opacity="0.7"
          class="cursor-pointer hover:opacity-100"
        >
          <title>{`${p.model}\nPrompt Size: ${Math.round(p.x).toLocaleString()} tokens\nGen Speed: ${p.y.toFixed(2)} t/s`}</title>
        </circle>
      {/each}

    {:else}
      <text
        x={padding.left + chartWidth / 2}
        y={padding.top + chartHeight / 2}
        font-size="14"
        fill="currentColor"
        opacity="0.35"
        text-anchor="middle"
      >
        No data yet — make some requests to see the scatter plot
      </text>
    {/if}
  </svg>
</div>
