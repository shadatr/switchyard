#!/usr/bin/env bash
# Benchmarks gateway overhead against a zero-latency mock upstream.
# For each rate and mode it runs the load generator directly against mockllm
# (baseline) and through Switchyard, and snapshots the gateway's own
# overhead histogram. Results land in bench/results/<timestamp>/.
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .env ] && { set -a; . ./.env; set +a; }

RATES="${RATES:-1000 5000 7500}"
DURATION="${DURATION:-20s}"
WARMUP="${WARMUP:-3s}"
TOKENS="${TOKENS:-20}"
CONFIG="${CONFIG:-bench/switchyard.bench.yaml}"
KEY="${KEY:-}"            # API key when the config has auth enabled
OUT="bench/results/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUT"

go build -o bin/switchyard ./cmd/switchyard
go build -o bin/mockllm ./cmd/mockllm
go build -o bin/loadgen ./cmd/loadgen
go build -o bin/benchreport ./cmd/benchreport

cleanup() { pkill -f "bin/switchyard -config" 2>/dev/null || true; pkill -f "bin/mockllm" 2>/dev/null || true; }
trap cleanup EXIT
cleanup

./bin/mockllm -ttft 0 -itl 0 -tokens "$TOKENS" >"$OUT/mockllm.log" 2>&1 &
sleep 0.5

start_gateway() {
  pkill -f "bin/switchyard -config" 2>/dev/null || true
  sleep 0.3
  ./bin/switchyard -config "$CONFIG" >"$OUT/switchyard.log" 2>&1 &
  for _ in $(seq 50); do curl -sf 127.0.0.1:8787/healthz >/dev/null && return; sleep 0.1; done
  echo "gateway did not start"; exit 1
}

for mode in nonstream stream; do
  sflag=false; [ "$mode" = stream ] && sflag=true
  for rps in $RATES; do
    echo "== $mode @ ${rps} rps: baseline"
    ./bin/loadgen -url http://127.0.0.1:9797/v1/chat/completions -model mock-a -stream="$sflag" \
      -rps "$rps" -duration "$DURATION" -warmup "$WARMUP" -label "baseline-$mode-$rps" \
      -out "$OUT/baseline-$mode-$rps.json" >/dev/null
    echo "== $mode @ ${rps} rps: gateway"
    start_gateway
    ./bin/loadgen -url http://127.0.0.1:8787/v1/chat/completions -model mock-fast -stream="$sflag" \
      -rps "$rps" -duration "$DURATION" -warmup "$WARMUP" -label "gateway-$mode-$rps" -key "$KEY" \
      -out "$OUT/gateway-$mode-$rps.json" >/dev/null
    curl -s 127.0.0.1:8787/metrics >"$OUT/gateway-$mode-$rps.metrics"
  done
done

./bin/benchreport "$OUT" | tee "$OUT/report.md"
