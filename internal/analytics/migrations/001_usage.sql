CREATE TABLE IF NOT EXISTS usage_events (
  ts                DateTime64(3, 'UTC'),
  request_id        String,
  tenant_id         LowCardinality(String),
  key_id            String,
  route             LowCardinality(String),
  target            LowCardinality(String),
  kind              LowCardinality(String),   -- chat | embeddings | cache_embed
  stream            UInt8,
  status            LowCardinality(String),
  cache             LowCardinality(String),
  attempts          UInt8,
  prompt_tokens     UInt32,
  completion_tokens UInt32,
  cached_tokens     UInt32,
  cost_micros       Int64,
  saved_micros      Int64,
  latency_ms        UInt32,
  ttft_ms           UInt32,
  overhead_us       UInt32,
  usage_estimated   UInt8,
  run_id            String DEFAULT ''
) ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, ts)
TTL toDateTime(ts) + INTERVAL 180 DAY;
