CREATE TABLE oap_action_plans (
 id text PRIMARY KEY,
 action text NOT NULL CHECK(action IN ('create','configure','deploy','restart','recover')),
 credential_id text NOT NULL REFERENCES oap_credentials(id),
 application_id text REFERENCES oap_applications(id),
 target_id text NOT NULL REFERENCES oap_targets(id),
 environment text NOT NULL,
 definition_version bigint NOT NULL,
 definition_hash text NOT NULL,
 target_hash text NOT NULL,
 input_fingerprint text NOT NULL,
 payload jsonb NOT NULL,
 public_summary jsonb NOT NULL,
 plan_hash text NOT NULL,
 requires_review boolean NOT NULL,
 approved_by text REFERENCES oap_credentials(id),
 approved_at timestamptz,
 result jsonb,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oap_action_plan_application ON oap_action_plans(application_id,created_at DESC);
