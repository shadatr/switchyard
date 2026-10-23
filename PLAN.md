# Switchyard — Implementation Plan

> A high-performance LLM gateway written in Go. One OpenAI-compatible endpoint in front of many model providers, with streaming pass-through, routing & fallback, rate limits & budgets, caching, and real-time cost/latency analytics in ClickHouse.

This document is the single source of truth. An engineer (human or AI) should be able to build the whole project by executing the phases **in order**, checking every **Acceptance** box before moving on. Do not skip ahead. Do not add features that are not in this plan without updating the plan first.

---

## 0. Ground rules

1. **Original work only.** No code, names, schemas or screenshots from any employer (Bull Technology, NanoShield, FLARE). Everything is written from scratch here.
2. **Never commit secrets.** Real provider keys live only in `.env` (git-ignored). `.env.example` contains placeholders.
3. **Small, verifiable steps.** Each phase ends with green tests and a commit: `phase N: <summary>`.
4. **Hot path rule.** Nothing on the request path may block on ClickHouse, Postgres or the dashboard. Redis is the only network dependency allowed on the hot path, and it must have a timeout and a fail-open policy (see §6.4).
5. **Measure, don't claim.** Every performance number in README/resume must come from `bench/` scripts in this repo, with machine spec and command recorded in `BENCHMARKS.md`.
6. **Style.** `gofmt`, `go vet`, `staticcheck` clean. Standard library first. Errors wrapped with `%w`. `context.Context` is the first parameter of anything that does I/O. No global mutable state except metrics registries.
7. **Tests run with `-race`** always.

---

## 1. Tech stack (pinned)

| Concern | Choice | Notes |
|---|---|---|
| Language | Go 1.26 | module `github.com/shadatr/switchyard` |
| HTTP | `net/http` (Go 1.22+ pattern routing) | no web framework |
| Config | YAML via `gopkg.in/yaml.v3` + env overrides (`SWITCHYARD_*`) | |
| Redis | `github.com/redis/go-redis/v9`, Redis 7 | rate limits, budgets, cache |
| Postgres | `github.com/jackc/pgx/v5`, Postgres 16, migrations with `goose` | projects, API keys, budgets config |
| ClickHouse | `github.com/ClickHouse/clickhouse-go/v2`, ClickHouse 24.x | request logs + aggregates |
| Metrics | `github.com/prometheus/client_golang` | `/metrics` |
| Tracing (phase 9) | OpenTelemetry Go SDK, OTLP exporter | optional Jaeger in compose |
| WebSocket | `github.com/coder/websocket` | live tail |
| Logging | `log/slog` JSON | |
| Dashboard | Next.js 15 (App Router), TypeScript, Tailwind, Recharts, SWR | in `web/` |
| Load testing | k6 (run via `grafana/k6` Docker image) | in `bench/` |
| Lint | `staticcheck` (`go install honnef.co/go/tools/cmd/staticcheck@latest`) | |
| CI | GitHub Actions | |

---

## 2. Repository layout

