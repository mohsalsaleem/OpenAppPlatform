ALTER TABLE oap_bindings ADD COLUMN IF NOT EXISTS retired_at timestamptz;
ALTER TABLE oap_deployments ADD COLUMN IF NOT EXISTS operation text NOT NULL DEFAULT 'deploy'
 CHECK (operation IN ('deploy','restart','scale-down'));
UPDATE oap_deployments SET operation='restart' WHERE steps @> '[{"action":"restart"}]'::jsonb;
