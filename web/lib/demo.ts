// Demo mode (NEXT_PUBLIC_DEMO=1): the dashboard runs without a backend.
// demoFetch answers admin-API calls from responses recorded from a real
// gateway (public/demo, see scripts/capture-demo.mjs), shifted so they end
// "now"; demoLive synthesizes the live tail from the same recordings. Key
// changes are kept in memory for the session.
import type { ApiKey, KeyCreateInput, KeyUpdateInput, LiveEvent, LiveMessage, RequestRow } from "./api";

export const IS_DEMO = process.env.NEXT_PUBLIC_DEMO === "1";
export const DEMO_TOKEN = "demo";
export const REPO_URL = process.env.NEXT_PUBLIC_REPO_URL ?? "";

const PRESETS: [string, number][] = [
  ["15m", 15 * 60e3],
  ["1h", 3600e3],
  ["6h", 6 * 3600e3],
  ["24h", 24 * 3600e3],
  ["7d", 7 * 24 * 3600e3],
];

interface Meta {
  captured_at: number;
  projects: string[];
}

const cache = new Map<string, Promise<unknown>>();
function load<T>(path: string): Promise<T> {
  let p = cache.get(path);
  if (!p) {
    p = fetch(`/demo/${path}`).then((r) => {
      if (!r.ok) throw new Error(`demo data missing: ${path}`);
      return r.json();
    });
    cache.set(path, p);
  }
  return p as Promise<T>;
}

const meta = () => load<Meta>("meta.json");

/** Picks the recorded range closest to the requested window length. */
export function nearestPreset(ms: number): string {
  let best = PRESETS[0]!;
  for (const p of PRESETS) if (Math.abs(Math.log(p[1] / ms)) < Math.abs(Math.log(best[1] / ms))) best = p;
  return best[0];
}

const shiftIso = (iso: string, by: number) => new Date(Date.parse(iso) + by).toISOString();

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function err(status: number, message: string): Response {
  return json({ error: { message } }, status);
}

// In-memory key store for the session (seeded from the recording).
let keys: ApiKey[] | null = null;
async function keyStore(): Promise<ApiKey[]> {
  keys ??= (await load<{ data: ApiKey[] }>("keys.json")).data.map((k) => ({ ...k }));
  return keys;
}

function randomSecret(): string {
  const a = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";
  const b = new Uint8Array(40);
  crypto.getRandomValues(b);
  return "sy-demo" + Array.from(b.slice(0, 36), (x) => a[x % a.length]).join("");
}

async function requestPool(preset: string, shift: number): Promise<RequestRow[]> {
  const rows = await load<RequestRow[]>(`${preset}/requests.json`);
  return rows.map((r) => ({ ...r, ts: shiftIso(r.ts, shift) }));
}

function matchStatus(status: number, want: string | null): boolean {
  if (!want) return true;
  if (want === "ok") return status < 400;
  if (want === "error") return status >= 400;
  return String(status) === want;
}

