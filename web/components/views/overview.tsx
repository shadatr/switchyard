"use client";

import { useMemo } from "react";
import { BreakdownTable } from "@/components/breakdown";
import { CostChart, LatencyChart, LegendItem, RequestsChart, Sparkline } from "@/components/charts";
import { ProjectPicker, RangePicker, RefreshButton } from "@/components/filters";
import { EmptyState, ErrorState, PageHeader, Panel, Skeleton, cx } from "@/components/ui";
import type { Group, Overview } from "@/lib/api";
import { formatCompact, formatInt, formatInterval, formatMs, formatPct, formatUsd, statusText } from "@/lib/format";
import { useBreakdown, useOverview, useTimeseries } from "@/lib/hooks";
import { usePrefs } from "@/lib/prefs";
import { type ChartPoint, fillBuckets, presetFor } from "@/lib/range";

export function OverviewView() {
  const prefs = usePrefs();
  const overview = useOverview();
  const ts = useTimeseries();
  const byModel = useBreakdown("model");
  const byTarget = useBreakdown("target");
  const byStatus = useBreakdown("status");

  const series = useMemo<ChartPoint[]>(
    () => (ts.data ? fillBuckets(ts.data.points, prefs.from, prefs.to, ts.data.interval_seconds) : []),
    [ts.data, prefs.from, prefs.to],
  );
  const iv = ts.data?.interval_seconds ?? 60;
  const o = overview.data;
  const validating = overview.isValidating || ts.isValidating;

  const refreshAll = () => {
    prefs.refresh();
  };

  return (
    <>
      <PageHeader
        title="Overview"
        description={`${presetFor(prefs.range).long} of traffic through the gateway.`}
        meta={o ? <SourceChip o={o} /> : <Skeleton className="h-5 w-48" />}
        actions={
          <>
            <ProjectPicker className="w-full sm:w-[160px]" />
            <RangePicker />
            <RefreshButton onClick={refreshAll} spinning={validating} />
          </>
        }
      />

      {overview.error && !o ? (
        <Panel>
          <ErrorState error={overview.error} onRetry={() => overview.mutate()} />
        </Panel>
      ) : (
        <Kpis o={o} series={series} />
      )}

      {o && o.requests === 0 && !overview.error ? (
        <Panel className="mt-4">
          <EmptyState title="No requests in this window">
            Nothing was logged {presetFor(prefs.range).long.toLowerCase()}
            {prefs.project ? " for this project" : ""}. Widen the range, or send traffic through the gateway.
          </EmptyState>
        </Panel>
      ) : (
        <>
          <div className="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-2">
            <Panel
              title="Requests"
              subtitle={`per ${formatInterval(iv)} bucket`}
              actions={
                <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
                  <LegendItem color="var(--c-ok)" label="OK" />
                  <LegendItem color="var(--c-err)" label="Errors" />
                </div>
              }
              bodyClassName="px-2 pb-2 pt-3"
            >
              <ChartBody state={ts} empty={series.length === 0}>
                <RequestsChart data={series} intervalSeconds={iv} />
              </ChartBody>
            </Panel>
            <Panel
              title="Latency"
              subtitle="end-to-end, percentiles per bucket"
              actions={
                <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1">
                  <LegendItem kind="line" color="var(--c-p50)" label="p50" />
                  <LegendItem kind="line" color="var(--c-p95)" label="p95" />
                  <LegendItem kind="line" color="var(--c-p99)" label="p99" />
                </div>
              }
              bodyClassName="px-2 pb-2 pt-3"
              style={{ animationDelay: "60ms" }}
            >
              <ChartBody state={ts} empty={series.length === 0}>
                <LatencyChart data={series} intervalSeconds={iv} />
              </ChartBody>
            </Panel>
          </div>

          <div className="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-3">
            <Panel
              title="Cost"
              subtitle={o ? `${formatUsd(o.cost_usd)} in window` : "USD per bucket"}
              bodyClassName="px-2 pb-2 pt-3"
              style={{ animationDelay: "90ms" }}
            >
              <ChartBody state={ts} empty={series.length === 0}>
                <CostChart data={series} intervalSeconds={iv} />
              </ChartBody>
            </Panel>
            <Panel title="By model" subtitle="model alias requested by clients" className="xl:col-span-2" style={{ animationDelay: "120ms" }}>
              <BreakdownBody state={byModel} label="Model" />
            </Panel>
          </div>

          <div className="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-3">
            <Panel title="By target" subtitle="provider/model that served the request" className="xl:col-span-2" style={{ animationDelay: "150ms" }}>
              <BreakdownBody state={byTarget} label="Target" />
            </Panel>
            <Panel title="Status codes" subtitle="share of responses" style={{ animationDelay: "180ms" }}>
              <StatusMix state={byStatus} />
            </Panel>
          </div>
        </>
      )}
    </>
  );
}

