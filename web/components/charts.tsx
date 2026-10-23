"use client";

import { useId } from "react";
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { formatBucket, formatCompact, formatDateTime, formatInt, formatMs, formatUsd } from "@/lib/format";
import type { ChartPoint } from "@/lib/range";

const axisTick = { fill: "var(--c-axis)", fontSize: 10.5, fontFamily: "var(--font-mono)" };
const margin = { top: 8, right: 18, bottom: 0, left: 0 };

interface TooltipRow {
  name: string;
  value: string;
  color: string;
}

function TooltipCard({ ts, rows, footer }: { ts: number; rows: TooltipRow[]; footer?: string }) {
  return (
    <div className="min-w-[168px] rounded-lg border border-line-strong bg-raised/95 px-3 py-2 text-[11.5px] shadow-pop backdrop-blur">
      <div className="mb-1.5 font-mono text-[10.5px] text-muted">{formatDateTime(ts)}</div>
      <div className="flex flex-col gap-1">
        {rows.map((r) => (
          <div key={r.name} className="flex items-center gap-2">
            <span className="size-2 rounded-[2px]" style={{ background: r.color }} />
            <span className="text-fg-2">{r.name}</span>
            <span className="tnum ml-auto pl-4 font-mono text-fg">{r.value}</span>
          </div>
        ))}
      </div>
      {footer ? <div className="mt-1.5 border-t border-line pt-1.5 font-mono text-[10.5px] text-muted">{footer}</div> : null}
    </div>
  );
}

interface TipProps {
  active?: boolean;
  payload?: ReadonlyArray<{ payload?: ChartPoint }>;
}

function pointOf(p: TipProps): ChartPoint | null {
  if (!p.active || !p.payload?.length) return null;
  return p.payload[0]?.payload ?? null;
}

function xAxis(intervalSeconds: number) {
  return (
    <XAxis
      dataKey="ts"
      tickFormatter={(v: number) => formatBucket(v, intervalSeconds)}
      tick={axisTick}
      tickLine={false}
      axisLine={{ stroke: "var(--line-strong)" }}
      minTickGap={36}
      tickMargin={8}
    />
  );
}

function axisMs(v: number): string {
  if (v >= 1000) return `${+(v / 1000).toFixed(1)}s`;
  return `${Math.round(v)}ms`;
}

export function LegendItem({ color, label, kind = "box" }: { color: string; label: string; kind?: "box" | "line" }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-[11.5px] text-muted">
      <span
        aria-hidden="true"
        className={kind === "line" ? "h-[2px] w-3 rounded-full" : "size-2 rounded-[2px]"}
        style={{ background: color }}
      />
      {label}
    </span>
  );
}

const cursor = { fill: "var(--hover)", opacity: 0.6 };
const lineCursor = { stroke: "var(--line-strong)", strokeWidth: 1, strokeDasharray: "3 3" };

export function RequestsChart({ data, intervalSeconds, height = 220 }: { data: ChartPoint[]; intervalSeconds: number; height?: number }) {
  // With hundreds of buckets, bars get hairline-thin: drop the gap.
  const gap = data.length > 90 ? 0 : 1;
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={data} margin={margin} barCategoryGap={gap}>
        <CartesianGrid vertical={false} stroke="var(--c-grid)" />
        {xAxis(intervalSeconds)}
        <YAxis
          tick={axisTick}
          tickLine={false}
          axisLine={false}
          width={44}
          allowDecimals={false}
          tickFormatter={(v: number) => formatCompact(v)}
        />
        <Tooltip
          cursor={cursor}
          isAnimationActive={false}
          content={(p: TipProps) => {
            const d = pointOf(p);
            if (!d) return null;
            const rate = d.requests ? (d.errors / d.requests) * 100 : 0;
            return (
              <TooltipCard
                ts={d.ts}
                rows={[
                  { name: "OK", value: formatInt(d.ok), color: "var(--c-ok)" },
                  { name: "Errors", value: formatInt(d.errors), color: "var(--c-err)" },
                ]}
                footer={`${formatInt(d.requests)} total · ${rate.toFixed(rate < 1 && rate > 0 ? 2 : 1)}% err`}
              />
            );
          }}
        />
        <Bar dataKey="ok" stackId="r" fill="var(--c-ok)" isAnimationActive={false} />
        <Bar dataKey="errors" stackId="r" fill="var(--c-err)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
      </BarChart>
    </ResponsiveContainer>
  );
}

