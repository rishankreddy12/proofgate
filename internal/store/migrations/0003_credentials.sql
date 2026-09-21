CREATE TABLE provider_credentials (
  provider     text NOT NULL,
  version      int NOT NULL,
  kek_id       text NOT NULL,
  wrapped_dek  bytea NOT NULL,
  nonce        bytea NOT NULL,
  ciphertext   bytea NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  created_by   text NOT NULL,
  active       boolean NOT NULL DEFAULT true,
  PRIMARY KEY (provider, version)
);
CREATE UNIQUE INDEX provider_credentials_one_active ON provider_credentials (provider) WHERE active;
