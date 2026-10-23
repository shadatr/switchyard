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
GW_PORT="${GW_PORT:-8877}"     # dedicated ports so a running dev stack is untouched
MOCK_PORT="${MOCK_PORT:-9877}"
OUT="bench/results/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUT"

go build -o bin/switchyard ./cmd/switchyard
go build -o bin/mockllm ./cmd/mockllm
go build -o bin/loadgen ./cmd/loadgen
go build -o bin/benchreport ./cmd/benchreport

# Only processes started by this script are stopped (by PID), never by name.
MOCK_PID=""; GW_PID=""
stop_gateway() { [ -n "$GW_PID" ] && kill "$GW_PID" 2>/dev/null && wait "$GW_PID" 2>/dev/null; GW_PID=""; }
cleanup() { stop_gateway; [ -n "$MOCK_PID" ] && kill "$MOCK_PID" 2>/dev/null; true; }
trap cleanup EXIT

# Point the config's mock provider at our mock port.
RUN_CONFIG="$OUT/config.yaml"
sed "s#127.0.0.1:9797#127.0.0.1:$MOCK_PORT#" "$CONFIG" >"$RUN_CONFIG"

./bin/mockllm -addr ":$MOCK_PORT" -ttft 0 -itl 0 -tokens "$TOKENS" >"$OUT/mockllm.log" 2>&1 &
MOCK_PID=$!
sleep 0.5

start_gateway() {
  stop_gateway
  SWITCHYARD_ADDR=":$GW_PORT" ./bin/switchyard -config "$RUN_CONFIG" >"$OUT/switchyard.log" 2>&1 &
  GW_PID=$!
  for _ in $(seq 50); do curl -sf "127.0.0.1:$GW_PORT/healthz" >/dev/null && return; sleep 0.1; done
  echo "gateway did not start"; exit 1
}

for mode in nonstream stream; do
  sflag=false; [ "$mode" = stream ] && sflag=true
  for rps in $RATES; do
    echo "== $mode @ ${rps} rps: baseline"
    ./bin/loadgen -url "http://127.0.0.1:$MOCK_PORT/v1/chat/completions" -model mock-a -stream="$sflag" \
      -rps "$rps" -duration "$DURATION" -warmup "$WARMUP" -label "baseline-$mode-$rps" \
      -out "$OUT/baseline-$mode-$rps.json" >/dev/null
    echo "== $mode @ ${rps} rps: gateway"
    start_gateway
    ./bin/loadgen -url "http://127.0.0.1:$GW_PORT/v1/chat/completions" -model mock-fast -stream="$sflag" \
      -rps "$rps" -duration "$DURATION" -warmup "$WARMUP" -label "gateway-$mode-$rps" -key "$KEY" \
      -out "$OUT/gateway-$mode-$rps.json" >/dev/null
    curl -s "127.0.0.1:$GW_PORT/metrics" >"$OUT/gateway-$mode-$rps.metrics"
  done
done

./bin/benchreport "$OUT" | tee "$OUT/report.md"
