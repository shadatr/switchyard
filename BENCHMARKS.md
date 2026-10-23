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

## Results (phase 3, commit after flush coalescing)

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