function SourceChip({ o }: { o: Overview }) {
  return (
    <div className="inline-flex items-center gap-2 font-mono text-[11px] text-faint">
      <span
        className="rounded border border-line px-1.5 py-px text-muted"
        title={o.source === "rollup_1m" ? "Served from the per-minute rollup table (requests_1m)" : "Served from the raw requests table"}
      >
        {o.source === "rollup_1m" ? "rollup · 1m" : "raw"}
      </span>
      <span title="ClickHouse query time">{formatMs(o.query_ms)} query</span>
    </div>
  );
}

// -------------------------------------------------------------------- KPIs

function Kpis({ o, series }: { o: Overview | undefined; series: ChartPoint[] }) {
  const reqs = series.map((p) => p.requests);
  const errs = series.map((p) => (p.requests ? p.errors / p.requests : null));
  const cost = series.map((p) => p.cost_usd);
  const cache = series.map((p) => (p.requests ? p.cache_hits / p.requests : null));
  const p95 = series.map((p) => p.p95);
  const ttft = series.map((p) => p.ttft_p50);
  const errTone = !o ? "neutral" : o.error_rate >= 0.05 ? "err" : o.error_rate >= 0.01 ? "warn" : "ok";
  const tokensTotal = o ? o.prompt_tokens + o.completion_tokens : 0;

  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <Tile label="Requests" loading={!o} delay={0} spark={<Sparkline values={reqs} />}>
        <Big>{o ? formatCompact(o.requests) : ""}</Big>
        <Sub>{o ? `${formatInt(o.requests)} total` : ""}</Sub>
      </Tile>
      <Tile
        label="Error rate"
        loading={!o}
        delay={30}
        spark={<Sparkline values={errs} color={errTone === "ok" ? "var(--ok)" : errTone === "warn" ? "var(--warn)" : "var(--err)"} />}
      >
        <Big className={errTone === "err" ? "text-err" : errTone === "warn" ? "text-warn" : undefined}>{o ? formatPct(o.error_rate) : ""}</Big>
        <Sub>{o ? `${formatInt(o.errors)} failed` : ""}</Sub>
      </Tile>
      <Tile label="Cost" loading={!o} delay={60} spark={<Sparkline values={cost} color="var(--c-cost)" />}>
        <Big>{o ? formatUsd(o.cost_usd) : ""}</Big>
        <Sub>{o && o.requests ? `${formatUsd(o.cost_usd / o.requests)} / request` : "—"}</Sub>
      </Tile>
      <Tile label="Tokens" loading={!o} delay={90}>
        <Big>{o ? formatCompact(tokensTotal) : ""}</Big>
        {o ? <TokenSplit inT={o.prompt_tokens} outT={o.completion_tokens} /> : null}
      </Tile>
      <Tile label="Cache hit rate" loading={!o} delay={120} spark={<Sparkline values={cache} color="var(--info)" />}>
        <Big>{o ? formatPct(o.cache_hit_rate) : ""}</Big>
        <Sub>{o ? `${formatCompact(o.cache_hits)} served from cache` : ""}</Sub>
      </Tile>
      <Tile label="Time to first token" loading={!o} delay={150} spark={<Sparkline values={ttft} color="var(--c-p50)" />}>
        {o ? (
          <Triple
            items={[
              { k: "p50", v: o.ttft_ms.p50, c: "var(--c-p50)" },
              { k: "p95", v: o.ttft_ms.p95, c: "var(--c-p95)" },
            ]}
          />
        ) : null}
      </Tile>
      <Tile label="Latency" loading={!o} delay={180} className="col-span-2" spark={<Sparkline values={p95} color="var(--c-p95)" />}>
        {o ? (
          <Triple
            items={[
              { k: "p50", v: o.latency_ms.p50, c: "var(--c-p50)" },
              { k: "p95", v: o.latency_ms.p95, c: "var(--c-p95)" },
              { k: "p99", v: o.latency_ms.p99, c: "var(--c-p99)" },
            ]}
          />
        ) : null}
      </Tile>
    </div>
  );
}

function Tile({
  label,
  loading,
  children,
  spark,
  className,
  delay,
}: {
  label: string;
  loading: boolean;
  children?: React.ReactNode;
  spark?: React.ReactNode;
  className?: string;
  delay: number;
}) {
  return (
    <div
      className={cx(
        "rise relative flex min-h-[118px] flex-col overflow-hidden rounded-xl border border-line bg-panel px-4 pt-3.5 shadow-[inset_0_1px_0_rgb(255_255_255/0.025)]",
        className,
      )}
      style={{ animationDelay: `${delay}ms` }}
    >
      <div className="font-mono text-[10.5px] uppercase tracking-[0.08em] text-muted">{label}</div>
      {loading ? (
        <div className="mt-2.5 flex flex-col gap-2" aria-busy="true">
          <Skeleton className="h-7 w-24" />
          <Skeleton className="h-3.5 w-32" />
        </div>
      ) : (
        <div className="mt-1.5 flex-1">{children}</div>
      )}
      {spark && !loading ? <div className="-mx-4 mt-1 opacity-90">{spark}</div> : <div className="h-3" />}
    </div>
  );
}