```
switchyard/
├── cmd/
│   ├── switchyard/        # main gateway binary
│   └── mockllm/           # fake OpenAI-compatible upstream for tests & benchmarks
├── internal/
│   ├── config/            # YAML + env loading, validation
│   ├── mockllm/           # fake OpenAI-compatible upstream (used by cmd/mockllm and tests)
│   ├── api/               # OpenAI-compatible request/response types
│   ├── sse/               # SSE reader/writer (fuzz-tested)
│   ├── provider/          # Provider interface + adapters
│   │   ├── openai/        # OpenAI & any OpenAI-compatible (OpenRouter, Groq, Together…)
│   │   ├── anthropic/     # Messages API ⇄ OpenAI translation
│   │   ├── gemini/        # Gemini ⇄ OpenAI translation
│   │   └── (mock upstream lives in internal/mockllm)
│   ├── router/            # model aliases, weighted targets, fallback, retries
│   ├── breaker/           # per-target circuit breaker
│   ├── auth/              # API key auth (hashed keys), key cache
│   ├── ratelimit/         # Redis token buckets (RPM, TPM) via Lua + monthly USD budgets
│   ├── pricing/           # per-model price table, cost calculation
│   ├── tokens/            # token estimation when upstream omits usage
│   ├── cache/             # exact-match response cache
│   ├── logsink/           # async batcher → ClickHouse
│   ├── live/              # in-memory broadcaster for live tail
│   ├── store/             # Postgres repositories
│   ├── admin/             # admin API for dashboard
│   ├── metrics/           # Prometheus collectors
│   └── server/            # HTTP wiring, middleware, graceful shutdown
├── migrations/
│   ├── postgres/          # goose SQL migrations
│   └── clickhouse/        # plain SQL, applied at startup (idempotent)
├── web/                   # Next.js dashboard
├── bench/                 # k6 scripts + run.sh + results/
├── deploy/
│   ├── docker-compose.yml
│   ├── switchyard.example.yaml
│   └── Dockerfile
├── docs/
│   ├── architecture.md    # diagrams (Mermaid) + design decisions
│   └── api.md
├── .github/workflows/ci.yml
├── Makefile
├── BENCHMARKS.md
├── PLAN.md
├── README.md
├── .env.example
└── .gitignore
```

---

## 3. Public API (what clients call)

All client endpoints require `Authorization: Bearer sy-<key>`.

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/chat/completions` | OpenAI-compatible chat; `stream: true` supported |
| GET | `/v1/models` | list model **aliases** the key may use |
| GET | `/healthz` | liveness (no deps) |
| GET | `/readyz` | readiness (Redis ping; Postgres & ClickHouse reported but non-fatal) |
| GET | `/metrics` | Prometheus |

Response headers added by the gateway on every chat response:
- `X-Switchyard-Request-Id` (ULID)
- `X-Switchyard-Target` (e.g. `openai/gpt-4o-mini`)
- `X-Switchyard-Cache` (`hit` | `miss` | `bypass`)
- `X-Switchyard-Attempts` (number of upstream attempts)

Error format = OpenAI style: `{"error":{"message":"…","type":"…","code":"…"}}` with status:
`401 invalid_api_key`, `403 model_not_allowed`, `429 rate_limit_exceeded` (+ `Retry-After`), `402 budget_exceeded`, `502 upstream_error`, `504 upstream_timeout`, `400 invalid_request`.

### Admin API (dashboard only)
Requires `Authorization: Bearer <SWITCHYARD_ADMIN_TOKEN>`. Prefix `/admin/api`.

| Method | Path | Returns |
|---|---|---|
| GET | `/admin/api/overview?from&to&project` | totals: requests, errors, tokens in/out, cost, p50/p95/p99 latency & TTFT, cache hit rate |
| GET | `/admin/api/timeseries?metric&interval&from&to&project&model` | buckets for charts |
| GET | `/admin/api/requests?from&to&model&status&key&cursor&limit` | paginated request log (keyset on `(ts, request_id)`) |
| GET | `/admin/api/requests/{id}` | one request incl. attempts |
| GET | `/admin/api/breakdown?by=model|key|target|status` | grouped totals |
| GET/POST/PATCH | `/admin/api/keys` | list / create (returns plaintext once) / update limits, budget, allowed models, disable |
| GET | `/admin/api/targets` | live circuit-breaker state per target |
| GET (WS) | `/admin/live` | stream of request summary events |

---

## 4. Configuration (`switchyard.yaml`)

```yaml
server:
  addr: ":8787"
  read_header_timeout: 5s
  shutdown_grace: 20s
  max_body_bytes: 2097152          # 2 MiB

admin:
  token_env: SWITCHYARD_ADMIN_TOKEN

