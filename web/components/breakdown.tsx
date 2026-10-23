"use client";

import type { Group } from "@/lib/api";
import { formatCompact, formatMs, formatPct, formatUsd } from "@/lib/format";
import { Skeleton, cx } from "./ui";

/** Grouped totals with an inline share bar behind each name. */
export function BreakdownTable({
  groups,
  label,
  renderKey,
  loading,
}: {
  groups: Group[] | undefined;
  label: string;
  renderKey?: (key: string) => React.ReactNode;
  loading?: boolean;
}) {
  if (loading || !groups) {
    return (
      <div className="flex flex-col gap-2.5 p-4">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-6" style={{ opacity: 1 - i * 0.15 }} />
        ))}
      </div>
    );
  }
  const total = groups.reduce((s, g) => s + g.requests, 0) || 1;
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[560px] text-[12.5px]">
        <caption className="sr-only">Breakdown by {label}</caption>
        <thead>
          <tr className="text-left font-mono text-[10.5px] uppercase tracking-[0.06em] text-faint">
            <th scope="col" className="py-2 pl-4 pr-2 font-normal">
              {label}
            </th>
            <th scope="col" className="px-2 py-2 text-right font-normal">
              Requests
            </th>
            <th scope="col" className="px-2 py-2 text-right font-normal">
              Errors
            </th>
            <th scope="col" className="px-2 py-2 text-right font-normal">
              Tokens
            </th>
            <th scope="col" className="px-2 py-2 text-right font-normal">
              Cost
            </th>
            <th scope="col" className="py-2 pl-2 pr-4 text-right font-normal">
              p95
            </th>
          </tr>
        </thead>
        <tbody className="tnum">
          {groups.map((g) => {
            const share = g.requests / total;
            const er = g.requests ? g.errors / g.requests : 0;
            return (
              <tr key={g.key} className="border-t border-line transition-colors hover:bg-hover/50">
                <th scope="row" className="relative py-2 pl-4 pr-2 text-left font-normal">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-mono text-[12px] text-fg">{renderKey ? renderKey(g.key) : g.key || "—"}</span>
                    <span className="font-mono text-[10.5px] text-faint">{formatPct(share)}</span>
                  </div>
                  <div className="mt-1 h-[3px] w-full max-w-[220px] overflow-hidden rounded-full bg-panel-2">
                    <div className="h-full rounded-full bg-signal/70" style={{ width: `${Math.max(1, share * 100)}%` }} />
                  </div>
                </th>
                <td className="px-2 py-2 text-right font-mono text-fg">{formatCompact(g.requests)}</td>
                <td className={cx("px-2 py-2 text-right font-mono", er >= 0.05 ? "text-err" : er > 0 ? "text-warn" : "text-muted")}>
                  {formatPct(er)}
                </td>
                <td className="px-2 py-2 text-right font-mono text-fg-2">{formatCompact(g.tokens)}</td>
                <td className="px-2 py-2 text-right font-mono text-fg-2">{formatUsd(g.cost_usd)}</td>
                <td className="py-2 pl-2 pr-4 text-right font-mono text-fg-2">{formatMs(g.latency_p95_ms)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