function Big({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cx("tnum text-[26px] font-semibold leading-none tracking-[-0.03em] text-fg", className)}>{children}</div>;
}

function Sub({ children }: { children: React.ReactNode }) {
  return <div className="tnum mt-1.5 font-mono text-[11px] text-muted">{children}</div>;
}

function Triple({ items }: { items: { k: string; v: number; c: string }[] }) {
  return (
    <div className="flex items-end gap-5">
      {items.map((it, i) => (
        <div key={it.k} className="min-w-0">
          <div className={cx("tnum whitespace-nowrap font-semibold leading-none tracking-[-0.03em]", i === 0 ? "text-[26px] text-fg" : "text-[18px] text-fg-2")}>
            {/* latency is logged in whole ms, so 0 means sub-millisecond */}
            {it.v === 0 ? "<1 ms" : formatMs(it.v)}
          </div>
          <div className="mt-1.5 flex items-center gap-1.5 font-mono text-[11px] text-muted">
            <span className="h-[2px] w-2.5 rounded-full" style={{ background: it.c }} />
            {it.k}
          </div>
        </div>
      ))}
    </div>
  );
}

function TokenSplit({ inT, outT }: { inT: number; outT: number }) {
  const total = inT + outT || 1;
  return (
    <div className="mt-2.5">
      <div className="flex h-1.5 overflow-hidden rounded-full bg-panel-2">
        <div className="h-full bg-[color:var(--c-p50)]" style={{ width: `${(inT / total) * 100}%` }} />
        <div className="h-full w-[2px] bg-panel" />
        <div className="h-full flex-1 bg-[color:var(--c-p99)]" />
      </div>
      <div className="tnum mt-1.5 flex justify-between font-mono text-[11px] text-muted">
        <span>
          <span className="text-fg-2">{formatCompact(inT)}</span> in
        </span>
        <span>
          <span className="text-fg-2">{formatCompact(outT)}</span> out
        </span>
      </div>
    </div>
  );
}

// ------------------------------------------------------------ chart states

function ChartBody({
  state,
  empty,
  children,
}: {
  state: { data?: unknown; error?: unknown; mutate: () => unknown };
  empty: boolean;
  children: React.ReactNode;
}) {
  if (state.error && !state.data) return <ErrorState compact error={state.error} onRetry={() => state.mutate()} />;
  if (!state.data) return <ChartSkeleton />;
  if (empty) return <EmptyState compact title="No data in range" />;
  return <>{children}</>;
}

function ChartSkeleton() {
  return (
    <div className="flex h-[220px] items-end gap-1 px-3 pb-6" aria-busy="true">
      {Array.from({ length: 36 }, (_, i) => (
        <Skeleton key={i} className="flex-1 rounded-sm" style={{ height: `${25 + ((i * 37) % 60)}%` }} />
      ))}
    </div>
  );
}

function BreakdownBody({
  state,
  label,
}: {
  state: { data?: { groups: Group[] }; error?: unknown; isLoading: boolean; mutate: () => unknown };
  label: string;
}) {
  if (state.error && !state.data) return <ErrorState compact error={state.error} onRetry={() => state.mutate()} />;
  if (state.data && state.data.groups.length === 0) return <EmptyState compact title="Nothing to break down" />;
  return <BreakdownTable groups={state.data?.groups} label={label} loading={!state.data} />;
}

function StatusMix({ state }: { state: { data?: { groups: Group[] }; error?: unknown; mutate: () => unknown } }) {
  if (state.error && !state.data) return <ErrorState compact error={state.error} onRetry={() => state.mutate()} />;
  if (!state.data) {
    return (
      <div className="flex flex-col gap-3 p-4">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-5" />
        ))}
      </div>
    );
  }
  const groups = [...state.data.groups].sort((a, b) => Number(a.key) - Number(b.key));
  const total = groups.reduce((s, g) => s + g.requests, 0);
  if (!total) return <EmptyState compact title="No responses" />;
  return (
    <ul className="flex flex-col gap-3 p-4">
      {groups.map((g) => {
        const code = Number(g.key);
        const color = code >= 500 ? "var(--err)" : code === 429 || code === 402 ? "var(--warn)" : code >= 400 ? "var(--err)" : "var(--ok)";
        const share = g.requests / total;
        return (
          <li key={g.key}>
            <div className="flex items-baseline justify-between gap-2">
              <span className="flex items-center gap-2">
                <span className="size-2 rounded-[2px]" style={{ background: color }} />
                <span className="tnum font-mono text-[12.5px] text-fg">{g.key}</span>
                <span className="text-[11.5px] text-muted">{statusText(code)}</span>
              </span>
              <span className="tnum font-mono text-[11.5px] text-fg-2">
                {formatCompact(g.requests)} <span className="text-faint">· {formatPct(share)}</span>
              </span>
            </div>
            <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-panel-2">
              <div className="h-full rounded-full" style={{ width: `${Math.max(0.5, share * 100)}%`, background: color }} />
            </div>
          </li>
        );
      })}
    </ul>
  );
}