redis:      { addr: "localhost:16379", timeout: 50ms, fail_open: true }
postgres:   { dsn_env: SWITCHYARD_PG_DSN }
clickhouse: { addr: "localhost:19000", database: "switchyard", username: "default", password_env: SWITCHYARD_CH_PASSWORD }

providers:
  - name: openai
    type: openai                    # openai | anthropic | gemini | mock
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_API_KEY
    timeout: 60s
    connect_timeout: 3s
    max_idle_conns_per_host: 256
  - name: anthropic
    type: anthropic
    base_url: https://api.anthropic.com
    api_key_env: ANTHROPIC_API_KEY
  - name: mock
    type: openai                    # mockllm speaks OpenAI protocol
    base_url: http://localhost:9797/v1
    api_key_env: MOCK_API_KEY

models:                             # aliases clients use
  - alias: fast
    strategy: priority              # priority | weighted
    targets:
      - { provider: openai,    model: gpt-4o-mini }
      - { provider: anthropic, model: claude-haiku-4-5 }
  - alias: mock-fast
    strategy: weighted
    targets:
      - { provider: mock, model: mock-a, weight: 80 }
      - { provider: mock, model: mock-b, weight: 20 }

routing:
  max_attempts: 3
  retry_backoff: { base: 100ms, max: 1s, jitter: true }
  retry_on: [429, 500, 502, 503, 504, timeout, connect_error]
  first_byte_timeout: 20s

breaker:
  failure_rate: 0.5                 # open when ≥50% of requests fail…
  min_requests: 20                  # …over at least 20 requests…
  window: 10s                       # …in a sliding 10s window (10 buckets)
  open_duration: 30s                # then half-open
  half_open_max_requests: 1

cache:
  enabled: true
  ttl: 10m
  only_when_temperature_zero: true
  max_entry_bytes: 262144

pricing:                            # USD per 1M tokens — EXAMPLE values, edit to current list prices
  "openai/gpt-4o-mini":          { input: 0.15, output: 0.60 }
  "anthropic/claude-haiku-4-5":  { input: 1.00, output: 5.00 }
  "mock/mock-a":                 { input: 0.10, output: 0.40 }
  "mock/mock-b":                 { input: 0.20, output: 0.80 }

logsink:
  queue_size: 100000
  batch_size: 5000
  flush_interval: 500ms
