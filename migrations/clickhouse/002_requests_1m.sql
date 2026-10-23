CREATE TABLE IF NOT EXISTS requests_1m (
  minute            DateTime('UTC'),
  project_id        LowCardinality(String),
  model_alias       LowCardinality(String),
  target            LowCardinality(String),
  requests          SimpleAggregateFunction(sum, UInt64),
  errors            SimpleAggregateFunction(sum, UInt64),
  cache_hits        SimpleAggregateFunction(sum, UInt64),
  prompt_tokens     SimpleAggregateFunction(sum, UInt64),
  completion_tokens SimpleAggregateFunction(sum, UInt64),
  cost_usd          SimpleAggregateFunction(sum, Float64),
  latency_q         AggregateFunction(quantiles(0.5, 0.95, 0.99), UInt32),
  ttft_q            AggregateFunction(quantiles(0.5, 0.95, 0.99), UInt32)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(minute)
ORDER BY (project_id, minute, model_alias, target)
TTL minute + INTERVAL 400 DAY;
