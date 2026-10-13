-- Preserve existing intents and IDs; historical requests are not inferred from paths.
ALTER TABLE oap_audit ADD COLUMN deployment_id text REFERENCES oap_deployments(id);
ALTER TABLE oap_audit ADD COLUMN source_event_id text REFERENCES oap_source_events(id);
CREATE INDEX oap_audit_deployment ON oap_audit(deployment_id) WHERE deployment_id IS NOT NULL;