```

Validation on startup (fail fast with a clear message): unknown provider in a target, duplicate alias, missing env var for a used provider, weights ≤ 0 under `weighted`, pricing missing for a target (warn, cost = 0).

---

## 5. Data model

### 5.1 Postgres (`migrations/postgres/0001_init.sql`)
```sql
CREATE TABLE projects (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE api_keys (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id       uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name             text NOT NULL,
  prefix           text NOT NULL,                 -- first 8 chars, shown in UI
  hash             bytea NOT NULL UNIQUE,         -- sha256(full key)
  allowed_models   text[] NOT NULL DEFAULT '{}',  -- empty = all
  rpm_limit        integer,                       -- null = unlimited
  tpm_limit        integer,
  monthly_budget_usd numeric(12,4),
  disabled         boolean NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_used_at     timestamptz
);
CREATE INDEX ON api_keys (project_id);
```
Keys are generated as `sy-` + 40 random base62 chars; only the SHA-256 is stored. Auth uses an in-memory LRU (`hash → key record`, TTL 30s) so Postgres is **not** on the hot path for repeat callers.

### 5.2 ClickHouse (`migrations/clickhouse/001_requests.sql`)
```sql
CREATE TABLE IF NOT EXISTS switchyard.requests (
  ts              DateTime64(3, 'UTC'),
  request_id      String,
  project_id      LowCardinality(String),
  key_id          LowCardinality(String),
  model_alias     LowCardinality(String),
  target          LowCardinality(String),      -- provider/model actually used
  attempts        UInt8,
  status          UInt16,
  error_code      LowCardinality(String),
  stream          Bool,
  cache           LowCardinality(String),      -- hit|miss|bypass
  prompt_tokens   UInt32,
  completion_tokens UInt32,
  tokens_estimated Bool,
  cost_usd        Float64,
  latency_ms      UInt32,                      -- total
  ttft_ms         UInt32,                      -- time to first token (stream) or = latency
  upstream_ms     UInt32,
  gateway_overhead_us UInt32                   -- latency minus upstream time
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (project_id, ts, request_id)
TTL toDateTime(ts) + INTERVAL 90 DAY;

CREATE TABLE IF NOT EXISTS switchyard.requests_1m (
  minute DateTime('UTC'), project_id LowCardinality(String), model_alias LowCardinality(String), target LowCardinality(String),
  requests UInt64, errors UInt64, prompt_tokens UInt64, completion_tokens UInt64, cost_usd Float64,
  latency_q AggregateFunction(quantiles(0.5, 0.95, 0.99), UInt32),
  ttft_q    AggregateFunction(quantiles(0.5, 0.95, 0.99), UInt32)
) ENGINE = AggregatingMergeTree ORDER BY (project_id, minute, model_alias, target);

CREATE MATERIALIZED VIEW IF NOT EXISTS switchyard.requests_1m_mv TO switchyard.requests_1m AS
SELECT toStartOfMinute(ts) AS minute, project_id, model_alias, target,
       count() AS requests, countIf(status >= 400) AS errors,
       sum(prompt_tokens) AS prompt_tokens, sum(completion_tokens) AS completion_tokens, sum(cost_usd) AS cost_usd,
       quantilesState(0.5, 0.95, 0.99)(latency_ms) AS latency_q,
       quantilesState(0.5, 0.95, 0.99)(ttft_ms) AS ttft_q
FROM switchyard.requests GROUP BY minute, project_id, model_alias, target;
```
**Note:** the `requests_1m` sum columns must use `SimpleAggregateFunction(sum, …)` types so merges stay correct — implement it that way; the sketch above shows intent.

### 5.3 Redis keys
| Key | Type | Purpose | TTL |
|---|---|---|---|
| `rl:{key_id}:rpm` | hash (tokens, ts) | request token bucket | 2 min |
| `rl:{key_id}:tpm` | hash | token-per-minute bucket | 2 min |
| `budget:{key_id}:{YYYYMM}` | string (micro-USD int) | spend this month | 40 days |
| `cache:{sha256}` | string (gzip JSON) | cached response | `cache.ttl` |

---

## 6. Core design (read before coding)

### 6.1 Request lifecycle (non-streaming)
1. Middleware: request ID (ULID), body size limit, recover, access log, metrics timer.
2. **Auth**: hash bearer key → LRU → Postgres on miss. Reject disabled.
3. Decode body into `api.ChatRequest` (keep raw JSON too — unknown fields are passed through to OpenAI-type providers).
4. **Model check**: alias exists and is allowed for the key.
5. **Rate limit (RPM)**: Lua token bucket, cost 1. Reject 429 with `Retry-After`.
6. **TPM pre-check**: estimate prompt tokens (`tokens.Estimate`), take from TPM bucket; reconcile with real usage after the response (add back or take more; never negative-block after the fact).
7. **Budget pre-check**: reject 402 if `spent ≥ budget`.
8. **Cache lookup** (only if eligible: cache enabled, temperature == 0 or unset+config allows, no tools with side effects flag). Hit → respond, log `cache=hit`, cost 0.
9. **Router**: pick target(s) → for each attempt: skip open breakers; call provider with per-attempt context; on retryable error before any byte was written → backoff+jitter → next target.
10. Write response; compute cost from usage × pricing; increment budget; store in cache (if eligible & 200).
11. Emit a `logsink.Record` (non-blocking) and a `live.Event`.

### 6.2 Streaming specifics
- Set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, `X-Accel-Buffering: no`; use `http.ResponseController` to flush after **each** SSE event.
- Force `stream_options.include_usage = true` on OpenAI-type upstreams so the last chunk carries usage. If usage never arrives, estimate from accumulated text (`tokens_estimated = true`).
- **Fallback is only allowed before the first byte is sent to the client.** After that, an upstream error is forwarded as a final SSE `data: {"error":…}` event followed by `data: [DONE]`.
- Client disconnect → request context cancelled → upstream request cancelled (verify with a test that counts open upstream connections).
- TTFT = time from receiving the client request to flushing the first content chunk.
- Never buffer the whole stream in memory except when caching is eligible (cap at `max_entry_bytes`; if exceeded, skip caching).

### 6.3 Provider interface
```go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req *api.ChatRequest, target string) (*api.ChatResponse, *Meta, error)
    ChatStream(ctx context.Context, req *api.ChatRequest, target string) (Stream, error)
}
type Stream interface {
    Next() (*api.ChatChunk, error) // io.EOF at end
    Usage() *api.Usage               // may be nil until end
    Close() error
}
type Meta struct{ UpstreamLatency time.Duration; StatusCode int }
```
Errors are classified into a typed `*provider.Error{Status int; Retryable bool; Code string}` so the router never inspects strings.
Each provider owns one tuned `*http.Transport` (keep-alives, `MaxIdleConnsPerHost` from config, `ForceAttemptHTTP2: true`, dial/TLS timeouts).

### 6.4 Failure policy (hot path)
| Dependency down | Behavior |
|---|---|
| Redis | rate limit & budget **fail open** (allow), cache bypass, metric `switchyard_dependency_errors_total{dep="redis"}`; log at most once per 10s |
| Postgres | cached keys keep working; unknown keys → 503 |
| ClickHouse | logsink keeps queuing; when queue full → drop newest, metric `switchyard_logs_dropped_total` |
| Provider | breaker + fallback |

### 6.5 Router & breaker
- `priority`: try targets in order. `weighted`: weighted random **without replacement** for the attempt order.
- Breaker per target: closed → (failure rate ≥ `failure_rate` over ≥ `min_requests` in a sliding `window`) → open for `open_duration` → half-open allows `half_open_max_requests` probes → success closes, failure re-opens. Lock-free reads via `atomic`; state changes under a mutex. Exported as metric `switchyard_breaker_state{target}` (0 closed, 1 half-open, 2 open).

### 6.6 Log sink
- `chan Record` of `queue_size`; `Enqueue` uses `select { case ch <- r: default: dropped++ }`.
- One goroutine accumulates into a slice, flushes on `batch_size` or `flush_interval` using `conn.PrepareBatch`. On flush error: retry with backoff up to 3 times, then drop batch (metric). On shutdown: drain with deadline.

### 6.7 Live tail
- `live.Hub` with subscribers; each subscriber has a buffered channel (256). Slow subscriber → drop events for that subscriber (never block the publisher). WS handler pings every 15s.

---

## 7. Mock upstream (`cmd/mockllm`)
OpenAI-compatible `/v1/chat/completions` (stream & non-stream) and `/v1/models`. Behavior from env/flags **and** per-request headers (headers win, for tests):
- `--ttft` (default 200ms); listens on :9797, `--itl` inter-token latency (default 15ms), `--tokens` output tokens (default 100), `--error-rate` (0–1), `--error-status` (default 503), `--jitter` (fraction).
- Headers: `X-Mock-TTFT`, `X-Mock-ITL`, `X-Mock-Tokens`, `X-Mock-Status`, `X-Mock-Fail-After-Tokens`.
- Deterministic output text (`"token-1 token-2 …"`) so tests can assert.
- Returns `usage` in final chunk when `stream_options.include_usage` is true.
- For benchmarks run with `--ttft 0 --itl 0` to isolate gateway overhead.

---

## 8. Phases

Each phase lists **Tasks**, **Files**, **Acceptance** (all must pass), and **Commit**.

### Phase 0 — Scaffold (½ day)
Tasks
1. `git init`; `go mod init github.com/shadatr/switchyard`; create layout dirs with `doc.go` per package.
2. `.gitignore` (bin/, .env, web/node_modules, web/.next, bench/results/*.json), `.env.example`.
3. `Makefile` targets: `build`, `test` (`go test -race ./...`), `lint` (`gofmt -l`, `go vet`, `staticcheck`), `up`/`down` (compose), `run`, `mock`, `bench`, `web`.
4. `deploy/docker-compose.yml`: redis:7, postgres:16, clickhouse/clickhouse-server:24.8 (host ports 16379, 15432, 18123, 19000 (offset to avoid clashing with other local stacks)), healthchecks.
5. `cmd/switchyard/main.go`: loads config, starts server with `/healthz`, graceful shutdown on SIGINT/SIGTERM.
6. `internal/config` with validation + table-driven tests.
7. CI workflow running lint + unit tests.
Acceptance
- [ ] `make lint test build` passes.
- [ ] `make up` brings all 3 services healthy.
- [ ] `curl :8787/healthz` → 200.
- [ ] Invalid config (duplicate alias) exits non-zero with a clear message (test covers it).
Commit: `phase 0: scaffold`

### Phase 1 — Mock upstream + OpenAI types + SSE (1 day)
Tasks
1. `internal/api`: `ChatRequest`, `ChatResponse`, `ChatChunk`, `Usage`, `Message` (content as string **or** array of parts — custom `UnmarshalJSON`), keep `Extra map[string]json.RawMessage` for pass-through.
2. `internal/sse`: `Reader` (handles `data:` lines, multi-line data, comments, `[DONE]`, CRLF) and `Writer` (writes + flushes). Fuzz test `FuzzReader`.
3. `cmd/mockllm` per §7.
Acceptance
- [ ] Unit tests for JSON round-trip (string content and parts content).
- [ ] `go test -fuzz=FuzzReader -fuzztime=30s ./internal/sse` finds nothing.
- [ ] `curl -N` against mockllm streams tokens with the configured timing.
Commit: `phase 1: api types, sse, mock upstream`

### Phase 2 — Pass-through proxy, non-stream + stream (1–2 days)
Tasks
1. `provider/openai` adapter (non-stream + stream, tuned transport, error classification).
2. `server`: `/v1/chat/completions` with a temporary static auth (single key from env) and a single target.
3. Streaming per §6.2 incl. disconnect propagation.
4. Metrics: `switchyard_requests_total{alias,target,status}`, `switchyard_request_duration_seconds` histogram, `switchyard_ttft_seconds`, `switchyard_gateway_overhead_seconds`.
Acceptance
- [ ] Integration test (gateway + mockllm in-process via `httptest`) for non-stream and stream: body/chunks identical in content to direct call.
- [ ] Test: client cancels mid-stream → upstream connection closed within 100ms.
- [ ] Test: upstream 500 → client gets OpenAI-style 502.
- [ ] Manual: OpenAI python/JS SDK pointed at `http://localhost:8787/v1` works (document in README).
Commit: `phase 2: streaming proxy`

