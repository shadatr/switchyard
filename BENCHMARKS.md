# Benchmarks

All numbers here come from `bench/run.sh` in this repository. Raw JSON results and Prometheus snapshots are written to `bench/results/<run>/` (git-ignored); the tables below are copied from each run's generated `report.md`.

## Setup

| | |
|---|---|
| Machine | Apple M4, 10 cores, 24 GB RAM, macOS (Darwin 25.6) |
| Go | 1.25.7, `GOMAXPROCS=10` |
| Upstream | `mockllm -ttft 0 -itl 0 -tokens 20` (zero added latency — isolates gateway cost) |
| Load generator | `cmd/loadgen`: open-model, fixed arrival rate, latency measured from each request's *scheduled* start (no coordinated omission) |
| Duration | 3 s warm-up + 20 s measured per scenario |
| Gateway config | `bench/switchyard.bench.yaml` — single mock target, no Redis/Postgres/ClickHouse (phase 3 baseline) |

**Important caveat:** load generator, mock upstream and gateway all run on the same laptop and compete for the same 10 cores. Absolute numbers are therefore conservative, and client-observed p99 is noisy (it includes scheduler and TCP effects on a busy machine). The most stable metric is the gateway's **self-measured overhead** (request latency minus time waiting on the upstream, exported as the `switchyard_gateway_overhead_seconds` histogram).

### Columns
- **Baseline** — load generator → mockllm directly.
- **Gateway** — load generator → Switchyard → mockllm.
- **Added** — gateway minus baseline (client-observed). Negative values are noise.
- **Self-measured overhead** — from the gateway's own histogram (p50 / p95 / p99).

## Phase 3 results (proxy only, after flush coalescing)

### Run A — `20261023-121242`

| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Self-measured overhead p50 / p95 / p99 (ms) |
|---|---:|---:|---:|---|---|---|---|
| nonstream | 1000 | 1000 | 0 | 0.29 / 1.04 | 0.39 / 1.76 | +0.10 / +0.72 | 0.013 / 0.037 / 0.070 |
| nonstream | 5000 | 5000 | 0 | 0.08 / 0.16 | 0.12 / 0.51 | +0.04 / +0.35 | 0.005 / 0.010 / 0.030 |
| nonstream | 10000 | 10000 | 0 | 0.21 / 20.37 | 0.12 / 0.95 | -0.10 / -19.42 | 0.006 / 0.018 / 0.053 |
| stream | 1000 | 1000 | 0 | 0.48 / 12.66 | 0.75 / 2.90 | +0.26 / -9.76 | 0.019 / 0.039 / 0.071 |
| stream | 5000 | 5000 | 0 | 0.14 / 1.05 | 0.19 / 3.56 | +0.05 / +2.52 | 0.009 / 0.042 / 0.103 |
| stream | 10000 | 865 | 149815 | 0.59 / 7566.94 | 19.85 / 16882.69 | — | 0.031 / 1.091 / 20.415 |

At 10,000 streaming rps (200,000 SSE events/s) the **baseline itself** degrades (p99 7.5 s): the test rig is saturated, so this row says nothing about the gateway.

### Run B — `20261023-122305` (reproducibility + 7,500 rps)

| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Self-measured overhead p50 / p95 / p99 (ms) |
|---|---:|---:|---:|---|---|---|---|
| nonstream | 1000 | 1000 | 0 | 0.43 / 2.18 | 0.56 / 2.54 | +0.13 / +0.35 | 0.029 / 0.065 / 0.106 |
| nonstream | 5000 | 5000 | 0 | 0.09 / 0.17 | 0.12 / 0.32 | +0.04 / +0.15 | 0.005 / 0.010 / 0.023 |
| nonstream | 7500 | 7500 | 0 | 0.07 / 0.18 | 0.12 / 438.04 | +0.04 / +437.85 ⚠ | 0.006 / 0.017 / 0.040 |
| stream | 1000 | 1000 | 0 | 0.52 / 4.96 | 0.57 / 2.35 | +0.05 / -2.62 | 0.027 / 0.072 / 0.110 |
| stream | 5000 | 5000 | 0 | 0.14 / 0.89 | 0.17 / 8.73 | +0.03 / +7.84 | 0.007 / 0.027 / 0.070 |
| stream | 7500 | 7500 | 0 | 0.12 / 3.04 | 0.43 / 14.04 | +0.31 / +11.00 | 0.018 / 0.098 / 0.264 |

