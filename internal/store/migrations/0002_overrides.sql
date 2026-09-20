CREATE TABLE route_overrides (
  route       text NOT NULL,
  key         text NOT NULL,
  value       text NOT NULL,
  reason      text NOT NULL,
  actor       text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (route, key)
);

CREATE TABLE proof_events (
  id         bigserial PRIMARY KEY,
  at         timestamptz NOT NULL DEFAULT now(),
  kind       text NOT NULL,     -- override | override_cleared | cache_recommendation | rollback | resume
  route      text NOT NULL,
  detail     jsonb NOT NULL,
  actor      text NOT NULL
);
CREATE INDEX proof_events_route_idx ON proof_events (route, at DESC);
