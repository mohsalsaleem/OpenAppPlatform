CREATE TABLE IF NOT EXISTS oap_deployment_plans (
 id text PRIMARY KEY,
 application_id text NOT NULL REFERENCES oap_applications(id),
 credential_id text NOT NULL REFERENCES oap_credentials(id),
 definition_version bigint NOT NULL,
 definition_hash text NOT NULL,
 components jsonb NOT NULL,
 images jsonb NOT NULL,
 plan_hash text NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '30 minutes',
 approved_by text REFERENCES oap_credentials(id),
 approved_at timestamptz,
 deployment_id text REFERENCES oap_deployments(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS oap_deployment_plans_app_time ON oap_deployment_plans(application_id,created_at DESC);