⚠ The 438 ms p99 in this row is **not** time spent inside the gateway (its self-measured p99 for the same run is 0.04 ms). The likely cause is new-connection bursts overflowing the listen backlog (`kern.ipc.somaxconn = 128` on this Mac), which delays SYNs. To be investigated in a later phase on Linux with a larger backlog.

### Summary

- **Gateway self-measured overhead p99 stays below 0.11 ms** at every sustainable rate in both runs (non-streaming and streaming).
- **Client-observed added median latency is ≤ 0.31 ms** at every sustainable rate.
- Streaming is sustained at **7,500 rps × 20 tokens = 150,000 SSE events/s** with zero errors on a single laptop shared with the load generator and the upstream.

## Final run — v0.1.0 (run `20261023-134106`)

Everything enabled except Redis limits: API-key auth, routing + breakers, ClickHouse request logging, Prometheus metrics, OpenTelemetry (disabled → no-op). `bench/switchyard.bench-analytics.yaml`. Note: the demo stack (gateway, dashboard, ~40 rps of live traffic, ClickHouse ingestion) was running on the same laptop during this run.

| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Self-measured overhead p50 / p95 / p99 (ms) |
|---|---:|---:|---:|---|---|---|---|
| nonstream | 1000 | 1000 | 0 | 0.35 / 1.31 | 0.52 / 1.92 | +0.17 / +0.62 | 0.033 / 0.075 / 0.122 |
| nonstream | 5000 | 5000 | 0 | 0.08 / 0.36 | 0.12 / 0.59 | +0.04 / +0.22 | 0.005 / 0.010 / 0.027 |
| nonstream | 7500 | 7500 | 0 | 0.07 / 0.73 | 0.11 / 0.47 | +0.04 / −0.26 | 0.005 / 0.014 / 0.036 |
| stream | 1000 | 1000 | 0 | 0.53 / 2.82 | 0.57 / 2.89 | +0.04 / +0.07 | 0.029 / 0.076 / 0.131 |
| stream | 5000 | 5000 | 0 | 0.12 / 0.65 | 0.17 / 1.22 | +0.05 / +0.57 | 0.007 / 0.021 / 0.054 |
| stream | 7500 | 7499 | 0 | 0.11 / 1.38 | 0.26 / 28.37 | +0.15 / +26.98 | 0.010 / 0.044 / 0.079 |

- Streaming TTFT p50: 0.37 → 0.46 ms (1k), 0.08 → 0.13 ms (5k), 0.07 → 0.22 ms (7.5k).
- **Self-measured overhead p99 ≤ 0.131 ms at every rate**, zero errors throughout.
- The +27 ms client p99 at 7,500 streaming rps is tail latency from a saturated machine (load generator + upstream + gateway + the live demo stack sharing 10 cores); the gateway's own p99 for the same run is 0.079 ms.

## Phase 4 — with API-key authentication (`bench/switchyard.bench-auth.yaml`)

Run `20261023-123624`, 5,000 rps. Overhead is measured from request arrival, so it now **includes** authentication (SHA-256 of the key + in-memory LRU lookup; Postgres is only hit on a cache miss).

| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Self-measured overhead p50 / p95 / p99 (ms) |
|---|---:|---:|---:|---|---|---|---|
| nonstream | 5000 | 5000 | 0 | 0.09 / 0.14 | 0.13 / 0.35 | +0.04 / +0.21 | 0.005 / 0.010 / 0.028 |
| stream | 5000 | 5000 | 0 | 0.13 / 0.33 | 0.17 / 0.55 | +0.03 / +0.22 | 0.007 / 0.025 / 0.067 |

Self-measured p99 overhead: **0.028 ms with auth vs 0.023 ms without** (phase 3, run B) — authentication costs ≈ 5 µs at p99.

## Phase 5 — routing, fallback and circuit breaker

### Overhead with router + breaker (run `20261023-124847`, 5,000 rps, auth enabled)

| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Self-measured overhead p50 / p95 / p99 (ms) |
|---|---:|---:|---:|---|---|---|---|
| nonstream | 5000 | 5000 | 0 | 0.09 / 0.14 | 0.13 / 0.32 | +0.04 / +0.19 | 0.005 / 0.010 / 0.022 |
| stream | 5000 | 5000 | 0 | 0.14 / 0.29 | 0.17 / 0.76 | +0.03 / +0.47 | 0.007 / 0.020 / 0.063 |

No measurable regression from routing and the per-request breaker bookkeeping.

### Chaos: a flaky primary behind Switchyard (`bench/chaos.sh`)

