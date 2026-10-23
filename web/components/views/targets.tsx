"use client";

import { IconTargets } from "@/components/icons";
import { EmptyState, ErrorState, PageHeader, Panel, Skeleton, cx } from "@/components/ui";
import type { Target } from "@/lib/api";
import { formatCompact, formatMs, formatPct } from "@/lib/format";
import { useTargets } from "@/lib/hooks";

const STATES = {
  closed: { label: "Closed", tone: "ok", color: "var(--ok)", help: "Healthy: traffic flows normally." },
  half_open: { label: "Half-open", tone: "warn", color: "var(--warn)", help: "Probing: a trial request decides whether to close." },
  open: { label: "Open", tone: "err", color: "var(--err)", help: "Tripped: traffic is routed to fallbacks until the cool-down ends." },
} as const;

const FLOW = {
  closed: "Requests flow to the target. When the failure rate over the sliding window crosses the threshold (with enough volume), the breaker trips.",
  open: "The target is skipped and the router falls back to the next target for the model. After the open duration elapses, it moves to half-open.",
  half_open: "A limited number of trial requests are let through. Success closes the breaker; a failure re-opens it.",
} as const;

function stateOf(s: string) {
  return STATES[s as keyof typeof STATES] ?? { label: s, tone: "neutral", color: "var(--faint)", help: "Unknown state" };
}

export function TargetsView() {
  const targets = useTargets();
  const list = targets.data ?? [];
  const counts = { closed: 0, half_open: 0, open: 0 } as Record<string, number>;
  for (const t of list) counts[t.state] = (counts[t.state] ?? 0) + 1;

  return (
    <>
      <PageHeader
        title="Targets"
        description="Upstream provider/model pairs and their circuit breakers. Stats cover the last 15 minutes; refreshes every 5s."
        meta={
          targets.data ? (
            <div className="flex flex-wrap items-center gap-3 font-mono text-[11px] text-muted">
              {(["closed", "half_open", "open"] as const).map((s) => (
                <span key={s} className="inline-flex items-center gap-1.5" title={STATES[s].help}>
                  <span className="size-2 rounded-full" style={{ background: STATES[s].color }} />
                  {counts[s] ?? 0} {STATES[s].label.toLowerCase()}
                </span>
              ))}
            </div>
          ) : null
        }
      />

      {targets.error && !targets.data ? (
        <Panel>
          <ErrorState error={targets.error} onRetry={() => targets.mutate()} />
        </Panel>
      ) : !targets.data ? (
        <Grid>
          {Array.from({ length: 4 }, (_, i) => (
            <div key={i} className="rounded-xl border border-line bg-panel p-4">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="mt-2 h-3 w-24" />
              <div className="mt-6 grid grid-cols-3 gap-3">
                <Skeleton className="h-8" />
                <Skeleton className="h-8" />
                <Skeleton className="h-8" />
              </div>
            </div>
          ))}
        </Grid>
      ) : list.length === 0 ? (
        <Panel>
          <EmptyState icon={<IconTargets size={18} />} title="No targets configured">
            Add models with targets under <code className="font-mono">models:</code> in <code className="font-mono">switchyard.yaml</code>.
          </EmptyState>
        </Panel>
      ) : (
        <Grid>
          {list.map((t, i) => (
            <TargetCard key={t.target} t={t} delay={i * 40} />
          ))}
        </Grid>
      )}

      <Panel className="mt-6" title="How the breaker works" subtitle="Per-target circuit breaker in the router">
        <ol className="grid grid-cols-1 divide-y divide-[color:var(--line)] md:grid-cols-3 md:divide-x md:divide-y-0">
          {(["closed", "open", "half_open"] as const).map((s, i) => (
            <li key={s} className="flex gap-3 p-4">
              <span className="tnum mt-px font-mono text-[11px] text-faint">0{i + 1}</span>
              <div>
                <div className="flex items-center gap-2 text-[13px] font-medium text-fg">
                  <span className="size-2 rounded-full" style={{ background: STATES[s].color, boxShadow: `0 0 6px ${STATES[s].color}` }} />
                  {STATES[s].label}
                </div>
                <p className="mt-1 text-[12.5px] leading-relaxed text-muted">{FLOW[s]}</p>
              </div>
            </li>
          ))}
        </ol>
      </Panel>
    </>
  );
}

