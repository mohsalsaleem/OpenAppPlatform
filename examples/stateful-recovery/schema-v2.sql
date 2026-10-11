BEGIN;
ALTER TABLE customers DROP COLUMN email;
ALTER TABLE customers ADD COLUMN display_name text NOT NULL DEFAULT 'Migrated customer';
UPDATE schema_version SET version = 2;
INSERT INTO customers VALUES (2, 'Written after backup');
COMMIT;
