-- Keep admission atomic under the existing application transaction lock, but
-- persist workload-level conflicts so unrelated components can progress together.
CREATE TABLE oap_release_claims (
 release_id text NOT NULL REFERENCES oap_deployments(id),
 component text NOT NULL,
 mode text NOT NULL CHECK (mode IN ('read','write')),
 PRIMARY KEY(release_id,component)
);
CREATE INDEX oap_release_claim_component ON oap_release_claims(component,release_id);
-- Existing work conservatively claims the whole frozen application. This preserves
-- its recovery fence even when an older release selected only some components.
INSERT INTO oap_release_claims(release_id,component,mode)
 SELECT d.id,c->>'name','write' FROM oap_deployments d,
 LATERAL jsonb_array_elements(d.spec->'components') c
 WHERE d.state IN ('queued','running','attention');
DROP INDEX oap_one_active_release;
