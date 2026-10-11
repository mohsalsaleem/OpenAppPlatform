# Application data recovery example

Run `make test-local` from the repository root. The harness creates an isolated,
temporary PostgreSQL server and runs this example with its native `pg_dump` and
`pg_restore` binaries. No production database or credentials are used.

`schema-v1.sql` models the first application contract: customers have an email.
The drill takes a custom-format backup, then `schema-v2.sql` removes that column
and adds a write made after the backup. The earlier query now fails. Deploying an
earlier image would not recreate the missing column or undo the later write.

The drill rejects a corrupt archive, restores the verified archive into another
database, and verifies the earlier schema, backed-up customer and a new write.
It also verifies the migrated source remains intact. The row written after the
backup is absent from the restored copy: this demonstrates a real data-loss
boundary that an owner must accept before cutting over.

Each run saves `stateful-recovery.json` and `application-data.dump` in its
`.local/test-env/runs/<run-id>/` report directory. The databases are removed after
the drill; the report and synthetic-data archive remain for inspection. The
fixture assumes writers are stopped during backup/restore and has no worker,
upload or external-client coordination. There is no production cutover.

The accompanying system test verifies that OAP image rollback and saved-definition
restoration do not touch application rows or reverse its incompatible migration.
The application compatibility acknowledgement is an owner decision; OAP does not
automatically validate a database schema. This example is a native recovery
procedure, not OAP-managed stateful provisioning or automated restore.

See the [owner recovery runbook](../../docs/recovery-runbook.md) before adapting
the procedure to a real application. Real backups need suitable retention,
encryption, consistency, dependency coverage and independently tested restoration.
