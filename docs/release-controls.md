# Release cancellation and abandonment

Cancellation closes OAP work only when every recorded instance is pending or prepared, with no provider operation ID. Prepared resources and configuration remain. A reason, the exact `expectedUpdatedAt`, and acknowledgment of retained changes are required by `POST /api/v1/deployments/{id}/control` with `mode: cancel`.

Abandonment closes tracking after dispatch may have started. It never stops a provider, deletes resources or undoes configuration. The application stays fenced against new lifecycle releases. A human owner session must submit `mode: abandon`, acknowledge retained changes and continuing provider work, and later call `POST /api/v1/deployments/{id}/reconcile-abandoned` after inspecting the operator. Scale-down abandonment is unavailable: retirement must use its existing recovery and finalization path.

Reconciliation requires the exact release timestamp and acknowledgment of the current runtime. Recorded provider operation IDs cannot be replaced. For missing IDs, supply `operations` entries with `component`, `ordinal`, and `remoteDeploymentId`. OAP verifies instance ownership and observes every possible operation to a terminal succeeded/failed state before releasing the fence. Unknown or running operations retain the fence. Pending work remains closed; terminal provider work does not imply application health or data compatibility.

Control metadata is persisted in the release step ledger. Cancelled releases use the underlying failed state; unresolved abandoned releases use attention until reconciliation. Public API states remain cancelled/abandoned. No schema migration is required. Do not run older controller binaries alongside this version: they do not understand closure metadata and could resume closed work. Retain backups and reconcile uncertain work before considering a binary downgrade.

## Verification

The exact committed tree passed the reproducible local suite (`20261010-232121-be9f1f7e`): race-enabled unit/system checks, native Docker deployment regressions and browser/owner-session flows. Control checks cover durable cancellation, retained preparation, worker-lock conflicts, app fencing, missing/running provider operations, agent denials and successful human owner abandonment/reconciliation. Browser action tests use intercepted release responses; controller actions are exercised against isolated PostgreSQL with a provider fixture.

Commit `d7b2b0d2e4b7535a35963017e354acd69e38f6ad` was built on the existing server and deployed to Coolify staging. The hosted owner API rejected cancel/abandon on a completed release with 409. The hosted modal passed acknowledgment and Escape checks using browser-only fixture data, with no review writes to stored application/release state. Actual cancellation and abandonment of a running Coolify job were not exercised. The retained signed GitHub source release and both replica digests remained healthy.
