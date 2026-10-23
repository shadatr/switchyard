// Time-range presets and timeseries helpers (pure, unit-tested).

import type { Point } from "./api";

export const RANGE_PRESETS = [
  { id: "15m", label: "15m", long: "Last 15 minutes", ms: 15 * 60_000 },
  { id: "1h", label: "1h", long: "Last hour", ms: 60 * 60_000 },
  { id: "6h", label: "6h", long: "Last 6 hours", ms: 6 * 60 * 60_000 },
  { id: "24h", label: "24h", long: "Last 24 hours", ms: 24 * 60 * 60_000 },
  { id: "7d", label: "7d", long: "Last 7 days", ms: 7 * 24 * 60 * 60_000 },
] as const;

export type RangeId = (typeof RANGE_PRESETS)[number]["id"];

export function isRangeId(v: unknown): v is RangeId {
  return RANGE_PRESETS.some((p) => p.id === v);
}

export function presetFor(id: RangeId) {
  return RANGE_PRESETS.find((p) => p.id === id) ?? RANGE_PRESETS[1];
}

/**
 * Resolves a preset to a [from, to) window ending at `anchor`. `to` is
 * aligned up to the next second so SWR keys stay stable within a render
 * cycle, and the window length stays exact.
 */
export function resolveRange(id: RangeId, anchor: number): { from: number; to: number } {
  const to = Math.ceil(anchor / 1000) * 1000;
  return { from: to - presetFor(id).ms, to };
}

/** Refresh cadence suited to each range: short ranges move faster. */
export function refreshMsFor(id: RangeId): number {
  switch (id) {
    case "15m":
      return 10_000;
    case "1h":
      return 15_000;
    case "6h":
      return 30_000;
    default:
      return 60_000;
  }
}

export interface ChartPoint {
  ts: number;
  requests: number;
  ok: number;
  errors: number;
  cache_hits: number;
  tokens: number;
  cost_usd: number;
  p50: number | null;
  p95: number | null;
  p99: number | null;
  ttft_p50: number | null;
  ttft_p95: number | null;
}

/**
 * The API only returns buckets that contain rows. Fill the gaps so bars
 * line up with time and lines break (null) instead of interpolating across
 * empty periods.
 */
export function fillBuckets(points: Point[], from: number, to: number, intervalSeconds: number): ChartPoint[] {
  const step = Math.max(1, intervalSeconds) * 1000;
  const byTs = new Map<number, Point>();
  for (const p of points) byTs.set(Date.parse(p.t), p);
  // Buckets are aligned to the epoch (ClickHouse toStartOfInterval).
  const start = Math.floor(from / step) * step;
  const out: ChartPoint[] = [];
  // Guard against absurd intervals producing huge arrays.
  const maxBuckets = 2000;
  for (let t = start; t < to && out.length < maxBuckets; t += step) {
    const p = byTs.get(t);
    out.push(toChartPoint(t, p));
    byTs.delete(t);
  }
  // Anything the API returned off-grid (e.g. tz-aligned buckets) is kept.
  for (const [t, p] of byTs) out.push(toChartPoint(t, p));
  out.sort((a, b) => a.ts - b.ts);
  return out;
}

function toChartPoint(t: number, p: Point | undefined): ChartPoint {
  if (!p || p.requests === 0) {
    return {
      ts: t,
      requests: 0,
      ok: 0,
      errors: 0,
      cache_hits: 0,
      tokens: 0,
      cost_usd: 0,
      p50: null,
      p95: null,
      p99: null,
      ttft_p50: null,
      ttft_p95: null,
    };
  }
  return {
    ts: t,
    requests: p.requests,
    ok: Math.max(0, p.requests - p.errors),
    errors: p.errors,
    cache_hits: p.cache_hits,
    tokens: p.tokens,
    cost_usd: p.cost_usd,
    p50: p.latency_ms.p50,
    p95: p.latency_ms.p95,
    p99: p.latency_ms.p99,
    ttft_p50: p.ttft_ms.p50 || null,
    ttft_p95: p.ttft_ms.p95 || null,
  };
}