function Grid({ children }: { children: React.ReactNode }) {
  return <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">{children}</div>;
}

function TargetCard({ t, delay }: { t: Target; delay: number }) {
  const s = stateOf(t.state);
  const [provider, ...rest] = t.target.split("/");
  const model = rest.join("/");
  const er = t.requests_15m ? t.errors_15m / t.requests_15m : 0;
  return (
    <article
      className={cx(
        "rise relative overflow-hidden rounded-xl border bg-panel p-4",
        t.state === "open" ? "border-[color:var(--err)]/35" : t.state === "half_open" ? "border-[color:var(--warn)]/35" : "border-line",
      )}
      style={{ animationDelay: `${delay}ms` }}
    >
      {t.state !== "closed" ? (
        <div
          aria-hidden="true"
          className="pointer-events-none absolute -right-10 -top-10 size-40 rounded-full opacity-[0.12] blur-2xl"
          style={{ background: s.color }}
        />
      ) : null}
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="font-mono text-[11px] text-muted">{model ? provider : "target"}</div>
          <h2 className="truncate font-mono text-[15px] font-medium text-fg" title={t.target}>
            {model || t.target}
          </h2>
        </div>
        <SignalHead state={t.state} />
      </div>
      <div className="mt-1 text-[11.5px] text-muted">{s.help}</div>

      <dl className="mt-4 grid grid-cols-3 gap-px overflow-hidden rounded-lg border border-line bg-[color:var(--line)]">
        <Metric k="Requests" v={formatCompact(t.requests_15m)} />
        <Metric
          k="Errors"
          v={t.requests_15m ? formatPct(er) : "—"}
          sub={t.errors_15m ? formatCompact(t.errors_15m) : undefined}
          tone={er >= 0.05 ? "err" : er > 0 ? "warn" : undefined}
        />
        <Metric k="p95" v={t.requests_15m ? formatMs(t.latency_p95_ms_15m) : "—"} />
      </dl>
    </article>
  );
}

/** A three-aspect railway signal head; the lit lamp is the breaker state. */
function SignalHead({ state }: { state: string }) {
  const s = stateOf(state);
  const lamps = [
    { id: "open", color: "var(--err)" },
    { id: "half_open", color: "var(--warn)" },
    { id: "closed", color: "var(--ok)" },
  ];
  return (
    <div className="flex shrink-0 items-center gap-2">
      <span
        className="rounded-md border px-1.5 py-0.5 font-mono text-[10.5px] font-medium uppercase tracking-[0.06em]"
        style={{ color: s.color, borderColor: `color-mix(in srgb, ${s.color} 35%, transparent)`, background: `color-mix(in srgb, ${s.color} 12%, transparent)` }}
      >
        {s.label}
      </span>
      <div className="flex flex-col gap-[3px] rounded-[7px] border border-line-strong bg-bg p-[3px]" aria-hidden="true">
        {lamps.map((l) => {
          const lit = l.id === state;
          return (
            <span
              key={l.id}
              className={cx("size-[7px] rounded-full", lit && state !== "closed" && "lamp-pulse")}
              style={{ background: lit ? l.color : "var(--panel-2)", color: l.color, boxShadow: lit ? `0 0 6px ${l.color}` : undefined }}
            />
          );
        })}
      </div>
    </div>
  );
}

function Metric({ k, v, sub, tone }: { k: string; v: string; sub?: string; tone?: "err" | "warn" }) {
  return (
    <div className="bg-panel px-3 py-2.5">
      <dt className="font-mono text-[10px] uppercase tracking-[0.08em] text-faint">{k}</dt>
      <dd className={cx("tnum mt-1 text-[16px] font-semibold tracking-[-0.02em]", tone === "err" ? "text-err" : tone === "warn" ? "text-warn" : "text-fg")}>
        {v}
        {sub ? <span className="ml-1.5 font-mono text-[11px] font-normal text-muted">{sub}</span> : null}
      </dd>
    </div>
  );
}
