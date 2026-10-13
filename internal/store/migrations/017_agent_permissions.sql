-- NULL preserves legacy credentials; new API-issued agents receive explicit pins.
ALTER TABLE oap_credentials ADD COLUMN permissions jsonb CHECK(permissions IS NULL OR jsonb_typeof(permissions)='object');
ALTER TABLE oap_deployments ADD COLUMN authorization_action text NOT NULL DEFAULT '';
