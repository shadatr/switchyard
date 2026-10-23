CREATE TABLE IF NOT EXISTS requests (
  ts                  DateTime64(3, 'UTC'),
  request_id          String,
  project_id          LowCardinality(String),
  key_id              LowCardinality(String),
  model_alias         LowCardinality(String),
  target              LowCardinality(String),
  attempts            UInt8,
  status              UInt16,
  error_code          LowCardinality(String),
  stream              Bool,
  cache               LowCardinality(String),
  prompt_tokens       UInt32,
  completion_tokens   UInt32,
  tokens_estimated    Bool,
  cost_usd            Float64,
  latency_ms          UInt32,
  ttft_ms             UInt32,
  upstream_ms         UInt32,
  gateway_overhead_us UInt32
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (project_id, ts, request_id)
TTL toDateTime(ts) + INTERVAL 90 DAY;
