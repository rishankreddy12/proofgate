CREATE TABLE IF NOT EXISTS mcp_calls (
  ts          DateTime64(3, 'UTC'),
  tenant_id   LowCardinality(String),
  key_id      String,
  server      LowCardinality(String),
  method      LowCardinality(String),
  tool        LowCardinality(String),
  run_id      String,
  decision    LowCardinality(String),
  status      UInt16,
  latency_ms  UInt32
) ENGINE = MergeTree PARTITION BY toYYYYMM(ts) ORDER BY (tenant_id, ts) TTL toDateTime(ts) + INTERVAL 180 DAY;