### Phase 3 — First benchmark (½ day) ← early proof
Tasks
1. `bench/k6/nonstream.js`, `bench/k6/stream.js` (scenarios: constant arrival rate 1k/5k/10k rps, 60s each).
2. `bench/run.sh`: starts mockllm with zero latency, runs k6 **directly against mockllm** (baseline) and **through the gateway**, writes JSON to `bench/results/`.
3. `bench/report.go` (or small script) → computes overhead p50/p95/p99 = gateway − baseline and prints a Markdown table.
4. Record in `BENCHMARKS.md`: machine (Apple M4, 10 cores, 24 GB), Go version, command, results table, `GOMAXPROCS`.
Acceptance
- [ ] Results table committed; numbers reproducible (±15%) across two runs.
- [ ] Profile captured once (`go tool pprof` CPU) and top 3 hotspots noted in BENCHMARKS.md.
Commit: `phase 3: baseline benchmarks`

### Phase 4 — Keys, auth, Postgres (1 day)
Tasks
1. goose migrations §5.1; `store` repo with pgx pool.
2. `auth` middleware with LRU+TTL cache; `last_used_at` updated asynchronously (batched every 10s).
3. CLI subcommand: `switchyard keys create --project demo --name local --rpm 600 --tpm 200000 --budget 5` prints key once.
4. Allowed-models check → 403.
Acceptance
- [ ] Tests: valid key, unknown key 401, disabled 401, model not allowed 403, cache hit doesn't touch Postgres (use a counting fake).
- [ ] Re-run benchmark: overhead change ≤ +0.2ms p99 vs phase 3 (record).
Commit: `phase 4: api keys`

