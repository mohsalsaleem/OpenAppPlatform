ALTER TABLE oap_deployments ADD COLUMN source jsonb;
CREATE TABLE oap_source_events (
 id text PRIMARY KEY, workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default'),
 hook_id text NOT NULL, delivery_id text NOT NULL, payload_hash text NOT NULL,
 application_id text NOT NULL REFERENCES oap_applications(id), credential_id text NOT NULL REFERENCES oap_credentials(id),
 definition_version bigint NOT NULL, binding jsonb NOT NULL, commit_sha text NOT NULL,
 state text NOT NULL CHECK(state IN ('received','building','built','released','attention')),
 builds jsonb NOT NULL, deployment_id text REFERENCES oap_deployments(id), error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(hook_id,delivery_id)
);
