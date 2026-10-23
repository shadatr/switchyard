#!/usr/bin/env bash
# Fallback demo: a primary that fails ~30% of requests behind Switchyard.
# Prints the client-observed success rate for direct vs gateway traffic.
set -euo pipefail
cd "$(dirname "$0")/.."
RPS="${RPS:-500}"; DURATION="${DURATION:-20s}"; ERROR_RATE="${ERROR_RATE:-0.3}"
go build -o bin/switchyard ./cmd/switchyard && go build -o bin/mockllm ./cmd/mockllm && go build -o bin/loadgen ./cmd/loadgen
cleanup() { pkill -f "bin/switchyard -config" 2>/dev/null || true; pkill -f "bin/mockllm" 2>/dev/null || true; }
trap cleanup EXIT; cleanup
./bin/mockllm -addr :9797 -ttft 20ms -itl 2ms -tokens 10 -error-rate "$ERROR_RATE" >/dev/null 2>&1 &
./bin/mockllm -addr :9798 -ttft 20ms -itl 2ms -tokens 10 >/dev/null 2>&1 &
./bin/switchyard -config bench/switchyard.chaos.yaml >/dev/null 2>&1 &
sleep 1
rate() { python3 -c "import json,sys;r=json.load(sys.stdin);print(f\"{r['ok']}/{r['sent']} ok = {100*r['ok']/max(r['sent'],1):.3f}%  (p50 {r['latency_ms']['p50']:.1f} ms, p99 {r['latency_ms']['p99']:.1f} ms)\")"; }
echo -n "direct to flaky upstream:    "; ./bin/loadgen -url http://127.0.0.1:9797/v1/chat/completions -model mock-a -rps "$RPS" -duration "$DURATION" -warmup 0s | rate
echo -n "through Switchyard (fallback): "; ./bin/loadgen -url http://127.0.0.1:8787/v1/chat/completions -model chaos -rps "$RPS" -duration "$DURATION" -warmup 0s | rate
echo "breaker / attempts metrics:"
curl -s 127.0.0.1:8787/metrics | grep -E '^switchyard_(requests_total|upstream_attempts_(sum|count)|breaker_state)' | sed 's/^/  /'
