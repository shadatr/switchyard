"use client";

import type { ReactNode } from "react";
import type { ApiKey } from "@/lib/api";
import { formatDateTime, formatInt, formatMs, formatUs, formatUsd, statusText } from "@/lib/format";
import { Badge, CopyButton, StatusBadge, cx } from "./ui";

/** Fields shared by REST request rows and live events. */
export interface RequestLike {
  ts: string | number;
  request_id: string;
  project_id: string;
  key_id: string;
  model: string;
  target: string;
  attempts: number;
  status: number;
  error_code?: string;
  stream: boolean;
  cache: string;
  prompt_tokens: number;
  completion_tokens: number;
  tokens_estimated?: boolean;
  cost_usd: number;
  latency_ms: number;
  ttft_ms: number;
  upstream_ms?: number;
  gateway_overhead_us?: number;
}

export function CacheBadge({ cache }: { cache: string }) {
  if (cache === "hit") return <Badge tone="info">hit</Badge>;
  if (cache === "miss") return <span className="font-mono text-[11px] text-muted">miss</span>;
  return <span className="font-mono text-[11px] text-faint">{cache || "—"}</span>;
}

export function RequestDetail({ r, keyName }: { r: RequestLike; keyName?: (id: string) => ApiKey | undefined }) {
  const key = keyName?.(r.key_id);
  const ok = r.status < 400;
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge status={r.status} />
        <span className="text-[13px] text-fg-2">{statusText(r.status)}</span>
        {r.error_code ? <Badge tone="err">{r.error_code}</Badge> : null}
        {r.stream ? <Badge tone="neutral">stream</Badge> : null}
        {r.cache === "hit" ? <Badge tone="info">cache hit</Badge> : null}
      </div>

      <Timing r={r} />

      <Section title="Routing">
        <Row k="Model alias" v={<Mono>{r.model || "—"}</Mono>} />
        <Row k="Target" v={<Mono>{r.target || "—"}</Mono>} />
        <Row
          k="Attempts"
          v={
            <span className="flex items-center gap-2">
              <Mono>{r.attempts}</Mono>
              {r.attempts > 1 ? <Badge tone="warn">fallback / retry</Badge> : null}
            </span>
          }
        />
        <Row k="Streaming" v={<Mono>{r.stream ? "yes" : "no"}</Mono>} />
        <Row k="Cache" v={<CacheBadge cache={r.cache} />} />
        <Row k="Error code" v={<Mono className={r.error_code ? "text-err" : "text-faint"}>{r.error_code || "none"}</Mono>} />
      </Section>

      <Section title="Tokens & cost">
        <Row k="Prompt" v={<Mono>{formatInt(r.prompt_tokens)}</Mono>} />
        <Row k="Completion" v={<Mono>{formatInt(r.completion_tokens)}</Mono>} />
        <Row
          k="Total"
          v={
            <span className="flex items-center gap-2">
              <Mono>{formatInt(r.prompt_tokens + r.completion_tokens)}</Mono>
              {r.tokens_estimated ? <Badge tone="warn" title="Upstream didn't report usage; counted by the gateway">estimated</Badge> : null}
            </span>
          }
        />
        <Row k="Cost" v={<Mono className={ok ? "text-fg" : "text-muted"}>{formatUsd(r.cost_usd)}</Mono>} />
      </Section>

      <Section title="Identity">
        <Row k="Time" v={<Mono>{formatDateTime(r.ts)}</Mono>} />
        <Row k="Request ID" v={<Copyable value={r.request_id} />} />
        <Row
          k="Key"
          v={
            <span className="flex min-w-0 flex-col items-end">
              {key ? <span className="text-[12.5px] text-fg">{key.name}</span> : null}
              <Mono className="text-muted">{r.key_id || "—"}</Mono>
            </span>
          }
        />
        <Row
          k="Project"
          v={
            <span className="flex min-w-0 flex-col items-end">
              {key ? <span className="text-[12.5px] text-fg">{key.project}</span> : null}
              <Mono className="text-muted">{r.project_id || "—"}</Mono>
            </span>
          }
        />
      </Section>
    </div>
  );
}

function Timing({ r }: { r: RequestLike }) {
  const total = Math.max(r.latency_ms, 0.001);
  const upstream = r.upstream_ms ?? null;
  const overheadMs = r.gateway_overhead_us !== undefined ? r.gateway_overhead_us / 1000 : null;
  const pct = (v: number) => `${Math.min(100, Math.max(0, (v / total) * 100))}%`;
  return (
    <Section title="Timings">
      <div className="px-3 pb-3 pt-3">
        <div className="relative h-7 overflow-hidden rounded-md bg-panel-2 shadow-[inset_0_0_0_1px_var(--line)]">
          {upstream !== null ? (
            <div
              className="absolute inset-y-0 left-0 rounded-l-md bg-[color:var(--c-p50)]/35"
              style={{ width: pct(upstream) }}
              title={`Upstream ${formatMs(upstream)}`}
            />
          ) : null}
          {r.ttft_ms > 0 ? (
            <div className="absolute inset-y-0 w-[2px] bg-signal" style={{ left: pct(r.ttft_ms) }} title={`First token at ${formatMs(r.ttft_ms)}`} />
          ) : null}
          <div className="tnum absolute inset-0 flex items-center justify-end px-2 font-mono text-[11px] text-fg">{formatMs(r.latency_ms)}</div>
        </div>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-[2px] bg-[color:var(--c-p50)]/60" /> upstream
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="h-2.5 w-[2px] bg-signal" /> first token
          </span>
        </div>
      </div>
      <Row k="Latency (end-to-end)" v={<Mono>{formatMs(r.latency_ms)}</Mono>} />
      <Row k="Time to first token" v={<Mono>{r.ttft_ms ? formatMs(r.ttft_ms) : "—"}</Mono>} />
      {upstream !== null ? <Row k="Upstream" v={<Mono>{formatMs(upstream)}</Mono>} /> : null}
      {overheadMs !== null ? (
        <Row
          k="Gateway overhead"
          v={<Mono className={overheadMs > 1 ? "text-warn" : "text-ok"}>{formatUs(r.gateway_overhead_us)}</Mono>}
        />
      ) : null}
    </Section>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="mb-1.5 font-mono text-[10.5px] uppercase tracking-[0.08em] text-faint">{title}</h3>
      <dl className="divide-y divide-[color:var(--line)] rounded-lg border border-line bg-panel">{children}</dl>
    </section>
  );
}

function Row({ k, v }: { k: string; v: ReactNode }) {
  return (
    <div className="flex min-h-9 items-center justify-between gap-4 px-3 py-1.5">
      <dt className="shrink-0 text-[12.5px] text-muted">{k}</dt>
      <dd className="min-w-0 text-right">{v}</dd>
    </div>
  );
}

function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cx("tnum break-all font-mono text-[12px] text-fg", className)}>{children}</span>;
}

function Copyable({ value }: { value: string }) {
  return (
    <span className="flex items-center gap-2">
      <Mono className="text-fg-2">{value}</Mono>
      <CopyButton value={value} label="Copy" />
    </span>
  );
}
