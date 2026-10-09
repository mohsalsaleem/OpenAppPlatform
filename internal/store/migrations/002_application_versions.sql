ALTER TABLE oap_applications ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1;
ALTER TABLE oap_applications ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
