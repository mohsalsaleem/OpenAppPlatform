CREATE TABLE IF NOT EXISTS oap_targets (
 id text PRIMARY KEY, spec jsonb NOT NULL, token_env text NOT NULL
);
CREATE TABLE IF NOT EXISTS oap_applications (
 id text PRIMARY KEY, name text NOT NULL, environment text NOT NULL,
 spec jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(name,environment)
);
CREATE TABLE IF NOT EXISTS oap_deployments (
 id text PRIMARY KEY, application_id text NOT NULL REFERENCES oap_applications(id),
 state text NOT NULL CHECK(state IN ('queued','running','succeeded','failed','attention')),
 spec jsonb NOT NULL, steps jsonb NOT NULL,
 idempotency_key text NOT NULL, request_hash text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(application_id,idempotency_key)
);
CREATE UNIQUE INDEX IF NOT EXISTS oap_one_active_release
 ON oap_deployments(application_id) WHERE state IN ('queued','running');
CREATE TABLE IF NOT EXISTS oap_bindings (
 target_id text NOT NULL REFERENCES oap_targets(id),
 resource_id text NOT NULL,
 application_id text NOT NULL REFERENCES oap_applications(id),
 component text NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal > 0),
 PRIMARY KEY(target_id,resource_id),
 UNIQUE(application_id,component,ordinal)
);
