# Release readiness

M0 batch 4 starts with a configurable completion gate over existing operator health.
This is controller policy, not a new HTTP probe or health-check provisioning API.
Both Docker and Coolify continue to execute their native lifecycle and health checks.

```json
{
  "name": "web",
  "kind": "web",
  "image": "nginx:1.28-alpine",
  "port": 80,
  "instances": 1,
  "strategy": "standard",
  "readiness": { "requireHealthy": true, "timeoutSeconds": 120 }
}
```

Omit readiness to preserve existing behavior: a completed provider deployment plus
`running` or `running:healthy` can finish the step, within 900 seconds. Unhealthy,
unknown and starting statuses never pass. `requireHealthy` accepts only explicit
`running:healthy` status (including its existing provider suffix format). A running
service with no native health result cannot pass this stricter gate. Configure its
native health check in the operator before selecting this requirement.

`timeoutSeconds` is zero/omitted for 900 seconds, or an integer from 30 to 1800.
The observation clock starts when a dispatch acknowledgement is persisted; it
includes waiting for provider completion and health. Timeout puts the release in
attention. It does not stop, undo or redispatch the provider operation. A process
restart retains the persisted observation clock. Legacy steps without a start time
retain the creation-time fallback.

The immutable release manifest holds its policy. Editing the current definition
changes future releases; it cannot alter a queued/waiting release. Owner-reviewed
recovery observes the recorded exact provider operation, applies the same frozen
health requirement, and refreshes the observation clock when observation resumes.
Completed-but-not-ready operations remain held for review; recovery does not bypass
health or create another deployment. Instance restart releases also honor this gate.
Retirement verifies stopped state through its separate contract.

Observed components cannot set readiness overrides. This does not change native
source workflow ownership. A handed-off managed image service may use controller
readiness policy without rewriting its native runtime configuration. Docker excludes
this policy from its container revision hash, so policy-only edits do not replace
containers during preparation. A separately requested deployment still executes the
normal lifecycle.

Settings offers the healthy requirement and timeout, with a diff before saving.
Deployment review and activity show the saved/frozen custom policy. Live instance
status is still the current operator state, separate from release acceptance.

No migration is required: the additive optional field lives in existing JSON
manifests. Unit tests cover defaults/bounds/statuses and Docker runtime identity;
system tests cover frozen policy through definition changes, controller restart,
timeout and exact-operation recovery without redispatch. Browser tests exercise
settings and the readable diff. HTTP probe configuration, dependency ordering,
release comparison, rollback and cancellation remain later slices of batch 4.

## Staging verification and compatibility

Deployed `831c59924ba0c99443d82ba57fd5674afb2abb15` to existing staging through
Coolify deployment `bwntrrgovn5pkv4gfqacuq6d`; runtime and health endpoint passed.
Local exact-tree run `20261010-213725-f1911a18` passed unit, PostgreSQL system,
native Docker lifecycle/health and browser tests. The live owner UI verified legacy
defaults, editing/diff, reload and sign-out without saving application changes. The
retained source release and both replicas remain healthy with their original digest.
Strict gate/timeout/recovery behavior was tested locally; no new hosted workload
release or native health-check change was used for this verification.

Database schema remains at migration 010; the tested staging backup and previous
healthy image remain retained. Older controller binaries do not enforce the new
policy. Metadata readability alone is not semantic rollback compatibility: do not
use a pre-readiness controller to process releases that require the new gate without
an explicit compatibility review. Runtime release rollback remains future batch 4
work and will not restore application data.

The same observation deadline also bounds retirement waiting; retirement continues
to use stopped-state verification rather than the healthy requirement.

## Explicit startup dependencies

Managed components may declare `"dependsOn": ["api"]`. References must be unique,
within the same application, at most 15, and cannot include the component itself.
Cycles are rejected when creating/updating definitions. Components without edges
retain definition order. Dependencies are explicit; service environment mappings
do not infer startup edges. Observed components cannot override their native startup
ordering, but may be a read-only dependency of a managed, affected-component release.

Release steps use a stable topological order, including every dependency replica
before a dependent component. The frozen release graph and readiness policy survive
configuration edits and controller restart. A failed selected dependency prevents
the dependent's preparation and dispatch and fails the release; other pending steps
may remain unexecuted. A waiting dependency cannot be bypassed by restarting OAP.

An unselected dependency is not added to the release or rebuilt. Before preparation
and again before dispatch, OAP inspects its current non-retired bindings against the
frozen replica count, resource identity/ownership and readiness policy. Missing,
unhealthy or foreign resources hold the release in attention. Restore the dependency
and explicitly resume the recorded pre-dispatch phase. Prepared-phase recovery
resumes prepared work; it does not prepare the resource again or repeat dispatch.
Once dispatch begins, the existing exact-operation recovery rule still applies.
Retirement does not enforce startup dependencies.

This is release/start ordering, not continuous dependency monitoring, a transaction
across services, parallel orchestration, reverse shutdown ordering or a promise that
health cannot change after a check. Selected dependencies use their recorded release
verification; unselected ones use live checks. A race between check and provider
dispatch remains possible. HTTP probes and runtime rollback are still later work.

Settings exposes dependency selection and a readable graph diff; release review and
activity show frozen edges. The connected Docker example waits for API native health
before starting its frontend. Local tests cover invalid graphs, replica readiness,
process restart, immutable graphs, failed dependencies, unselected dependency drift
before dispatch, safe pre-dispatch recovery, signed GitHub affected-component
selection and native Docker connectivity/restart/scaling.

Dependency slice staging verification: commit
`9eadc868f4a156c07609ca3982ea569885563047`, deployment
`8xvsfnuufuwbbb08al76f800`, healthy runtime. Exact-tree local run
`20261010-214932-7966bb45` passed, including the connected native Docker fixture,
signed selected-component GitHub dependency check, recovery and browser configuration
diff. The live owner UI verified the dependency panel on the retained single-component
application without saving; multi-component selection/order was tested locally.
Retained source release and replica digests remain unchanged and healthy. No
migration or new hosted workload was required. Older controllers also ignore startup
dependency fields; do not treat readability as ordering compatibility on rollback.
