// Records admin-API responses from a running gateway into public/demo so the
// dashboard can run without a backend (NEXT_PUBLIC_DEMO=1).
//
//   SWITCHYARD_ADMIN_TOKEN=... node scripts/capture-demo.mjs
//
// API keys are captured without secrets (the API never returns them), and
// project/key ids are kept so filters work.
import { mkdirSync, writeFileSync, rmSync } from "node:fs";

const BASE = (process.env.SWITCHYARD_URL ?? "http://127.0.0.1:8787").replace(/\/+$/, "");
const TOKEN = process.env.SWITCHYARD_ADMIN_TOKEN;
const OUT = new URL("../public/demo/", import.meta.url).pathname;
if (!TOKEN) {
  console.error("SWITCHYARD_ADMIN_TOKEN is required");
  process.exit(1);
}

const PRESETS = { "15m": 15 * 60e3, "1h": 3600e3, "6h": 6 * 3600e3, "24h": 24 * 3600e3, "7d": 7 * 24 * 3600e3 };
const DIMS_ALL = ["model", "target", "status", "key", "project", "cache"];
const DIMS_PROJECT = ["model", "target", "status", "key", "cache"];

async function get(path) {
  const r = await fetch(BASE + path, { headers: { Authorization: `Bearer ${TOKEN}` } });
  if (!r.ok) throw new Error(`${path}: ${r.status} ${await r.text()}`);
  return r.json();
}
const qs = (o) => "?" + new URLSearchParams(Object.entries(o).filter(([, v]) => v !== undefined && v !== "")).toString();
const write = (rel, data) => {
  const p = OUT + rel;
  mkdirSync(p.slice(0, p.lastIndexOf("/")), { recursive: true });
  writeFileSync(p, JSON.stringify(data));
};

rmSync(OUT, { recursive: true, force: true });
const capturedAt = Date.now();
const keys = await get("/admin/api/keys");
const projects = ["", ...new Set(keys.data.map((k) => k.project_id))];

let files = 0;
for (const [preset, ms] of Object.entries(PRESETS)) {
  const range = { from: capturedAt - ms, to: capturedAt };
  for (const project of projects) {
    const dir = `${preset}/${project || "all"}`;
    const f = { ...range, project };
    write(`${dir}/overview.json`, await get("/admin/api/overview" + qs(f)));
    write(`${dir}/timeseries.json`, await get("/admin/api/timeseries" + qs(f)));
    for (const by of project ? DIMS_PROJECT : DIMS_ALL) {
      write(`${dir}/breakdown-${by}.json`, await get("/admin/api/breakdown" + qs({ ...f, by })));
      files++;
    }
    files += 2;
  }
  // A pool of recent requests (plus errors, which are rarer) to page and filter client-side.
  const ok = await get("/admin/api/requests" + qs({ ...range, limit: 300 }));
  const errors = await get("/admin/api/requests" + qs({ ...range, status: "error", limit: 120 }));
  const seen = new Set();
  const pool = [...ok.data, ...errors.data].filter((r) => !seen.has(r.request_id) && seen.add(r.request_id));
  pool.sort((a, b) => (a.ts < b.ts ? 1 : a.ts > b.ts ? -1 : a.request_id < b.request_id ? 1 : -1));
  write(`${preset}/requests.json`, pool);
  files++;
}
write("keys.json", keys);
write("targets.json", await get("/admin/api/targets"));
write("meta.json", { captured_at: capturedAt, projects: projects.filter(Boolean) });
console.log(`captured ${files + 3} files for ${projects.length - 1} projects into public/demo`);
