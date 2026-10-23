import { describe, expect, it } from "vitest";
import { formatAgo, formatBucket, formatCompact, formatInt, formatInterval, formatMs, formatPct, formatUs, formatUsd } from "../format";

describe("formatCompact", () => {
  it.each([
    [0, "0"],
    [999, "999"],
    [1000, "1k"],
    [1234, "1.2k"],
    [999_999, "1M"], [999_400, "999k"],
    [1_500_000, "1.5M"],
    [123_456_789, "123M"],
    [2_000_000_000, "2B"],
    [-1500, "-1.5k"],
  ])("%s → %s", (n, s) => expect(formatCompact(n)).toBe(s));
  it("handles non-finite", () => {
    expect(formatCompact(NaN)).toBe("—");
    expect(formatCompact(undefined)).toBe("—");
  });
});

describe("formatMs", () => {
  it.each([
    [0, "0 ms"],
    [0.4567, "0.46 ms"],
    [4.2, "4.20 ms"],
    [12.345, "12.3 ms"],
    [250.4, "250 ms"],
    [9999, "9999 ms"],
    [12_500, "12.5 s"],
  ])("%s → %s", (n, s) => expect(formatMs(n)).toBe(s));
  it("handles NaN", () => expect(formatMs(NaN)).toBe("—"));
});

describe("formatUs", () => {
  it("shows µs below a millisecond and ms above", () => {
    expect(formatUs(412)).toBe("412 µs");
    expect(formatUs(1530)).toBe("1.53 ms");
  });
});

describe("formatUsd", () => {
  it.each([
    [0, "$0"],
    [1234.5, "$1,235"],
    [12.345, "$12.35"],
    [0.5, "$0.5"],
    [0.0123, "$0.0123"],
    [0.000042, "$0.000042"],
    [0.0000421234, "$0.000042"],
    [0.0000001, "<$0.000001"],
    [-2.5, "-$2.50"],
  ])("%s → %s", (n, s) => expect(formatUsd(n)).toBe(s));
});

describe("formatPct", () => {
  it.each([
    [0, "0%"],
    [0.0012, "0.12%"],
    [0.05, "5%"],
    [0.1234, "12.3%"],
    [0.9996, "99.96%"],
    [1, "100%"],
  ])("%s → %s", (n, s) => expect(formatPct(n)).toBe(s));
});

describe("formatInt", () => {
  it("adds separators", () => expect(formatInt(1234567)).toBe("1,234,567"));
});

describe("formatAgo", () => {
  const now = Date.parse("2026-10-23T12:00:00Z");
  it.each([
    ["2026-10-23T11:59:58Z", "just now"],
    ["2026-10-23T11:59:30Z", "30s ago"],
    ["2026-10-23T11:55:00Z", "5m ago"],
    ["2026-10-23T09:00:00Z", "3h ago"],
    ["2026-10-19T12:00:00Z", "4d ago"],
  ])("%s → %s", (t, s) => expect(formatAgo(t, now)).toBe(s));
  it("handles null as never", () => expect(formatAgo(null, now)).toBe("never"));
});

describe("formatInterval", () => {
  it.each([
    [10, "10s"],
    [300, "5m"],
    [3600, "1h"],
    [86400, "1d"],
  ])("%s → %s", (n, s) => expect(formatInterval(n)).toBe(s));
});

describe("formatBucket", () => {
  it("includes seconds for sub-minute buckets", () => {
    expect(formatBucket(new Date(2026, 9, 23, 14, 5, 30).getTime(), 10)).toBe("14:05:30");
  });
  it("shows HH:MM for minute buckets", () => {
    expect(formatBucket(new Date(2026, 9, 23, 14, 5).getTime(), 60)).toBe("14:05");
  });
});
