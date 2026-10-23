CREATE MATERIALIZED VIEW IF NOT EXISTS requests_1m_mv TO requests_1m AS
SELECT
  toStartOfMinute(ts)                                         AS minute,
  project_id,
  model_alias,
  target,
  toUInt64(count())                                           AS requests,
  toUInt64(countIf(status >= 400))                            AS errors,
  toUInt64(countIf(cache = 'hit'))                            AS cache_hits,
  toUInt64(sum(prompt_tokens))                                AS prompt_tokens,
  toUInt64(sum(completion_tokens))                            AS completion_tokens,
  sum(cost_usd)                                               AS cost_usd,
  quantilesState(0.5, 0.95, 0.99)(latency_ms)                 AS latency_q,
  quantilesStateIf(0.5, 0.95, 0.99)(ttft_ms, status = 200)    AS ttft_q
FROM requests
GROUP BY minute, project_id, model_alias, target;
