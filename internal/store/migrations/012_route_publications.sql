-- Reviewed plans are immutable; intent is committed before native publication.
CREATE TABLE oap_route_publications (
 id text PRIMARY KEY,
 application_id text NOT NULL REFERENCES oap_applications(id),
 owner_id text NOT NULL,
 plan jsonb NOT NULL,
 state text NOT NULL CHECK (state IN ('prepared','publishing','applied','cancelled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX oap_one_uncertain_route ON oap_route_publications(application_id) WHERE state='publishing';
-- A process crash must not allow a new deployment/configuration change to erase
-- the reviewed membership before an owner resumes or safely cancels the intent.
CREATE FUNCTION oap_route_publication_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app text;
BEGIN
 IF TG_TABLE_NAME='oap_applications' THEN app:=NEW.id; ELSE app:=NEW.application_id; END IF;
 PERFORM pg_advisory_xact_lock(hashtext(app));
 IF EXISTS(SELECT 1 FROM oap_route_publications WHERE application_id=app AND state='publishing') THEN
  RAISE EXCEPTION 'Unresolved native route publication' USING ERRCODE='23505';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER oap_route_fence_definition BEFORE UPDATE OF spec ON oap_applications FOR EACH ROW EXECUTE FUNCTION oap_route_publication_fence();
CREATE TRIGGER oap_route_fence_release BEFORE INSERT ON oap_deployments FOR EACH ROW EXECUTE FUNCTION oap_route_publication_fence();
