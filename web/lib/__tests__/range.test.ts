import { describe, expect, it } from "vitest";
import type { Point } from "../api";
import { fillBuckets, isRangeId, resolveRange } from "../range";

const pt = (iso: string, requests: number, errors = 0): Point => ({
  t: iso,
  requests,
  errors,
  cache_hits: 0,
  tokens: requests * 10,
  cost_usd: requests * 0.001,
  latency_ms: { p50: 10, p95: 20, p99: 30 },
  ttft_ms: { p50: 5, p95: 8, p99: 9 },
});

describe("resolveRange", () => {
  it("ends on the next whole second and spans the preset exactly", () => {
    const r = resolveRange("1h", 1_000_000_500);
    expect(r.to).toBe(1_000_001_000);
    expect(r.to - r.from).toBe(3_600_000);
  });
  it("is stable within the same second", () => {
    expect(resolveRange("15m", 5_000_100)).toEqual(resolveRange("15m", 5_000_900));
  });
});

describe("isRangeId", () => {
  it("accepts presets only", () => {
    expect(isRangeId("24h")).toBe(true);
    expect(isRangeId("2h")).toBe(false);
    expect(isRangeId(null)).toBe(false);
  });
});

describe("fillBuckets", () => {
  const from = Date.parse("2026-10-23T12:00:00Z");
  const to = Date.parse("2026-10-23T12:05:00Z");

  it("fills missing buckets with zero counts and null latency", () => {
    const out = fillBuckets([pt("2026-10-23T12:01:00Z", 10, 2), pt("2026-10-23T12:03:00Z", 4)], from, to, 60);
    expect(out.map((p) => p.requests)).toEqual([0, 10, 0, 4, 0]);
    expect(out[1]).toMatchObject({ ok: 8, errors: 2, p95: 20 });
    expect(out[0]?.p50).toBeNull();
    expect(out[2]?.p99).toBeNull();
  });

  it("aligns the first bucket down to the interval", () => {
    const out = fillBuckets([], from + 15_000, to, 60);
    expect(out[0]?.ts).toBe(from);
    expect(out).toHaveLength(5);
  });

  it("keeps off-grid points instead of dropping them", () => {
    const out = fillBuckets([pt("2026-10-23T12:01:30Z", 3)], from, to, 60);
    expect(out.some((p) => p.requests === 3)).toBe(true);
    expect(out).toHaveLength(6);
  });
});
