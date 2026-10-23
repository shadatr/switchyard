"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Sparkline } from "@/components/charts";
import { IconPause, IconPlay } from "@/components/icons";
import { Sheet } from "@/components/overlay";
import { CacheBadge, RequestDetail } from "@/components/request-detail";
import { Button, EmptyState, Kbd, Lamp, PageHeader, Panel, Segmented, Select, StatusBadge, cx } from "@/components/ui";
import { formatClock, formatCompact, formatMs, formatUsd } from "@/lib/format";
import { useKeyIndex, useRequestDetail } from "@/lib/hooks";
import { LIVE_CAP, type LiveRow, type LiveStatus, useLiveTail } from "@/lib/live";

type StatusFilter = "all" | "ok" | "error";

export function LiveView() {
  const [paused, setPaused] = useState(false);
  const live = useLiveTail(paused);
  const [model, setModel] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [selected, setSelected] = useState<LiveRow | null>(null);
  const keyIndex = useKeyIndex();
  const history = useRateHistory(live.rate);
  const lastSeq = useRef(0);

  // Space toggles pause when focus isn't in a control.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null;
      if (e.key !== " " || el?.closest("input, textarea, select, button, a, [role=radio], dialog[open], tr")) return;
      e.preventDefault();
      setPaused((p) => !p);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const models = useMemo(() => [...new Set(live.rows.map((r) => r.model))].sort(), [live.rows]);
  const rows = useMemo(
    () =>
      live.rows.filter(
        (r) => (!model || r.model === model) && (status === "all" || (status === "ok" ? r.status < 400 : r.status >= 400)),
      ),
    [live.rows, model, status],
  );
  const newestSeq = live.rows[0]?.seq ?? 0;
  const prevSeq = lastSeq.current;
  useEffect(() => {
    lastSeq.current = newestSeq;
  }, [newestSeq]);

  const errorsInView = rows.filter((r) => r.status >= 400).length;
  const detail = useRequestDetail(selected?.request_id ?? null);

  return (
    <>
      <PageHeader
        title="Live"
        description="Requests streamed from the gateway as they finish. Newest on top, last 500 kept."
        actions={
          <>
            <Select aria-label="Model" value={model} onChange={(e) => setModel(e.target.value)} className="w-[160px]">
              <option value="">All models</option>
              {models.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
              {model && !models.includes(model) ? <option value={model}>{model}</option> : null}
            </Select>
            <Segmented<StatusFilter>
              label="Status filter"
              value={status}
              onChange={setStatus}
              options={[
                { id: "all", label: "all" },
                { id: "ok", label: "ok" },
                { id: "error", label: "errors" },
              ]}
            />
            <Button
              variant={paused ? "primary" : "secondary"}
              onClick={() => setPaused((p) => !p)}
              icon={paused ? <IconPlay size={13} /> : <IconPause size={13} />}
              aria-pressed={paused}
              className="min-w-[104px]"
            >
              {paused ? "Resume" : "Pause"}
              <span className="ml-1 hidden sm:inline">
                <Kbd>space</Kbd>
              </span>
            </Button>
          </>
        }
      />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-[1.4fr_1fr_1fr_1fr]">
        <div className="rise relative col-span-2 overflow-hidden rounded-xl border border-line bg-panel px-4 pt-3.5 lg:col-span-1">
          <div className="flex items-center justify-between">
            <span className="font-mono text-[10.5px] uppercase tracking-[0.08em] text-muted">Throughput</span>
            <ConnectionPill status={live.status} retryInMs={live.retryInMs} paused={paused} />
          </div>
          <div className="mt-1.5 flex items-baseline gap-1.5">
            <span className="tnum text-[30px] font-semibold leading-none tracking-[-0.03em]">{live.rate.toFixed(live.rate < 10 ? 1 : 0)}</span>
            <span className="font-mono text-[12px] text-muted">req/s</span>
            <span className="ml-auto font-mono text-[10.5px] text-faint">5s avg · last 60s</span>
          </div>
          <div className="-mx-4 mt-2">
            <Sparkline values={history} height={40} />
          </div>
        </div>
        <Stat label="Received" value={formatCompact(live.received)} sub="since this tab connected" />
        <Stat
          label={paused ? "Buffered" : "In view"}
          value={paused ? formatCompact(live.buffered) : formatCompact(rows.length)}
          sub={paused ? "held while paused" : `${errorsInView} error${errorsInView === 1 ? "" : "s"} · cap ${LIVE_CAP}`}
          tone={paused && live.buffered > 0 ? "warn" : undefined}
        />
        <Stat
          label="Skipped"
          value={formatCompact(live.lastSkipped)}
          sub={live.skippedBatches ? `reported in ${live.skippedBatches} batch${live.skippedBatches === 1 ? "" : "es"}` : "server kept up"}
          tone={live.lastSkipped > 0 ? "warn" : undefined}
          title="Events the gateway couldn't deliver to this tab (as reported in the latest batch)"
        />
      </div>

      <Panel className="mt-4" bodyClassName="flex flex-col">
        {rows.length === 0 ? (
          live.status === "open" || live.rows.length > 0 ? (
            <EmptyState
              title={live.rows.length ? "No events match these filters" : "Waiting for traffic…"}
              icon={<Lamp tone="signal" pulse className="size-2.5" />}
            >
              {live.rows.length
                ? "Loosen the model or status filter to see the stream."
                : "Connected. Events appear here the moment a request finishes."}
            </EmptyState>
          ) : (
            <EmptyState title={live.status === "unauthorized" ? "Not authorized" : "Connecting to live tail…"} icon={<Lamp tone="warn" pulse />}>
              {live.retryInMs !== null ? `Retrying in ${(live.retryInMs / 1000).toFixed(1)}s with backoff.` : "Opening the WebSocket."}
            </EmptyState>
          )
        ) : (
          <div className="max-h-[calc(100dvh-330px)] min-h-[320px] overflow-auto">
            <table className="w-full min-w-[920px] text-[12.5px]">
              <caption className="sr-only">Live requests</caption>
              <thead className="sticky top-0 z-10 bg-panel shadow-[0_1px_0_var(--line)]">
                <tr className="text-left font-mono text-[10.5px] uppercase tracking-[0.06em] text-faint">
                  <th scope="col" className="py-2.5 pl-4 pr-2 font-normal">
                    Time
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Status
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Model
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Target
                  </th>
                  <th scope="col" className="px-2 text-right font-normal">
                    Latency
                  </th>
                  <th scope="col" className="px-2 text-right font-normal">
                    TTFT
                  </th>
                  <th scope="col" className="px-2 text-right font-normal">
                    Tokens
                  </th>
                  <th scope="col" className="px-2 text-right font-normal">
                    Cost
                  </th>
                  <th scope="col" className="px-2 font-normal">
                    Cache
                  </th>
                  <th scope="col" className="py-2.5 pl-2 pr-4 text-right font-normal">
                    Att.
                  </th>
                </tr>
              </thead>
              <tbody className="tnum">
                {rows.map((r) => (
                  <tr
                    key={r.seq}
                    tabIndex={0}
                    onClick={() => setSelected(r)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") setSelected(r);
                    }}
                    className={cx(
                      "cursor-pointer border-t border-line hover:bg-hover/60 focus-visible:bg-hover focus-visible:outline-none",
                      prevSeq > 0 && r.seq > prevSeq && !paused && "flash-in",
                    )}
                  >
                    <td className="whitespace-nowrap py-1.5 pl-4 pr-2 font-mono text-[11.5px] text-muted">{formatClock(r.ts, true)}</td>
                    <td className="px-2 py-1.5">
                      <span className="flex items-center gap-1.5">
                        <StatusBadge status={r.status} />
                        {r.error_code ? <span className="font-mono text-[11px] text-err/90">{r.error_code}</span> : null}
                      </span>
                    </td>
                    <td className="max-w-[160px] truncate px-2 py-1.5 font-mono text-[12px] text-fg">{r.model}</td>
                    <td className="max-w-[180px] truncate px-2 py-1.5 font-mono text-[12px] text-fg-2">{r.target || "—"}</td>
                    <td className="px-2 py-1.5 text-right font-mono text-fg">{formatMs(r.latency_ms)}</td>
                    <td className="px-2 py-1.5 text-right font-mono text-fg-2">{r.ttft_ms ? formatMs(r.ttft_ms) : "—"}</td>
                    <td className="whitespace-nowrap px-2 py-1.5 text-right font-mono text-fg-2">
                      {formatCompact(r.prompt_tokens)}
                      <span className="text-faint"> → </span>
                      {formatCompact(r.completion_tokens)}
                    </td>
                    <td className="px-2 py-1.5 text-right font-mono text-fg-2">{formatUsd(r.cost_usd)}</td>
                    <td className="px-2 py-1.5">
                      <CacheBadge cache={r.cache} />
                    </td>
                    <td className={cx("py-1.5 pl-2 pr-4 text-right font-mono", r.attempts > 1 ? "text-warn" : "text-muted")}>{r.attempts}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
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

/** Samples the rate once a second for the throughput sparkline. */
function useRateHistory(rate: number): number[] {
  const [hist, setHist] = useState<number[]>([]);
  const rateRef = useRef(rate);
  useEffect(() => {
    rateRef.current = rate;
  }, [rate]);
  useEffect(() => {
    const id = window.setInterval(() => setHist((h) => [...h, rateRef.current].slice(-60)), 1000);
    return () => window.clearInterval(id);
  }, []);
  return hist;
}

function ConnectionPill({ status, retryInMs, paused }: { status: LiveStatus; retryInMs: number | null; paused: boolean }) {
  const label =
    status === "open" ? (paused ? "paused" : "streaming") : status === "connecting" ? "connecting" : status === "unauthorized" ? "unauthorized" : "reconnecting";
  const tone = status === "open" ? (paused ? "warn" : "ok") : status === "unauthorized" ? "err" : "warn";
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-line-strong bg-raised px-2 py-0.5 font-mono text-[10.5px] text-fg-2" role="status">
      <Lamp tone={tone} pulse={status === "open" && !paused} className="size-1.5" />
      {label}
      {status === "reconnecting" && retryInMs !== null ? <span className="text-faint">{(retryInMs / 1000).toFixed(1)}s</span> : null}
    </span>
  );
}

function Stat({ label, value, sub, tone, title }: { label: string; value: string; sub: string; tone?: "warn"; title?: string }) {
  return (
    <div className="rise flex flex-col rounded-xl border border-line bg-panel px-4 py-3.5" title={title}>
      <span className="font-mono text-[10.5px] uppercase tracking-[0.08em] text-muted">{label}</span>
      <span className={cx("tnum mt-1.5 text-[24px] font-semibold leading-none tracking-[-0.03em]", tone === "warn" ? "text-warn" : "text-fg")}>
        {value}
      </span>
      <span className="mt-auto pt-2 font-mono text-[11px] text-muted">{sub}</span>
    </div>
  );
}
