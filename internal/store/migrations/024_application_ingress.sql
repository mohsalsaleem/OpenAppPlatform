-- Route ownership is metadata; no native publication occurs when a spec is saved.
CREATE UNIQUE INDEX oap_application_ingress_domain
 ON oap_applications ((spec->>'targetId'),(spec#>>'{ingress,domain}'))
 WHERE spec->'ingress' IS NOT NULL AND spec->'ingress' <> 'null'::jsonb;
CREATE TABLE oap_ingress_publications (
 deployment_id text PRIMARY KEY REFERENCES oap_deployments(id),
 application_id text NOT NULL REFERENCES oap_applications(id),
 target_id text NOT NULL REFERENCES oap_targets(id),
 authority_hash text NOT NULL,
 expected_hash text NOT NULL,
 expected_etag text NOT NULL,
 candidate_hash text NOT NULL,
 candidate_config jsonb NOT NULL,
 phase text NOT NULL CHECK(phase IN ('prepared','dispatching','applied')),
 dispatched_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE oap_ingress_authority (
 target_id text PRIMARY KEY REFERENCES oap_targets(id),
 application_id text NOT NULL UNIQUE REFERENCES oap_applications(id),
 authority_hash text NOT NULL,
 config_hash text NOT NULL
);

ALTER TABLE oap_action_plans DROP CONSTRAINT oap_action_plans_action_check;
ALTER TABLE oap_action_plans ADD CHECK(action IN ('create','configure','deploy','restart','recover','scale-down'));
