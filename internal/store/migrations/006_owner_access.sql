CREATE TABLE oap_workspaces(id text PRIMARY KEY CHECK(id='default'), name text NOT NULL);
INSERT INTO oap_workspaces VALUES('default','My workspace');
ALTER TABLE oap_targets ADD COLUMN workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default');
ALTER TABLE oap_applications ADD COLUMN workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default');
ALTER TABLE oap_bindings ADD COLUMN workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default');
ALTER TABLE oap_deployments ADD COLUMN workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default');
CREATE TABLE oap_users (
 id text PRIMARY KEY, workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default'),
 email text NOT NULL UNIQUE, name text NOT NULL, password_hash text NOT NULL,
 role text NOT NULL CHECK(role IN ('owner','operator','viewer')), active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX oap_single_owner ON oap_users(role) WHERE role='owner';
CREATE TABLE oap_credentials (
 id text PRIMARY KEY, workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default'),
 user_id text NOT NULL REFERENCES oap_users(id), token_hash text NOT NULL UNIQUE,
 kind text NOT NULL CHECK(kind IN ('session','agent')), name text NOT NULL,
 scope text NOT NULL CHECK(scope IN ('read','operate')), application_id text REFERENCES oap_applications(id),
 expires_at timestamptz NOT NULL, revoked_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(kind!='agent' OR application_id IS NOT NULL)
);
ALTER TABLE oap_deployments ADD COLUMN credential_id text REFERENCES oap_credentials(id);
CREATE TABLE oap_invitations (
 id text PRIMARY KEY, workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default'),
 email text NOT NULL, role text NOT NULL CHECK(role IN ('operator','viewer')),
 token_hash text NOT NULL UNIQUE, expires_at timestamptz NOT NULL, accepted_at timestamptz
);
CREATE TABLE oap_audit (
 id text PRIMARY KEY, workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default'),
 user_id text, credential_id text, action text NOT NULL, status integer,
 created_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz
);
CREATE INDEX oap_audit_recent ON oap_audit(created_at DESC);
CREATE TABLE oap_auth_attempts (key text PRIMARY KEY, count integer NOT NULL, until_at timestamptz NOT NULL);
