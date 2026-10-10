CREATE TABLE oap_application_groups (
 id text PRIMARY KEY,
 name text NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default')
);
-- Never automatically merge similarly named resources: scopes remain unchanged.
INSERT INTO oap_application_groups(id,name) SELECT id,name FROM oap_applications;
ALTER TABLE oap_applications ADD COLUMN group_id text REFERENCES oap_application_groups(id);
UPDATE oap_applications SET group_id=id;
-- Keep older controllers able to insert environments during a compatible rollback.
CREATE FUNCTION oap_default_application_group() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.group_id IS NULL THEN
  INSERT INTO oap_application_groups(id,name) VALUES(NEW.id,NEW.name);
  NEW.group_id := NEW.id;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER oap_default_application_group BEFORE INSERT ON oap_applications
 FOR EACH ROW EXECUTE FUNCTION oap_default_application_group();
ALTER TABLE oap_applications ALTER COLUMN group_id SET NOT NULL;
CREATE UNIQUE INDEX oap_group_environment ON oap_applications(group_id,environment);
