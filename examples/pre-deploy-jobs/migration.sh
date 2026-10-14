#!/bin/sh
set -eu
: "${DATABASE_URL:?Bind an application database URL through OAP secret references}"
exec psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
CREATE TABLE IF NOT EXISTS oap_example_messages (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  body text NOT NULL
);
COMMIT;
SQL
