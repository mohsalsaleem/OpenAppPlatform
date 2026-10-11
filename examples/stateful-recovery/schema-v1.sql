CREATE TABLE schema_version (version integer PRIMARY KEY);
INSERT INTO schema_version VALUES (1);
CREATE TABLE customers (id integer PRIMARY KEY, email text NOT NULL);
INSERT INTO customers VALUES (1, 'before-backup@example.test');