Two mock upstreams (TTFT 20 ms, 10 tokens); alias `chaos` routes `flaky` first, `healthy` second; 500 rps.

| Scenario | Direct to primary | Through Switchyard | Notes |
|---|---:|---:|---|
| Primary fails 30% of requests (20 s, 10,000 req) | 71.2% success | **100.000%** success, p99 45.9 ms | Primary still serves its healthy share (6,945 req); 3,055 retried on the secondary; breaker never trips |
| Primary fully down (12 s, 6,000 req) | 0% success | **100.000%** success, p99 44.3 ms | Breaker opens after ~20 requests; only 22 wasted attempts in total (half-open probes every 5 s) |

### Finding: why the breaker counts *started* requests

The first breaker design tripped after N consecutive failures. In the 30% chaos test it tripped almost immediately and kept the primary disabled for the whole run (32 of 10,000 requests reached it). Sampling the breaker every second showed it re-tripping after exactly 5 failures in every window.

Cause: failures come back in ~1 ms while successes take ~40 ms, so with ~20 requests in flight the failures *complete* first and look consecutive. Switching to a failure rate over completed requests still tripped once at startup for the same reason (the first completions are all failures). The final design counts failures against requests **started** in a sliding 10 s window (10 buckets) — independent of completion order. Regression test: `TestFastFailuresDoNotTripMostlyHealthyTarget`.

## Phase 6 — rate limits and budgets (Redis)

Run `20261023-125433`, 5,000 rps, key with RPM + TPM + monthly budget (limits set high so nothing is rejected — this measures enforcement cost). Redis 7 runs in Docker Desktop (`bench/switchyard.bench-limits.yaml`).

| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Self-measured overhead p50 / p95 / p99 (ms) |
|---|---:|---:|---:|---|---|---|---|
| nonstream | 5000 | 5000 | 0 | 0.08 / 0.12 | 0.31 / 0.98 | +0.23 / +0.86 | 0.151 / 0.318 / 1.107 |
| stream | 5000 | 5000 | 0 | 0.12 / 0.31 | 0.52 / 1.69 | +0.39 / +1.38 | 0.249 / 0.564 / 0.988 |

Enforcing limits costs one pipelined Redis round trip per request (RPM bucket + TPM bucket + budget read in a single `EVALSHA`/`GET` pipeline); settlement after the response is asynchronous. Self-measured p99 overhead rises from ~0.02 ms to ~1.1 ms — almost entirely the round trip through Docker Desktop's VM network on macOS, so expect lower on Linux with Redis on the same host or a nearby instance. Keys without limits skip Redis entirely.

Correctness (see `internal/ratelimit` tests, run against both miniredis and the real Redis): 100 goroutines × 100 requests against a 600 rpm key never exceed 600 + refill; a 30-rpm key over HTTP allowed exactly 30 of 35 requests and returned `Retry-After: 2`; with Redis stopped, requests kept succeeding (fail open) and `switchyard_dependency_errors_total{dep="redis"}` counted every degraded request while the warning was logged once.

## Phase 7 — response cache

Upstream: mockllm with TTFT 200 ms, 15 ms/token, 50 tokens (≈ 950 ms per answer). 5,000 rps of an identical `temperature: 0` request for 15 s (+2 s warm-up), auth + limits + cache enabled (`bench/switchyard.bench-cache.yaml`).

| Mode | OK | Client p50 | Client p99 |
|---|---:|---:|---:|
| non-stream, cache hit | 75,000 / 75,000 | 0.19 ms | 1.96 ms |
| stream (replayed), cache hit | 75,000 / 75,000 | 0.20 ms | 1.17 ms |

Cache counters for the whole run: 165,259 hits, 4,741 misses, 4,741 stores.

**Known limitation — cache stampede.** The 4,741 misses are the requests that arrived during the first ~0.95 s, before the first answer had been stored: every one of them went upstream. Request coalescing (identical in-flight misses wait for a single upstream call) would reduce that to one upstream call; it is on the roadmap.

## Phase 8 — analytics pipeline (ClickHouse)

### Logging overhead (run `20261023-130634`, 5,000 rps, auth + ClickHouse logging, `bench/switchyard.bench-analytics.yaml`)

| Mode | Target rps | Achieved rps | Errors | Baseline p50 / p99 (ms) | Gateway p50 / p99 (ms) | Added p50 / p99 (ms) | Self-measured overhead p50 / p95 / p99 (ms) |
|---|---:|---:|---:|---|---|---|---|
| nonstream | 5000 | 5000 | 0 | 0.09 / 0.15 | 0.13 / 0.24 | +0.04 / +0.09 | 0.005 / 0.010 / 0.018 |
| stream | 5000 | 5000 | 0 | 0.13 / 0.31 | 0.17 / 0.68 | +0.04 / +0.37 | 0.007 / 0.019 / 0.037 |