### Phase 5 — Router, fallback, breaker (1–2 days)
Tasks
1. `router` (priority, weighted-without-replacement), retries with backoff+jitter, `first_byte_timeout`.
2. `breaker` per §6.5 + metric.
3. Headers `X-Switchyard-Target`, `X-Switchyard-Attempts`.
Acceptance
- [ ] Table tests: primary 503 → fallback succeeds; all fail → 502 with attempts=3; 400 from upstream is **not** retried; fallback never happens after first streamed byte.
- [ ] Breaker tests with a fake clock: opens at the failure-rate threshold, ignores expired buckets, half-opens after duration, closes on success.
- [ ] *Design note (found in phase 5):* a "N consecutive failures" breaker trips healthy-ish targets under concurrency, because fast failures complete before slow successes. Use a failure **rate** over a sliding window instead.
- [ ] Weighted distribution test: 10k picks within ±2% of weights.
- [ ] Chaos demo: mockllm A with `--error-rate 0.3`, B healthy → client success rate ≥ 99.9% over 10k requests (script in `bench/chaos.sh`).
Commit: `phase 5: routing and fallback`

### Phase 6 — Rate limits, budgets, pricing (1 day)
Tasks
1. Lua token bucket (`ratelimit/bucket.lua`) loaded with `SCRIPT LOAD`/`EVALSHA`; returns allowed, remaining, retry_after_ms.
2. RPM + TPM per §6.1 steps 5–6 incl. reconciliation.
3. `pricing` + `budget` (micro-USD integers, `INCRBY`).
4. Fail-open per §6.4 with Redis timeout 50ms.
Acceptance
- [ ] Tests against real Redis (integration tag): 600 rpm → 601st request in the same second gets 429 with sane `Retry-After`; refill works.
- [ ] Concurrency test: 100 goroutines × 100 requests never exceed limit (no race).
- [ ] Budget exceeded → 402; month rollover key format correct.
- [ ] Redis stopped → requests still succeed; dependency metric increments.
Commit: `phase 6: rate limits and budgets`

