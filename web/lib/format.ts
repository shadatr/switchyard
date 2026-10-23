// Number/time formatters. Every function is total: NaN, Infinity and
// nullish inputs render as an em dash instead of "NaN".

const DASH = "—";

function finite(n: number | null | undefined): n is number {
  return typeof n === "number" && Number.isFinite(n);
}

/** 1234 → "1.2k", 1_500_000 → "1.5M"; small integers stay exact. */
export function formatCompact(n: number | null | undefined): string {
  if (!finite(n)) return DASH;
  const abs = Math.abs(n);
  if (abs < 1000) return Number.isInteger(n) ? String(n) : n.toFixed(1);
  const units: [number, string][] = [
    [1e12, "T"],
    [1e9, "B"],
    [1e6, "M"],
    [1e3, "k"],
  ];
  for (const [v, s] of units) {
    if (abs >= v * 0.99995) {
      const x = n / v;
      const digits = Math.abs(x) >= 100 ? 0 : 1;
      return `${trimZeros(x.toFixed(digits))}${s}`;
    }
  }
  return String(n);
}

/** Full integer with thousands separators: 1234567 → "1,234,567". */
export function formatInt(n: number | null | undefined): string {
  if (!finite(n)) return DASH;
  return Math.round(n).toLocaleString("en-US");
}

/** Milliseconds with 1–2 decimals; switches to seconds at 10s. */
export function formatMs(ms: number | null | undefined): string {
  if (!finite(ms)) return DASH;
  if (ms === 0) return "0 ms";
  const abs = Math.abs(ms);
  if (abs >= 10_000) return `${trimZeros((ms / 1000).toFixed(1))} s`;
  if (abs >= 100) return `${ms.toFixed(0)} ms`;
  if (abs >= 10) return `${ms.toFixed(1)} ms`;
  return `${ms.toFixed(2)} ms`;
}

/** Microseconds: 412 → "412 µs", 1530 → "1.53 ms". */
export function formatUs(us: number | null | undefined): string {
  if (!finite(us)) return DASH;
  if (Math.abs(us) >= 1000) return formatMs(us / 1000);
  return `${Math.round(us)} µs`;
}

/**
 * Dollars. Large amounts get 2 decimals; tiny per-request costs keep up to
 * 6 significant decimals so $0.000042 doesn't round to $0.00.
 */
export function formatUsd(n: number | null | undefined): string {
  if (!finite(n)) return DASH;
  if (n === 0) return "$0";
  const abs = Math.abs(n);
  const sign = n < 0 ? "-" : "";
  if (abs >= 1000) return `${sign}$${abs.toLocaleString("en-US", { maximumFractionDigits: 0 })}`;
  if (abs >= 1) return `${sign}$${abs.toFixed(2)}`;
  if (abs >= 0.01) return `${sign}$${trimZeros(abs.toFixed(4))}`;
  if (abs < 0.000001) return `${sign}<$0.000001`;
  return `${sign}$${trimZeros(abs.toFixed(6))}`;
}

/** Ratio 0..1 → "12.3%"; tiny non-zero values keep 2 decimals. */
export function formatPct(ratio: number | null | undefined): string {
  if (!finite(ratio)) return DASH;
  const p = ratio * 100;
  if (p === 0) return "0%";
  if (Math.abs(p) < 1) return `${p.toFixed(2)}%`;
  if (Math.abs(p) >= 99.95 && Math.abs(p) < 100) return `${p.toFixed(2)}%`;
  return `${trimZeros(p.toFixed(1))}%`;
}

/** "3s ago", "5m ago", "2h ago", "4d ago". */
export function formatAgo(iso: string | number | null | undefined, now: number = Date.now()): string {
  if (iso === null || iso === undefined || iso === "") return "never";
  const t = typeof iso === "number" ? iso : Date.parse(iso);
  if (!Number.isFinite(t)) return DASH;
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

/** HH:MM:SS.mmm in local time — for log rows. */
export function formatClock(t: string | number, withMs = false): string {
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return DASH;
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  const base = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  return withMs ? `${base}.${p(d.getMilliseconds(), 3)}` : base;
}

/** "Oct 23, 14:05:09" in local time. */
export function formatDateTime(t: string | number): string {
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return DASH;
  const mon = d.toLocaleString("en-US", { month: "short" });
  return `${mon} ${d.getDate()}, ${formatClock(t)}`;
}

/** Axis label for a bucket timestamp given the bucket width. */
export function formatBucket(t: string | number, intervalSeconds: number): string {
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  if (intervalSeconds >= 86_400) return d.toLocaleString("en-US", { month: "short", day: "numeric" });
  if (intervalSeconds >= 3600 && (d.getHours() === 0 || intervalSeconds >= 3 * 3600)) {
    return `${d.toLocaleString("en-US", { month: "short", day: "numeric" })} ${p(d.getHours())}:00`;
  }
  if (intervalSeconds < 60) return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Human interval: 10 → "10s", 300 → "5m", 3600 → "1h". */
export function formatInterval(seconds: number): string {
  if (!finite(seconds) || seconds <= 0) return DASH;
  if (seconds % 86_400 === 0) return `${seconds / 86_400}d`;
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

function trimZeros(s: string): string {
  return s.includes(".") ? s.replace(/\.?0+$/, "") : s;
}

/** Short reason phrase for the status codes the gateway emits. */
export function statusText(code: number): string {
  const m: Record<number, string> = {
    200: "OK",
    400: "Bad request",
    401: "Unauthorized",
    402: "Budget exceeded",
    403: "Model not allowed",
    404: "Not found",
    429: "Rate limited",
    499: "Client closed",
    500: "Server error",
    502: "Upstream error",
    503: "Unavailable",
    504: "Upstream timeout",
  };
  return m[code] ?? "";
}
