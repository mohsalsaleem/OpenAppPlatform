CREATE TABLE oap_target_observations (
 target_id text PRIMARY KEY REFERENCES oap_targets(id),
 target_hash text NOT NULL,
 checked_at timestamptz NOT NULL,
 summary jsonb NOT NULL
);
