CREATE TABLE IF NOT EXISTS cache_shadow (
  id                String,
  ts                DateTime64(3, 'UTC'),
  tenant_id         LowCardinality(String),
  route             LowCardinality(String),
  threshold         Float32,
  similarity        Float32,
  query             String,
  candidate_query   String,
  candidate_answer  String,
  actual_answer     String,
  candidate_source  String
) ENGINE = MergeTree ORDER BY (route, ts) TTL toDateTime(ts) + INTERVAL 30 DAY
;;
CREATE TABLE IF NOT EXISTS cache_labels (
  shadow_id             String,
  ts                    DateTime64(3, 'UTC'),
  rater                 LowCardinality(String),
  rater_id              String,
  acceptable            UInt8,
  reason                String,
  judge_prompt_version  LowCardinality(String)
) ENGINE = ReplacingMergeTree(ts) ORDER BY (shadow_id, rater) TTL toDateTime(ts) + INTERVAL 90 DAY
;;
CREATE TABLE IF NOT EXISTS routing_shadow (
  id                    String,
  ts                    DateTime64(3, 'UTC'),
  tenant_id             LowCardinality(String),
  route                 LowCardinality(String),
  decision              LowCardinality(String),
  cheap_target          LowCardinality(String),
  strong_target         LowCardinality(String),
  prompt                String,
  cheap_answer          String,
  strong_answer         String,
  cheap_score           Float32,
  strong_score          Float32,
  judge_model           LowCardinality(String),
  judge_prompt_version  LowCardinality(String)
) ENGINE = MergeTree ORDER BY (route, ts) TTL toDateTime(ts) + INTERVAL 30 DAY
;;
CREATE TABLE IF NOT EXISTS routing_decisions (
  ts                     DateTime64(3, 'UTC'),
  request_id             String,
  tenant_id              LowCardinality(String),
  route                  LowCardinality(String),
  decision               LowCardinality(String),
  reason                 LowCardinality(String),
  score                  Float32,
  cost_micros            Int64,
  counterfactual_micros  Int64
) ENGINE = MergeTree PARTITION BY toYYYYMM(ts) ORDER BY (route, ts) TTL toDateTime(ts) + INTERVAL 180 DAY