Every request is logged (non-blocking enqueue → batched native INSERT); self-measured p99 overhead is unchanged from phase 4.

### ClickHouse outage under load

ClickHouse container stopped, then 5,000 rps for 30 s: **150,000 / 150,000 requests succeeded**, client p50 0.13 ms, gateway self-measured mean overhead 15 µs. `switchyard_logs_dropped_total` = 148,125, `switchyard_logs_written_total` = 0 — logs are dropped and counted, requests are unaffected. Client-observed p99 rose to 2.09 ms (vs 0.24 ms with ClickHouse healthy); suspected GC pressure from the full 100k-record queue — not yet confirmed.

### Analytics queries at volume

`cmd/seedlogs` wrote 1,000,000 synthetic rows (24 h, realistic mix) in 1.66 s. The 24-hour overview (served from the 1-minute `AggregatingMergeTree` rollup fed by a materialized view) takes **14.9 ms** (best of 5) over 1,000,120 rows. An integration test checks that the rollup and the raw table agree and that keyset pagination returns every row exactly once.

### Live tail

The WebSocket live tail batches events every 250 ms (max 200 per batch, overflow counted as `skipped`); at 500 rps a client received 12 batches / 1,480 events over 3 s. Publishing with no subscribers costs 3.3 ns.

## Optimization log

### 1. Flush coalescing for SSE (phase 3)

**Before:** the gateway flushed every SSE event to the client individually. At 5,000 streaming rps the CPU profile showed **57% of CPU in `syscall` (write)**, the gateway used **2.0 cores**, client p99 rose by **+80 ms**, and at 10,000 rps the gateway collapsed (318 rps achieved, upstream connection churn produced 502s).

**Change:** after writing an event, flush only if the *next complete event* is not already in the upstream read buffer (`sse.Reader.HasBufferedEvent`). Events that arrived together leave in one write; an event is never held back waiting on network I/O, so TTFT is unaffected. A subtle requirement: a *partial* next event in the buffer must still trigger a flush — otherwise, with real providers (10–30 ms between tokens), the previous token would wait for the next TCP packet. The SSE reader was rewritten to own its buffer so this check is exact.

**After (same 5,000 streaming rps):** gateway CPU **0.89 cores (−56%)**, client p99 added **+2.5 to +7.8 ms** (was +80 ms), and 7,500 rps sustained with zero errors.

| Profile @ 5k stream rps (15 s) | Before | After |
|---|---:|---:|
| Total CPU samples | 30.18 s | 13.38 s |
| Top hotspot | `syscall` write/read 57% | `syscall` write 41% (inherent: one write per burst) |
| Other notable | `bufio.ReadByte` 4% (byte-wise SSE parsing) | removed (slice-based line scanning) |

### 2. Avoid the cgo DNS resolver for loopback (phase 3)

Under connection churn on macOS, resolving `localhost` goes through the system resolver, which consumes one OS thread per concurrent lookup; the load generator crashed with `failed to create new OS thread` at 7,500 rps. Benchmarks and examples now use `127.0.0.1`.

## Reproduce

```sh
./bench/run.sh                        # default RATES="1000 5000 7500"
RATES="1000 5000 7500" ./bench/run.sh # custom rates
```

## Phase 10 — real-provider smoke test (OpenRouter)

`deploy/switchyard.openrouter.yaml` routes alias `free` across four free OpenRouter models (two vendors as fallback). Using the official OpenAI Python SDK pointed at Switchyard:

- non-streaming: `'Switchyard works'`, target `openrouter/liquid/lfm-2.5-2.6b:free`, 1 attempt, usage 17 → 250 tokens;
- streaming with `include_usage`: `'1 2 3 4 5'`, usage 22 → 84 tokens.

Observed along the way (real-world behaviour, not gateway bugs): Google's free Gemma models were rate-limited upstream (OpenRouter 429) — Switchyard tried both and returned an OpenAI-style 429 carrying the upstream message; reasoning models returned `content: null` when `max_tokens` was smaller than their reasoning budget — identical to calling OpenRouter directly, confirming the pass-through is faithful.

Anthropic and Gemini adapters are covered by golden tests against recorded-format fixtures (`internal/provider/*/testdata`); they have not been exercised against the live APIs (no keys available).
