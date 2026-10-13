-- Separate target onboarding grants from existing app-scoped credentials.
ALTER TABLE oap_credentials ADD COLUMN target_grant boolean NOT NULL DEFAULT false;
ALTER TABLE oap_credentials DROP CONSTRAINT oap_credentials_check;
ALTER TABLE oap_credentials ADD CONSTRAINT oap_credential_scope_boundary CHECK ((
 (kind='session' AND NOT target_grant) OR
 (kind='agent' AND ((NOT target_grant AND application_id IS NOT NULL) OR
 (target_grant AND application_id IS NULL AND permissions IS NOT NULL AND
 permissions ?& ARRAY['createApplications','maxApplications','targetId','environment','actions'] AND
 permissions->>'createApplications'='true' AND
 (permissions->>'maxApplications')::integer BETWEEN 1 AND 10)))
) IS TRUE);
CREATE TABLE oap_agent_applications (
 application_id text PRIMARY KEY REFERENCES oap_applications(id),
 credential_id text NOT NULL REFERENCES oap_credentials(id),
 creation_key text NOT NULL CHECK(length(creation_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL,
 audit_id text UNIQUE REFERENCES oap_audit(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(credential_id,creation_key)
);
CREATE INDEX oap_agent_applications_credential ON oap_agent_applications(credential_id);
