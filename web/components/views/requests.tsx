"use client";

import { useEffect, useMemo, useState } from "react";
import { RangePicker, RefreshButton } from "@/components/filters";
import { Sheet } from "@/components/overlay";
import { CacheBadge, RequestDetail } from "@/components/request-detail";
import { Button, EmptyState, ErrorState, Input, PageHeader, Panel, Select, Skeleton, StatusBadge, cx } from "@/components/ui";
import type { RequestRow } from "@/lib/api";
import { formatClock, formatCompact, formatDateTime, formatMs, formatUsd } from "@/lib/format";
import { useBreakdown, useKeyIndex, useKeys, useRequestDetail, useRequestLog } from "@/lib/hooks";
import { usePrefs } from "@/lib/prefs";
import { presetFor, resolveRange } from "@/lib/range";

const STATUS_OPTIONS = [
  { v: "", label: "Any status" },
  { v: "ok", label: "OK (< 400)" },
  { v: "error", label: "Errors (≥ 400)" },
  { v: "200", label: "200" },
  { v: "400", label: "400 invalid request" },
  { v: "401", label: "401 invalid key" },
  { v: "402", label: "402 budget exceeded" },
  { v: "403", label: "403 model not allowed" },
  { v: "429", label: "429 rate limited" },
  { v: "500", label: "500 internal" },
  { v: "502", label: "502 upstream error" },
  { v: "504", label: "504 upstream timeout" },
];

const PAGE = 50;

