import { derived } from "svelte/store";
import type { ActivityLogEntry, ScatterData } from "../lib/types";
import { metrics } from "./api";

/** Max points to render — older entries are discarded first. */
const MAX_POINTS = 500;

/** Max generation speed to display — points above this are clipped. */
const MAX_TPS = 500;

/** Compute scatter plot data from a list of metrics entries. */
export function computeScatterData(entries: ActivityLogEntry[]): ScatterData | null {
  const points = entries
    .filter((m) => m.tokens.tokens_per_second > 0 && m.tokens.input_tokens > 0)
    .map((m) => ({
      x: m.tokens.cache_tokens + m.tokens.input_tokens,
      y: Math.min(m.tokens.tokens_per_second, MAX_TPS),
      model: m.model,
    }));

  if (points.length === 0) return null;

  // Keep only the most recent MAX_POINTS entries (data is newest-first).
  const truncated = points.length > MAX_POINTS ? points.slice(0, MAX_POINTS) : points;

  return {
    points: truncated,
    xMin: Math.min(...points.map((p) => p.x)),
    xMax: Math.max(...points.map((p) => p.x)),
    yMin: 0,
    yMax: Math.max(...points.map((p) => p.y)),
  };
}

/** Scatter plot: generation speed (tps) vs total context size */
export const scatterData = derived(metrics, computeScatterData);
