# Pre-deploy jobs

On standalone Docker, declare `kind: "job"` for an image-defined run-to-completion
process and list it in the dependent component's `dependsOn`. A job runs once per
release before its dependent workloads. Selected releases automatically include
required job dependencies; the reviewed plan shows their names, immutable images
and possible application-data effects. Saving a specification never runs a job.

See the [migration example](../examples/pre-deploy-jobs/README.md).

## Interface and evidence

Use the existing CLI `validate-spec`, `action-plan`, `execute-action`, `wait`,
`operation`, `instances`, `incident --component migrate`, and `verify` commands.
REST and operating MCP use the same controller and schemas. Target capabilities
advertise `preDeployJobs`; operator targets reject the kind.

Each job permits one owned image process, no public ports, adoption, persistent
volume mounts or health probes. It can use environment/secret references, app-local
networking and resource limits. Its image supplies the command; OAP does not expose
an arbitrary shell or application SQL executor. `readiness.timeoutSeconds` sets an
observation deadline between 30 and 1800 seconds (default 900); `requireHealthy`
is not valid for jobs.

The operation step records `resourceId`, `remoteDeploymentId`, `observed`, and
`exitCode` when known. Running is not success. Exit code 0 completes the job; a
nonzero exit or dead process fails the release and leaves remaining steps pending.
Already running siblings retain their previous version. `instances` identifies
job kind and reports `completed:0` separately from running workload health.

## Replay and recovery

Each release has a distinct container reference and native release label. Same
release preparation retains the same immutable inputs and container. Restart
policy is `no`; inherited image health checks are disabled, and there is no stable
job service alias. A completed or failed process cannot be restarted through OAP.
Older job containers remain as evidence; automatic retention/pruning is deferred.

The durable controller records dispatch before starting the process. Interrupted
or uncertain dispatch enters attention. Use the exact process ID exposed as the
instance resource's `runtimeId` to create an existing `recover` action plan; recovery
only observes that process. Changed identity or restart/health configuration is
rejected. A new release does not accept an earlier release's completion as its
migration result, and an earlier job still running blocks another migration.

A timeout enters attention without claiming completion or cancelling the process.
Inspect the operation and instance before recovery. A process may have committed a
migration and then crashed; these semantics do not promise exactly-once database
effects. Use transactional or safely repeatable migrations. Image rollback skips
jobs and never reverses schema or application data.

## Local acceptance

Unit tests cover validation, transitive job selection, creation settings, native
completion/exit evidence, replay and ownership/runtime drift. A pinned isolated
Docker fixture exercises the named-agent CLI: migration success and failure,
failed rollout preserving the existing API, fresh release execution, public native
identity for observation-only interrupted-dispatch recovery, and timeout held
without completion or dependent dispatch. Timeout/crash timestamps are injected
by the test host as fault drills; operating agent calls remain CLI-only.

The same journey restores all workload images without rerunning jobs and verifies
named-volume data survives. Separate routed acceptance verifies image rollback
through Caddy while sibling routes remain live. Full exact-tree gate evidence is
recorded privately by the test harness; these tests do not certify a newer tree
until that gate passes.