export function RequestsView() {
  const prefs = usePrefs();
  const [anchor, setAnchor] = useState(() => Date.now());
  const [model, setModel] = useState("");
  const [modelDraft, setModelDraft] = useState("");
  const [status, setStatus] = useState("");
  const [key, setKey] = useState("");
  const [selected, setSelected] = useState<RequestRow | null>(null);

  // Snapshot the window when the range changes so "Load more" pages a
  // stable set; refresh moves it forward explicitly.
  useEffect(() => setAnchor(Date.now()), [prefs.range]);
  const { from, to } = resolveRange(prefs.range, anchor);

  const filter = useMemo(
    () => ({ from, to, project: prefs.project, model, status, key, limit: PAGE }),
    [from, to, prefs.project, model, status, key],
  );
  const log = useRequestLog(filter);
  const keys = useKeys();
  const keyIndex = useKeyIndex();
  const models = useBreakdown("model");

  const rows = useMemo(() => (log.data ?? []).flatMap((p) => p.data), [log.data]);
  const last = log.data?.[log.data.length - 1];
  const hasMore = !!last?.next_cursor;
  const loadingMore = log.isValidating && log.size > (log.data?.length ?? 0);
  const firstLoad = !log.data && !log.error;
  const activeFilters = [model, status, key].filter(Boolean).length;

  const detail = useRequestDetail(selected?.request_id ?? null);

  return (
    <>
      <PageHeader
        title="Requests"
        description="Every request the gateway logged, newest first. Click a row for timings, tokens and routing."
        actions={
          <>
            <RangePicker />
            <RefreshButton onClick={() => setAnchor(Date.now())} spinning={log.isValidating && !loadingMore} />
          </>
        }
      />

      <Panel bodyClassName="flex flex-col">
        <form
          className="flex flex-wrap items-end gap-2 border-b border-line p-3"
          onSubmit={(e) => {
            e.preventDefault();
            setModel(modelDraft.trim());
          }}
        >
          <div className="w-full sm:w-[200px]">
            <label htmlFor="f-model" className="sr-only">
              Model
            </label>
            <Input
              id="f-model"
              placeholder="Model alias"
              list="model-options"
              value={modelDraft}
              onChange={(e) => {
                setModelDraft(e.target.value);
                // Choosing from the datalist applies immediately.
                if (models.data?.groups.some((g) => g.key === e.target.value)) setModel(e.target.value);
                if (e.target.value === "") setModel("");
              }}
              onBlur={() => setModel(modelDraft.trim())}
              className="font-mono"
            />
            <datalist id="model-options">
              {models.data?.groups.map((g) => <option key={g.key} value={g.key} />)}
            </datalist>
          </div>
          <Select aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)} className="w-[calc(50%-4px)] sm:w-[190px]">
            {STATUS_OPTIONS.map((o) => (
              <option key={o.v} value={o.v}>
                {o.label}
              </option>
            ))}
          </Select>
          <Select aria-label="API key" value={key} onChange={(e) => setKey(e.target.value)} className="w-[calc(50%-4px)] sm:w-[220px]">
            <option value="">Any key</option>
            {(keys.data ?? []).map((k) => (
              <option key={k.id} value={k.id}>
                {k.name} · {k.project}
              </option>
            ))}
          </Select>
          {activeFilters > 0 ? (
            <Button
              variant="ghost"
              size="md"
              onClick={() => {
                setModel("");
                setModelDraft("");
                setStatus("");
                setKey("");
              }}
            >
              Clear {activeFilters > 1 ? `${activeFilters} filters` : "filter"}
            </Button>
          ) : null}
          <div className="tnum ml-auto hidden self-center font-mono text-[11px] text-muted md:block">
            {formatDateTime(from)} → {formatClock(to)}
          </div>
        </form>

        {log.error && !log.data ? (
          <ErrorState error={log.error} onRetry={() => log.mutate()} />
        ) : firstLoad ? (
          <TableSkeleton />
        ) : rows.length === 0 ? (
          <EmptyState title="No matching requests">
            Nothing {activeFilters ? "matches these filters " : "was logged "}in the {presetFor(prefs.range).long.toLowerCase()}.
          </EmptyState>
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[980px] text-[12.5px]">
                <caption className="sr-only">Request log</caption>
                <thead className="sticky top-0 z-10 bg-panel">
                  <tr className="text-left font-mono text-[10.5px] uppercase tracking-[0.06em] text-faint">
                    <Th className="pl-4">Time</Th>
                    <Th>Status</Th>
                    <Th>Model</Th>
                    <Th>Target</Th>
                    <Th>Key</Th>
                    <Th right>Latency</Th>
                    <Th right>TTFT</Th>
                    <Th right>Tokens</Th>
                    <Th right>Cost</Th>
                    <Th>Cache</Th>
                    <Th right className="pr-4">
                      Att.
                    </Th>
                  </tr>
                </thead>
                <tbody className="tnum">
                  {rows.map((r) => {
                    const k = keyIndex(r.key_id);
                    const on = selected?.request_id === r.request_id;
                    return (
                      <tr
                        key={r.request_id}
                        tabIndex={0}
                        aria-label={`Request ${r.request_id}, status ${r.status}`}
                        onClick={() => setSelected(r)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            setSelected(r);
                          }
                        }}
                        className={cx(
                          "cursor-pointer border-t border-line transition-colors duration-100 hover:bg-hover/60 focus-visible:bg-hover focus-visible:outline-none",
                          on && "bg-hover",
                        )}
                      >
                        <td className="whitespace-nowrap py-2 pl-4 pr-2 font-mono text-[11.5px] text-muted" title={formatDateTime(r.ts)}>
                          {formatClock(r.ts, true)}
                        </td>
                        <td className="px-2 py-2">
                          <span className="flex items-center gap-1.5">
                            <StatusBadge status={r.status} />
                            {r.error_code ? <span className="truncate font-mono text-[11px] text-err/90">{r.error_code}</span> : null}
                          </span>
                        </td>
                        <td className="max-w-[160px] truncate px-2 py-2 font-mono text-[12px] text-fg">{r.model}</td>
                        <td className="max-w-[180px] truncate px-2 py-2 font-mono text-[12px] text-fg-2">{r.target || "—"}</td>
                        <td className="max-w-[140px] truncate px-2 py-2 text-[12px] text-fg-2" title={r.key_id}>
                          {k?.name ?? <span className="font-mono text-[11px] text-muted">{shortId(r.key_id)}</span>}
                        </td>
                        <td className="px-2 py-2 text-right font-mono text-fg">{formatMs(r.latency_ms)}</td>
                        <td className="px-2 py-2 text-right font-mono text-fg-2">{r.ttft_ms ? formatMs(r.ttft_ms) : "—"}</td>
                        <td className="whitespace-nowrap px-2 py-2 text-right font-mono text-fg-2">
                          {formatCompact(r.prompt_tokens)}
                          <span className="text-faint"> → </span>
                          {formatCompact(r.completion_tokens)}
                        </td>
                        <td className="px-2 py-2 text-right font-mono text-fg-2">{formatUsd(r.cost_usd)}</td>
                        <td className="px-2 py-2">
                          <CacheBadge cache={r.cache} />
                        </td>
                        <td className={cx("py-2 pl-2 pr-4 text-right font-mono", r.attempts > 1 ? "text-warn" : "text-muted")}>{r.attempts}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <div className="flex items-center justify-between gap-3 border-t border-line px-4 py-3">
              <span className="tnum font-mono text-[11.5px] text-muted">
                {rows.length.toLocaleString("en-US")} row{rows.length === 1 ? "" : "s"}
                {hasMore ? " loaded" : " · end of results"}
              </span>
              {hasMore ? (
                <Button onClick={() => log.setSize(log.size + 1)} disabled={loadingMore}>
                  {loadingMore ? "Loading…" : `Load ${PAGE} more`}
                </Button>
              ) : null}
            </div>
            {log.error && log.data ? (
              <div className="border-t border-line">
                <ErrorState compact error={log.error} onRetry={() => log.mutate()} />
              </div>
            ) : null}
          </>
        )}
      </Panel>

      <Sheet
        open={!!selected}
        onClose={() => setSelected(null)}
        title="Request"
        subtitle={selected ? <span className="font-mono">{selected.request_id}</span> : undefined}
      >
        {selected ? <RequestDetail r={detail.data ?? selected} keyName={keyIndex} /> : null}
      </Sheet>
    </>
  );
}

function shortId(id: string) {
  return id.length > 12 ? `${id.slice(0, 8)}…` : id || "—";
}

function Th({ children, right, className }: { children: React.ReactNode; right?: boolean; className?: string }) {
  return (
    <th scope="col" className={cx("whitespace-nowrap px-2 py-2.5 font-normal", right && "text-right", className)}>
      {children}
    </th>
  );
}

function TableSkeleton() {
  return (
    <div className="flex flex-col" aria-busy="true">
      {Array.from({ length: 12 }, (_, i) => (
        <div key={i} className="flex items-center gap-4 border-t border-line px-4 py-2.5" style={{ opacity: 1 - i * 0.06 }}>
          <Skeleton className="h-3.5 w-20" />
          <Skeleton className="h-4 w-9" />
          <Skeleton className="h-3.5 w-24" />
          <Skeleton className="h-3.5 w-28" />
          <Skeleton className="h-3.5 w-16" />
          <Skeleton className="ml-auto h-3.5 w-14" />
          <Skeleton className="h-3.5 w-12" />
          <Skeleton className="h-3.5 w-16" />
        </div>
      ))}
    </div>
  );
}
