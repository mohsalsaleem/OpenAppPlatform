# Release recovery boundaries and owner procedure

OAP records release intent and provider operation identity. Its metadata is not an
application backup. Recovery must distinguish the following state:

| State | Available operation | Boundary |
| --- | --- | --- |
| Uncertain provider dispatch | Attach observation to the exact operation using release recovery | Never dispatch again merely because a response was lost |
| Failed stateless image release | Review a compatible verified image rollback | New release; application data and desired images remain unchanged |
| Saved application settings | Review saved-configuration restoration | Save a new definition version; deployment is a separate action |
| Application database/schema, uploads or volumes | Native operator/database recovery procedure | OAP does not restore or verify this data |
| External secret values | Secret provider recovery procedure | Historical runtime-key metadata is not a secret backup |
| OAP PostgreSQL metadata | Independently verified metadata backup and compatible OAP binary | Bindings and release ledgers must agree with the actual operator |

## Lost responses and controller restarts

1. Inspect the release and native operator. Keep the recorded component and
   dependency guards until the outcome is known. Do not edit the release database.
2. For a recorded pre-dispatch preparation failure, use preparation retry. If
   dispatch might have started, obtain the exact provider operation ID instead.
3. Submit recovery with the current `expectedUpdatedAt`, component and ordinal.
   A recorded operation ID cannot be replaced. Recovery observes the existing
   operation and checks the frozen readiness policy; it does not deploy again.
4. If the provider is still working, OAP resumes observation. If an outcome cannot
   be established, retain attention or use owner abandonment followed by explicit
   reconciliation. Abandonment does not stop native work or release its guards.
5. Inspect runtime health and logs before starting another release. A terminal
   provider operation alone does not prove data/schema compatibility.

See [release controls](release-controls.md) and [image rollback](image-rollback.md)
for API requests and access requirements.

## Application data and schema incidents

Before recovery, record the current and proposed image digests, saved configuration
versions, migration versions, external dependencies, backup identity and acceptable
data-loss window. Confirm compatibility from the application's migration/recovery
instructions. OAP's compatibility acknowledgment records an operator decision; it
does not inspect or certify schemas.

Quiesce all writers through the native application/operator procedure, including
workers, jobs and external clients. Choose a verified backup and restore it into an
isolated destination first. Validate schema, representative data, secrets and
application behavior against the proposed image/configuration. Retain the current
data and metadata until the recovery is accepted. A production cutover or destructive
restore needs separate owner authorization.

Execute the database/storage provider's restore procedure and compatible application
release in the order prescribed by the application. Verify readiness, reads and
writes, then explicitly resume writers. Record the backup, migration state, release
IDs and validation outcome in the incident record. If no compatible restore or
forward-fix procedure is established, keep the incident unresolved; selecting an
older image does not reverse a migration.

## Restoring OAP metadata

Stop OAP controllers before restoring metadata. Native workloads may continue running;
keep access to the native operator independently of OAP. Restore into an isolated
database first and verify migrations, owner access, target references, bindings,
release steps, component claims and control/rollback provenance. Use a compatible
binary; readable metadata alone does not establish downgrade compatibility.

A backup can predate operations that already happened remotely. Do not start workers
against a historical ledger until those operations have been accounted for. Missing
operation IDs or incompatible current bindings require operator inspection and an
explicit reconciliation procedure. Automated reconciliation of an arbitrarily stale
OAP backup is not implemented. Never run two controllers against competing restored
copies or compensate by editing production release rows.

## Reproducible acceptance

`make test-local` runs `TestNativeDockerLostResponseRecoveryDoesNotRestartRuntime`
against the isolated PostgreSQL and Docker fixture environment. The adapter wrapper
loses a response after a real Docker deployment. A reconstructed controller must
retain the release fence, reject preparation retry and stale recovery, attach the
exact operation, and finish with one dispatch and the same process boot identity.
This exercises response uncertainty and controller reconstruction, not an OS-level
kill or restoration of application data.

Existing system tests cover interrupted durable dispatch, worker-lock conflicts,
readiness deadlines, dependency guards and cancellation/abandonment. Existing native
Docker tests cover verified image rollback. Stateful restoration, stale-metadata
restore reconciliation and a process-kill acceptance drill remain outstanding batch
4 work; this runbook and test do not complete that batch.
