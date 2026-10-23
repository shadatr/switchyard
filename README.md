# Switchyard

A high-performance LLM gateway in Go: one OpenAI-compatible endpoint in front of many model providers, with streaming pass-through, routing & fallback, rate limits & budgets, caching, and real-time cost/latency analytics in ClickHouse.

> 🚧 Under active development — see [PLAN.md](PLAN.md) for the roadmap.

## Quickstart (dev)

```sh
make up      # Redis, Postgres, ClickHouse via Docker
make run     # gateway on :8787
curl localhost:8787/healthz
```

## Use it with any OpenAI SDK

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:8787/v1", api_key="sy-your-key")
client.chat.completions.create(model="mock-fast", messages=[{"role": "user", "content": "hi"}])
```
