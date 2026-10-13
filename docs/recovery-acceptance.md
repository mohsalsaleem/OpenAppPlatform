# M0 batch 4 supported recovery acceptance

Historical batch review. The [13 October M0 exit review](m0-exit-review.md) and
roadmap priority correction govern current execution; general batch 5 expansion
remains deferred. The packaged P0.5 drill now also verifies a full HTTP/River
process crash and controlled metadata restore, with its stated limits.

Batch 4's supported stateless managed-image release workflow is accepted. This
does not enable OAP-managed stateful provisioning, automatic data restoration,
native source mutation or owner-grade production recovery. Batch 5 follows the
original delivery queue: stable endpoints, domains and replica routing.

| Original batch 4 criterion | Implementation and acceptance |
| --- | --- |
| Configurable readiness | Frozen bounded operator health gates, native HTTP probes, image-health return; PostgreSQL/native Docker and dedicated Coolify staging checks |
| Dependency/start ordering | Every selected dependency replica verifies before dependent dispatch; unselected bindings are inspected; dependency hold/restart system checks |
| Selected affected components | Durable component write claims/dependency read guards; independent releases, conflicting writers and reconstruction system/browser checks |
| Release/artifact comparison | Frozen definitions and masked runtime differences; scope/masking/version tests and hosted review |
| Compatible known-good runtime rollback | Reviewed immutable image release with ownership/configuration checks, idempotency and explicit data compatibility acknowledgement; native Docker and two-replica Coolify rollback/return |
| Cancellation/abandonment/reconciliation | Pre-dispatch cancellation and owner abandonment; exact-operation terminal reconciliation retains guards until resolved; permission, lock, browser and system checks |
| Crash/stale-worker behavior | Durable dispatch intent, competing-worker session locks, lost-response recovery and real child-controller SIGKILL; native process identity remains unchanged without duplicate dispatch |
| Data/schema recovery boundaries | Saved-definition restore is separate from runtime deployment and preserves saved images; system test proves neither image nor definition restore rewinds application schema/data |

The local gate runs race-enabled tests, isolated PostgreSQL and native Docker,
browser/owner-session flows and the MCP smoke check. Relevant recovery slices
passed exact-tree runs `20261011-092848-23fdc9f9` (lost response),
`20261011-115035-131ac1cd` (process kill), `20261011-120541-4d7fda5c`
(data/schema procedure), and `20261011-121626-efe577ba` (volume/writer procedure).
These retained reports are local evidence; tests and examples are committed.
See [native health checks](native-health-checks.md), [release readiness](release-readiness.md),
[image rollback](image-rollback.md), [release controls](release-controls.md) and
the [owner recovery runbook](recovery-runbook.md) for each operation's precise scope.

## Unsupported recovery paths

The native PostgreSQL and volume examples verify separate backups, corrupt archive
rejection, post-backup data loss, retained source state and restored reads/writes.
The volume fixture demonstrates a single synthetic writer cutover. They do not
prove coordinated database/filesystem recovery or a real multi-writer application
cutover. The process-kill test interrupts `Controller.Advance`, not a complete
HTTP/River daemon. Actual cancellation/abandonment of a live Coolify job remains
unverified; its controller/provider contracts are verified locally.

Arbitrarily stale OAP backups must not resume workers without native operation
inspection. Automated stale-backup reconciliation, encrypted backup management
and owner-grade restore belong to M1. Stateful workload provisioning and its
storage guarantees remain gated in batch 6. These limits remain visible rather
than blocking the accepted stateless workflow with a new backup platform.

## Batch 5 handoff

Preserve native Coolify proxy routes and trigger ownership. Prove endpoint identity
survives managed replacement, restart, supported scaling and controller outages.
Per-instance URLs are not evidence of a shared component endpoint. Docker DNS is
not evidence of health-aware load balancing. Shared routing, domain/TLS setup,
stopped-backend removal and draining each need explicit adapter capability and
reproducible acceptance before completion or zero-downtime claims.
