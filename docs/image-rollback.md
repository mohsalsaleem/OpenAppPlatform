# Controlled image rollback

The initial M0 batch 4 rollback is a new standard image release, not a database,
configuration, topology or schema restore. It supports existing OAP-owned managed
image components on Docker and Coolify. Observed/source-backed/adopted resources
remain outside this path.

First request a read-only plan:

```http
GET /api/v1/applications/{id}/rollback-plan?releaseId={precedingSuccessfulReleaseId}&expectedVersion={version}
```

The prior deployment must have recorded successful/healthy verification for every
selected replica, an immutable registry digest (or a Docker content ID on Docker),
and the same non-image configuration as the current definition: component identities,
replica counts, ports, variables/connections, readiness and dependency policies.
The source selection is retained; unselected components are not redeployed. Current
bindings must match the source IDs, remain active, and belong to this application.
Current operator-reported image configuration must match the latest image release
for that component. Active/attention work, mutable references, incomplete verification,
incompatible configuration and unexpected ownership/configuration are rejected.

The plan hashes definition version, latest release, target authority, credential
reference and inspected instance references. Submit its exact hash and the explicit
application data/schema compatibility acknowledgement:

```http
POST /api/v1/applications/{id}/rollbacks
Idempotency-Key: <unique-operation-key>

{"releaseId":"...","expectedVersion":2,"planHash":"...","acknowledgeDataCompatibility":true}
```

Ordinary application operate access and mutation audit apply. A read credential can
inspect a plan but cannot enqueue rollback. Server revalidation rejects stale plans;
identical request retries return the same release even after execution. Durable
preparation/dispatch/observation, credential rechecks and uncertain-outcome recovery
remain the normal release path. Target authority, bindings and native runtime safety
are checked again during execution/recovery. Rollback provenance is persisted in
step JSON; the underlying operation remains a deployment, with no schema migration.

The UI offers Review rollback from successful image deployment history, displays
exact from/restore references and limits, and requires compatibility acknowledgement
before queuing. It does not infer that an older image is safe for changed application
data. Image availability is not preflight-proven: the operator must fetch the exact
content reference and the new release must pass readiness. Standard rollout can
interrupt requests and may fail if an image is unavailable.

Docker additionally rejects mounted data that its managed runtime contract cannot
reconstruct, verifies host bindings and configured environment values, and can use
already-cached content IDs without a registry. Coolify verifies native ports/mappings
and referenced literal runtime variables; preparation preserves native domains,
storage and unrelated variables. Native operator lifecycle semantics still apply.
A race with external operator changes remains possible; this is not an atomic
transaction across OAP and the runtime.

Desired configuration, source event cursors and historical releases are not rewound.
After rollback, a later ordinary deployment can reapply the current desired images.
Configuration rollback, schema compatibility automation, native source rollback,
stateful reconstruction and cancellation/abandonment remain future work. Older
controllers do not enforce the new rollback preconditions; metadata readability alone
is not execution compatibility.