### Phase 7 — Cache (½ day)
Tasks
1. Canonical request hash: sha256 of normalized JSON (sorted keys; excluding `stream`, `user`, `stream_options`).
2. Store gzip JSON; streaming hit is replayed as SSE chunks (split content into ~20-char chunks, no artificial delay).
Acceptance
- [ ] Same request twice → second has `X-Switchyard-Cache: hit`, zero upstream calls.
- [ ] temperature 0.7 → `bypass`. Oversized response not cached.
- [ ] Benchmark: cache-hit p99 latency recorded.
Commit: `phase 7: response cache`

### Phase 8 — ClickHouse logging + admin API + live tail (2 days)
Tasks
1. Apply ClickHouse migrations at startup (idempotent).
2. `logsink` per §6.6; `live.Hub` per §6.7.
3. Admin endpoints §3 (queries use `requests_1m` for ranges > 2h, raw table otherwise; keyset pagination).
Acceptance
- [ ] Load 1M requests through the gateway with mock (or `bench/seed` that writes synthetic rows) → `/admin/api/overview` for 24h responds < 150ms (record).
- [ ] Logsink test: ClickHouse down → no request latency impact (benchmark delta ≤ noise), drop metric increases when queue full.
- [ ] Live tail: `websocat` shows events; slow consumer test doesn't block publisher.
- [ ] Gateway overhead with logging on still within +0.3ms p99 of phase 4.
Commit: `phase 8: analytics pipeline`

