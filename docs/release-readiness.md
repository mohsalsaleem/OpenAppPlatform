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