export function LatencyChart({
  data,
  intervalSeconds,
  height = 220,
}: {
  data: ChartPoint[];
  intervalSeconds: number;
  height?: number;
}) {
  return (
    <ResponsiveContainer width="100%" height={height}>
      <LineChart data={data} margin={margin}>
        <CartesianGrid vertical={false} stroke="var(--c-grid)" />
        {xAxis(intervalSeconds)}
        <YAxis tick={axisTick} tickLine={false} axisLine={false} width={48} tickFormatter={axisMs} />
        <Tooltip
          cursor={lineCursor}
          isAnimationActive={false}
          content={(p: TipProps) => {
            const d = pointOf(p);
            if (!d || d.p50 === null) return null;
            return (
              <TooltipCard
                ts={d.ts}
                rows={[
                  { name: "p99", value: formatMs(d.p99), color: "var(--c-p99)" },
                  { name: "p95", value: formatMs(d.p95), color: "var(--c-p95)" },
                  { name: "p50", value: formatMs(d.p50), color: "var(--c-p50)" },
                ]}
                footer={d.ttft_p50 !== null ? `TTFT p50 ${formatMs(d.ttft_p50)} · p95 ${formatMs(d.ttft_p95)}` : undefined}
              />
            );
          }}
        />
        {(["p99", "p95", "p50"] as const).map((k) => (
          <Line
            key={k}
            type="monotone"
            dataKey={k}
            stroke={`var(--c-${k})`}
            strokeWidth={k === "p50" ? 2 : 1.75}
            dot={false}
            activeDot={{ r: 4, strokeWidth: 2, stroke: "var(--panel)" }}
            connectNulls={false}
            isAnimationActive={false}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  );
}

export function CostChart({ data, intervalSeconds, height = 220 }: { data: ChartPoint[]; intervalSeconds: number; height?: number }) {
  const id = useId().replace(/:/g, "");
  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={data} margin={margin}>
        <defs>
          <linearGradient id={`cost-${id}`} x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor="var(--c-cost)" stopOpacity={0.32} />
            <stop offset="100%" stopColor="var(--c-cost)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} stroke="var(--c-grid)" />
        {xAxis(intervalSeconds)}
        <YAxis tick={axisTick} tickLine={false} axisLine={false} width={52} tickFormatter={(v: number) => formatUsd(v)} />
        <Tooltip
          cursor={lineCursor}
          isAnimationActive={false}
          content={(p: TipProps) => {
            const d = pointOf(p);
            if (!d) return null;
            return (
              <TooltipCard
                ts={d.ts}
                rows={[{ name: "Cost", value: formatUsd(d.cost_usd), color: "var(--c-cost)" }]}
                footer={`${formatCompact(d.tokens)} tokens · ${formatInt(d.requests)} req`}
              />
            );
          }}
        />
        <Area
          type="monotone"
          dataKey="cost_usd"
          stroke="var(--c-cost)"
          strokeWidth={2}
          fill={`url(#cost-${id})`}
          activeDot={{ r: 4, strokeWidth: 2, stroke: "var(--panel)" }}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

/** Decorative trend line inside KPI tiles (values are in the tile itself). */
export function Sparkline({ values, color = "var(--signal)", height = 28 }: { values: (number | null)[]; color?: string; height?: number }) {
  const id = useId().replace(/:/g, "");
  const data = values.map((v, i) => ({ i, v }));
  if (data.length < 2) return <div style={{ height }} />;
  return (
    <div aria-hidden="true" style={{ height }}>
      <ResponsiveContainer width="100%" height={height}>
        <AreaChart data={data} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id={`spark-${id}`} x1="0" x2="0" y1="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.22} />
              <stop offset="100%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <YAxis hide domain={["dataMin", "dataMax"]} />
          <Area
            type="monotone"
            dataKey="v"
            stroke={color}
            strokeWidth={1.25}
            fill={`url(#spark-${id})`}
            dot={false}
            connectNulls
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
