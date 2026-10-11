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

## Docker uploads and writer cutover

The same local gate runs a separate native Docker volume drill. A synthetic
non-root file writer is stopped and verified inactive before an uploads archive
is taken. The source then receives additional writes and a changed upload. The
archive is restored into a fresh volume; original files and ownership must match
the earlier snapshot, while the source's later changes remain intact.

The recovered writer starts against the restored volume, proves it can append
as UID 10001, and stops after validation. The original writer remains stopped
throughout this cutover. A corrupt tar archive must fail. Each run retains
`volume-recovery.json` and `application-uploads.tar`, with the archive SHA-256 in
the receipt. Test containers and volumes are removed after validation.

This is a single-writer filesystem cutover fixture. It does not prove coordinated
database/filesystem snapshots, multi-writer consistency, a public HTTP traffic
switch or recovery of an actual customer application. Quiesce and account for
every real writer and dependency before adapting it. Docker mounted-data rollback
remains unsupported in OAP's managed runtime contract; use the native procedure.

See the [owner recovery runbook](../../docs/recovery-runbook.md) before adapting
the procedure to a real application. Real backups need suitable retention,
encryption, consistency, dependency coverage and independently tested restoration.
