CREATE TABLE admin_audit (
  id       bigserial PRIMARY KEY,
  ts       timestamptz NOT NULL DEFAULT now(),
  actor    text NOT NULL,
  action   text NOT NULL,
  target   text NOT NULL DEFAULT '',
  detail   jsonb NOT NULL DEFAULT '{}',
  ip       text NOT NULL DEFAULT '',
  result   text NOT NULL DEFAULT 'ok'
);
CREATE INDEX admin_audit_ts_idx ON admin_audit (ts DESC);
CREATE INDEX admin_audit_actor_idx ON admin_audit (actor);
