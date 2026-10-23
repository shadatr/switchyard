// Typed client for the Switchyard admin API (/admin/api/*) and live tail
// (/admin/live). Types mirror the Go structs in internal/admin and
// internal/live; JSON field names there are authoritative.

export const DEFAULT_BASE_URL = "http://127.0.0.1:8787";

export function baseUrl(): string {
  const v = process.env.NEXT_PUBLIC_SWITCHYARD_URL;
  return (v && v.trim() !== "" ? v.trim() : DEFAULT_BASE_URL).replace(/\/+$/, "");
}

// ---------------------------------------------------------------- types

export interface Percentile {
  p50: number;
  p95: number;
  p99: number;
}

export interface Overview {
  requests: number;
  errors: number;
  error_rate: number;
  cache_hits: number;
  cache_hit_rate: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
  latency_ms: Percentile;
  ttft_ms: Percentile;
  source: "raw" | "rollup_1m" | string;
  query_ms: number;
}

export interface Point {
  t: string; // RFC 3339, UTC
  requests: number;
  errors: number;
  cache_hits: number;
  tokens: number;
  cost_usd: number;
  latency_ms: Percentile;
  ttft_ms: Percentile;
}

export interface Timeseries {
  interval_seconds: number;
  points: Point[];
}

export interface RequestRow {
  ts: string;
  request_id: string;
  project_id: string;
  key_id: string;
  model: string;
  target: string;
  attempts: number;
  status: number;
  error_code: string;
  stream: boolean;
  cache: string;
  prompt_tokens: number;
  completion_tokens: number;
  tokens_estimated: boolean;
  cost_usd: number;
  latency_ms: number;
  ttft_ms: number;
  upstream_ms: number;
  gateway_overhead_us: number;
}

export interface RequestsPage {
  data: RequestRow[];
  next_cursor: string;
}

export type BreakdownDimension = "model" | "key" | "target" | "status" | "project" | "cache";

export interface Group {
  key: string;
  requests: number;
  errors: number;
  tokens: number;
  cost_usd: number;
  latency_p95_ms: number;
}

export interface Breakdown {
  by: BreakdownDimension;
  groups: Group[];
}

export interface ApiKey {
  id: string;
  project_id: string;
  project: string;
  name: string;
  prefix: string;
  allowed_models: string[];
  rpm_limit: number | null;
  tpm_limit: number | null;
  monthly_budget_usd: number | null;
  spent_this_month_usd: number | null;
  disabled: boolean;
  created_at: string;
  last_used_at: string | null;
}

export interface KeyCreateInput {
  project?: string;
  name: string;
  allowed_models?: string[];
  rpm_limit?: number;
  tpm_limit?: number;
  monthly_budget_usd?: number;
}

export interface KeyUpdateInput {
  name?: string;
  allowed_models?: string[];
  rpm_limit?: number;
  tpm_limit?: number;
  monthly_budget_usd?: number;
  disabled?: boolean;
  clear_rpm_limit?: boolean;
  clear_tpm_limit?: boolean;
  clear_monthly_budget?: boolean;
}

export interface KeyCreated {
  key: ApiKey;
  secret: string;
}

export type BreakerState = "closed" | "half_open" | "open" | string;

export interface Target {
  target: string;
  state: BreakerState;
  requests_15m: number;
  errors_15m: number;
  latency_p95_ms_15m: number;
}

export interface LiveEvent {
  ts: number; // unix ms
  request_id: string;
  project_id: string;
  key_id: string;
  model: string;
  target: string;
  status: number;
  error_code?: string;
  stream: boolean;
  cache: string;
  attempts: number;
  latency_ms: number;
  ttft_ms: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
}

export interface LiveMessage {
  events: LiveEvent[];
  skipped: number;
}

// --------------------------------------------------------------- errors

export type ApiErrorKind = "unauthorized" | "unconfigured" | "bad_request" | "not_found" | "server" | "network";

export class ApiError extends Error {
  readonly status: number;
  readonly kind: ApiErrorKind;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.kind = kindFor(status);
  }
}

function kindFor(status: number): ApiErrorKind {
  if (status === 0) return "network";
  if (status === 401) return "unauthorized";
  if (status === 503) return "unconfigured";
  if (status === 404) return "not_found";
  if (status >= 400 && status < 500) return "bad_request";
  return "server";
}

// --------------------------------------------------------------- params