/** A fetch implementation serving the admin API from recordings. */
export const demoFetch: typeof fetch = async (input, init) => {
  const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://demo");
  const q = url.searchParams;
  const method = (init?.method ?? "GET").toUpperCase();
  const m = await meta();
  const shift = Date.now() - m.captured_at;
  const from = Number(q.get("from")) || Date.now() - 3600e3;
  const to = Number(q.get("to")) || Date.now();
  const preset = nearestPreset(to - from);
  const project = q.get("project") && m.projects.includes(q.get("project")!) ? q.get("project")! : "all";
  // The demo client's base URL is "demo", so paths arrive as /demo/admin/…
  const path = url.pathname.replace(/^\/demo(?=\/admin\/)/, "");

  switch (true) {
    case path === "/admin/api/overview":
      return json({ ...(await load<object>(`${preset}/${project}/overview.json`)), query_ms: 4 + Math.random() * 20 });

    case path === "/admin/api/timeseries": {
      const ts = await load<{ interval_seconds: number; points: { t: string }[] }>(`${preset}/${project}/timeseries.json`);
      // Shift by whole buckets so points stay on bucket boundaries.
      const bucket = ts.interval_seconds * 1000;
      const aligned = Math.floor(shift / bucket) * bucket;
      return json({ ...ts, points: ts.points.map((p) => ({ ...p, t: shiftIso(p.t, aligned) })) });
    }

    case path === "/admin/api/breakdown": {
      const by = q.get("by") ?? "model";
      const dir = project === "all" || by !== "project" ? project : "all";
      return load(`${preset}/${dir}/breakdown-${by}.json`).then(json, () => err(400, "unknown breakdown dimension"));
    }

    case path === "/admin/api/requests": {
      let rows = await requestPool(preset, shift);
      const model = q.get("model");
      const key = q.get("key");
      rows = rows.filter(
        (r) =>
          (!model || r.model === model) &&
          (!key || r.key_id === key) &&
          (project === "all" || r.project_id === project) &&
          matchStatus(r.status, q.get("status")),
      );
      const offset = Number(q.get("cursor") ?? 0) || 0;
      const limit = Math.min(Number(q.get("limit") ?? 50) || 50, 500);
      const page = rows.slice(offset, offset + limit);
      const next = offset + limit < rows.length ? String(offset + limit) : "";
      return json({ data: page, next_cursor: next });
    }

    case path.startsWith("/admin/api/requests/"): {
      const id = decodeURIComponent(path.slice("/admin/api/requests/".length));
      for (const [p] of PRESETS) {
        const hit = (await requestPool(p, shift)).find((r) => r.request_id === id);
        if (hit) return json(hit);
      }
      return err(404, "request not found");
    }

    case path === "/admin/api/keys" && method === "GET":
      return json({ data: await keyStore() });

    case path === "/admin/api/keys" && method === "POST": {
      const input = JSON.parse(String(init?.body ?? "{}")) as KeyCreateInput;
      if (!input.name?.trim()) return err(400, "name is required");
      const secret = randomSecret();
      const projectName = input.project?.trim() || "default";
      const store = await keyStore();
      const key: ApiKey = {
        id: crypto.randomUUID(),
        project_id: store.find((k) => k.project === projectName)?.project_id ?? crypto.randomUUID(),
        project: projectName,
        name: input.name.trim(),
        prefix: secret.slice(0, 8),
        allowed_models: input.allowed_models ?? [],
        rpm_limit: input.rpm_limit ?? null,
        tpm_limit: input.tpm_limit ?? null,
        monthly_budget_usd: input.monthly_budget_usd ?? null,
        spent_this_month_usd: 0,
        disabled: false,
        created_at: new Date().toISOString(),
        last_used_at: null,
      };
      store.unshift(key);
      return json({ key, secret }, 201);
    }

    case path.startsWith("/admin/api/keys/") && method === "PATCH": {
      const id = decodeURIComponent(path.slice("/admin/api/keys/".length));
      const store = await keyStore();
      const k = store.find((x) => x.id === id);
      if (!k) return err(404, "key not found");
      const u = JSON.parse(String(init?.body ?? "{}")) as KeyUpdateInput;
      if (u.name !== undefined) k.name = u.name;
      if (u.allowed_models !== undefined) k.allowed_models = u.allowed_models;
      if (u.disabled !== undefined) k.disabled = u.disabled;
      if (u.rpm_limit !== undefined) k.rpm_limit = u.rpm_limit;
      if (u.tpm_limit !== undefined) k.tpm_limit = u.tpm_limit;
      if (u.monthly_budget_usd !== undefined) k.monthly_budget_usd = u.monthly_budget_usd;
      if (u.clear_rpm_limit) k.rpm_limit = null;
      if (u.clear_tpm_limit) k.tpm_limit = null;
      if (u.clear_monthly_budget) k.monthly_budget_usd = null;
      return json(k);
    }

    case path === "/admin/api/targets":
      return json(await load("targets.json"));

    default:
      return err(404, "not found in demo");
  }
};

/**
 * Starts a synthetic live tail: every 250 ms it emits a batch of events
 * cloned from recorded requests, at roughly `rate` events per second.
 * Returns a stop function.
 */
export function demoLive(onMessage: (m: LiveMessage) => void, rate = 24): () => void {
  let stopped = false;
  let pool: RequestRow[] = [];
  void load<RequestRow[]>("1h/requests.json").then((rows) => {
    pool = rows;
  });
  const id = window.setInterval(() => {
    if (stopped || pool.length === 0) return;
    const n = Math.max(0, Math.round(rate / 4 + (Math.random() - 0.5) * (rate / 4)));
    const now = Date.now();
    const events: LiveEvent[] = [];
    for (let i = 0; i < n; i++) {
      const r = pool[Math.floor(Math.random() * pool.length)]!;
      const jitter = 0.75 + Math.random() * 0.5;
      events.push({
        ts: now - Math.floor(Math.random() * 250),
        request_id: crypto.randomUUID().replace(/-/g, "").slice(0, 26).toUpperCase(),
        project_id: r.project_id,
        key_id: r.key_id,
        model: r.model,
        target: r.target,
        status: r.status,
        error_code: r.error_code || undefined,
        stream: r.stream,
        cache: r.cache,
        attempts: r.attempts,
        latency_ms: r.latency_ms * jitter,
        ttft_ms: r.ttft_ms * jitter,
        prompt_tokens: r.prompt_tokens,
        completion_tokens: r.completion_tokens,
        cost_usd: r.cost_usd,
      } as LiveEvent);
    }
    events.sort((a, b) => a.ts - b.ts);
    onMessage({ events, skipped: 0 });
  }, 250);
  return () => {
    stopped = true;
    window.clearInterval(id);
  };
}
