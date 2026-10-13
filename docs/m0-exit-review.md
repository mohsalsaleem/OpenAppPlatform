# Supported M0 Exit Review

Reviewed 13 October 2026 against the breadth-first priority correction in
[the roadmap](../ROADMAP.md). The supported single-server core is accepted.
This is not acceptance of every original routing feature or production readiness.

## Evidence and Boundaries

| Exit criterion | Delivered evidence | Supported boundary |
| --- | --- | --- |
| Create or assemble an application | Guided discovery/grouping, versioned environment organization, and the frontend/API/worker/existing PostgreSQL example | Existing resources retain native ownership; no implicit adoption or database provisioning |
| Configure components | Shared shadcn controls; runtime inputs, connections, native probes, CPU/memory limits and retained named mounts | Linked/adopted configuration stays in the operator; writable local mounts require one instance |
| GitHub release event and exact artifact | Signed intake/deduplication, exact commit/source observation and optional immutable image-builder lane, with local and dedicated staging acceptance | Native Coolify GitHub App triggers/builds remain native; source management handoff is unsupported |
| Deploy and reach supported endpoints | Real Docker image execution; Coolify managed-image staging; Docker app-local DNS and fixed single-instance host ports; existing native HTTP(S) endpoints and a limited shared-route pilot | General domain/TLS lifecycle, bare-Docker shared routing and draining remain deferred; standard replacement can cause downtime |
| Current health and logs | Live target-scoped bindings, bounded per-instance logs, secret masking, worker image health and database dependency health | No distributed tracing, indexed collection, analytics or continuous AI monitoring is implied |
| Restart and supported scaling | Durable per-instance restart, supported scale-up/down, retained bindings and native fixtures | Observed databases/source resources do not receive lifecycle authority; no autoscaling or scheduled jobs |
| Failed update/recovery through UI/API | Selected releases, readiness/dependency holds, image/configuration review, compatible image rollback, cancellation/abandonment and exact-operation observation recovery | Rollback does not restore application data; unknown provider outcomes remain held rather than redispatched |
| Reproducible installation and external recovery | Owner-mode Compose package, non-root container-visible socket access, process-crash recovery, compatible image replacement, quiesced metadata backup and isolated restore with owner login | One Go service/UI plus PostgreSQL; no automated stale-backup reconciliation, SQL downgrade or workload-data restore |

P0.1–P0.5 are delivered within those limits. The four-component staging application
is healthy, and linked database configuration/data remain unchanged. Existing
operator builds, triggers, domains, registry login and persistence retain authority.

## Latest Acceptance Checkpoints

- Registry slice: `3615050`; full local gate `20261012-222322-3b711304`.
- Installation slice and running staging code: `73e288c`; full local gate
  `20261013-003113-a84adfa3`.
- That installation gate includes the shipping Dockerfile/Compose recipe, five
  packaged owner browser phases, 19 regular browser tests, the separate owner
  flow, PostgreSQL/native Docker/system/race checks and MCP smoke.
- The packaged drill preserved native workload IDs through controller crash,
  compatible image replacement/rollback and a controlled metadata-copy cutover.
  It rejected a corrupt archive and verified restored owner access.
- Staging candidate schema preflight passed read-only; deployment
  `vwkiffbbxfcqvqilfmgvsfn8` reached a healthy non-root runtime with no errors in
  its bounded startup log check.
- Scoped worker restart `b36cfc6446df3ded054847097379fccf` completed. Coolify health
  briefly lagged completion; read-only follow-up settled at 4/4 healthy, with
  worker logs/connectivity and unchanged linked database configuration/data.

These are retained acceptance checkpoints, not an uptime guarantee. Detailed
synthetic reports, archives and screenshots are local ignored artifacts; reproducible
tests and examples are committed. Fresh packaged installation/restore was exercised
locally; staging acceptance used the existing dedicated installation. No production
resource or arbitrary historical backup was activated.

See [GitHub release evidence](github-releases.md), [application environments](application-environments.md),
[supported release recovery](recovery-acceptance.md), [routing pilot](connected-preview-acceptance.md),
[runtime references](runtime-secret-references.md), [private pulls](private-registry-access.md),
[persistence](persistent-volumes.md) and [installation](installation.md).

## Remaining Limits Are Explicit

Original batch 5 is a limited pilot, not a completed routing platform. Its pending
general routing/domain/draining and selectable rolling/blue-green features remain
parked under the accepted breadth-first correction. Docker fixed ports and native
Coolify endpoints are the supported paths for the current baseline.

Owner-grade operation is also not complete: credentials currently use application
read/operate scopes, audit records lack complete asynchronous operation correlation,
backups do not provide managed retention/encryption/status, and there is no encrypted
local secret store or general workload security policy. A metadata backup alone
does not include external keys/credentials or restore application data.

## Next Ordered Work

Move to M1 breadth, starting with **M1.1 Auth/Scopes and Mutation Audit**. Preserve
current owner sessions and credential/release IDs. Extend explicit action/target/
environment boundaries and link mutation intent to durable operations/outcomes,
with redaction and queued-operation authorization tested. Do not add an identity
service or AI execution layer.

Subsequent slices address recovery classification, metadata-backup visibility,
recoverable encrypted secrets, baseline container/image policy, and deterministic
visibility plus notification event foundations. Complete one stated gate at a time;
remote tunnels/agent approval systems, new operators, AI assembly, analytics and
multi-server/runtime research remain in their later phases.