export interface RangeFilter {
  from: number; // unix ms
  to: number; // unix ms
  project?: string;
}

export interface RequestFilter extends RangeFilter {
  model?: string;
  status?: string; // "", "ok", "error" or an exact code
  key?: string;
  cursor?: string;
  limit?: number;
}

type ParamValue = string | number | undefined | null;

/** Builds a query string, dropping empty values. Keys keep insertion order. */
export function qs(params: Record<string, ParamValue>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

/** ws(s):// URL for the live tail; the token travels as a query param. */
export function liveUrl(base: string, token: string): string {
  const u = new URL("/admin/live", base);
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  u.searchParams.set("token", token);
  return u.toString();
}

// --------------------------------------------------------------- client

export interface ClientOptions {
  baseUrl: string;
  token: string;
  fetch?: typeof fetch;
}

export class SwitchyardClient {
  readonly baseUrl: string;
  private readonly token: string;
  private readonly fetchImpl: typeof fetch;

  constructor(opts: ClientOptions) {
    this.baseUrl = opts.baseUrl.replace(/\/+$/, "");
    this.token = opts.token;
    this.fetchImpl = opts.fetch ?? ((...a) => globalThis.fetch(...a));
  }

  async request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    let res: Response;
    try {
      res = await this.fetchImpl(this.baseUrl + path, {
        method,
        signal,
        headers: {
          Authorization: `Bearer ${this.token}`,
          ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
        },
        body: body !== undefined ? JSON.stringify(body) : undefined,
        cache: "no-store",
      });
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") throw e;
      throw new ApiError(0, `Can't reach the gateway at ${this.baseUrl}`);
    }
    if (!res.ok) {
      throw new ApiError(res.status, await errorMessage(res));
    }
    return (await res.json()) as T;
  }

  overview(f: RangeFilter) {
    return this.request<Overview>("GET", `/admin/api/overview${qs({ from: f.from, to: f.to, project: f.project })}`);
  }

  timeseries(f: RangeFilter & { interval?: string; model?: string }) {
    return this.request<Timeseries>(
      "GET",
      `/admin/api/timeseries${qs({ from: f.from, to: f.to, project: f.project, model: f.model, interval: f.interval })}`,
    );
  }

  requests(f: RequestFilter) {
    return this.request<RequestsPage>(
      "GET",
      `/admin/api/requests${qs({
        from: f.from,
        to: f.to,
        project: f.project,
        model: f.model,
        status: f.status,
        key: f.key,
        cursor: f.cursor,
        limit: f.limit,
      })}`,
    );
  }

  requestById(id: string) {
    return this.request<RequestRow>("GET", `/admin/api/requests/${encodeURIComponent(id)}`);
  }

  breakdown(by: BreakdownDimension, f: RangeFilter) {
    return this.request<Breakdown>("GET", `/admin/api/breakdown${qs({ by, from: f.from, to: f.to, project: f.project })}`);
  }

  keys() {
    return this.request<{ data: ApiKey[] }>("GET", "/admin/api/keys");
  }

  createKey(input: KeyCreateInput) {
    return this.request<KeyCreated>("POST", "/admin/api/keys", input);
  }

  updateKey(id: string, input: KeyUpdateInput) {
    return this.request<ApiKey>("PATCH", `/admin/api/keys/${encodeURIComponent(id)}`, input);
  }

  targets() {
    return this.request<{ data: Target[] }>("GET", "/admin/api/targets");
  }
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as { error?: { message?: string } };
    if (j?.error?.message) return j.error.message;
  } catch {
    // not JSON
  }
  return `${res.status} ${res.statusText || "request failed"}`.trim();
}

// --------------------------------------------------------- live helpers

/** Exponential backoff with full jitter, capped. attempt starts at 0. */
export function backoffMs(attempt: number, base = 500, cap = 15_000, rand: () => number = Math.random): number {
  const exp = Math.min(cap, base * 2 ** Math.max(0, attempt));
  return Math.round(exp / 2 + (rand() * exp) / 2);
}

export function parseLiveMessage(data: unknown): LiveMessage | null {
  if (typeof data !== "string") return null;
  try {
    const m = JSON.parse(data) as Partial<LiveMessage>;
    if (!m || !Array.isArray(m.events)) return null;
    return { events: m.events, skipped: typeof m.skipped === "number" ? m.skipped : 0 };
  } catch {
    return null;
  }
}
