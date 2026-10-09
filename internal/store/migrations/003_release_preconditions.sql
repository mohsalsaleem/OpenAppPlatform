ALTER TABLE oap_deployments ADD COLUMN IF NOT EXISTS definition_version bigint NOT NULL DEFAULT 1;
ALTER TABLE oap_deployments ADD COLUMN IF NOT EXISTS request_hash_version integer NOT NULL DEFAULT 1;