### Phase 9 — Dashboard (2–3 days)
Pages (Next.js, dark theme, Tailwind, Recharts):
1. **Overview**: KPI tiles (requests, error %, cost, tokens, cache hit %, p50/p95/p99 latency, TTFT); charts: requests/min stacked by status, latency percentiles over time, cost by model.
2. **Requests**: filterable table (time range, model, status, key), keyset pagination, row → drawer with attempts and timings.
3. **Live**: WebSocket tail with pause, filters, rate indicator.
4. **Keys**: list, create (show key once + copy), edit limits/budget/models, disable; spend progress bar.
5. **Targets**: breaker state badges, error rate, latency per target.
Admin token entered once, stored in `sessionStorage`; all calls via a typed `lib/api.ts`.
Acceptance
- [ ] `npm run build` and `npm run lint` pass; Vitest tests for `lib/api.ts` and formatters.
- [ ] Every page has empty, loading and error states.
- [ ] Screenshots + 2-minute demo GIF/video saved to `docs/`.
Commit: `phase 9: dashboard`

### Phase 10 — Anthropic & Gemini adapters (1–2 days)
Tasks
1. Translation both ways (system message handling, content parts, stop reasons, usage fields, streaming event types → OpenAI chunks).
2. Golden tests from recorded fixtures in `internal/provider/*/testdata/` (no live calls in CI).
Acceptance
- [ ] Golden tests pass; one manual live test per provider documented (optional, needs keys).
Commit: `phase 10: anthropic and gemini`

### Phase 11 — Observability & hardening (1 day)
1. OpenTelemetry spans: `gateway.request` → `auth`, `ratelimit`, `cache`, `upstream.attempt[n]`; Jaeger in compose profile `tracing`.
2. `Dockerfile` (multi-stage, distroless, non-root, ~20MB); compose profile `full` runs gateway + mock + web.
3. Graceful shutdown test: in-flight streams complete within grace period.
4. `govulncheck` in CI.
Commit: `phase 11: tracing and packaging`

### Phase 12 — Ship it (1 day)
1. README: one-paragraph pitch, architecture diagram (Mermaid), quickstart (`make up && make run`), SDK example, features list, **benchmark table**, design decisions, roadmap.
2. `docs/architecture.md`: request lifecycle sequence diagram, failure policy table, why each tech choice.
3. Final benchmark run → update BENCHMARKS.md and README numbers.
4. Public demo (mock providers only, admin dashboard read-only token) on Fly.io/Railway; link in README.
5. Tag `v0.1.0`; pin repo on GitHub; add to portfolio + resume bullet (numbers from BENCHMARKS.md only).
Acceptance
- [ ] Fresh clone → `make up && make run && make mock` → SDK example works in < 5 minutes following README only.
- [ ] All CI green.

---

## 9. Definition of done (v0.1.0)
- OpenAI SDKs work unchanged against Switchyard (stream + non-stream).
- Fallback + breaker demonstrably keep success ≥ 99.9% with a 30%-failing upstream.
- Published, reproducible gateway overhead numbers (target: p99 < 2ms at 5k rps on Apple M4 with zero-latency mock).
- Rate limits/budgets correct under concurrency; Redis outage doesn't take the gateway down.
- Analytics dashboard with live tail over ≥ 1M logged requests.
- Clean repo: CI green, race-free, documented, runnable in 5 minutes.

## 10. Out of scope (v0.1)
Request coalescing for cache misses (stampede protection — measured in phase 7), semantic/embedding cache, prompt management, multi-tenant SSO for the dashboard, Kubernetes manifests, request/response body storage (privacy), image/audio endpoints. Listed in README "Roadmap".
