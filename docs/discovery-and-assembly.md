# Guided discovery and application assembly

Open Deployment targets, choose **Group services**, select existing services, and review their component names before creating the application. Registration saves only OAP metadata and target-scoped bindings; it neither queues a release nor mutates workloads. Existing resource reservations are shown in discovery and duplicate grouping fails atomically.

## Observation and handoff

New registrations with `resourceId` always start `management: "observe"`, including API registrations that omit the field. Image-backed and source-backed Coolify applications are observable. Missing operator image/port information may remain empty/zero in observe-only definitions. Health and logs use the exact resource ID independently of release history. Environment values and credentials are not imported.

Deploy and restart are denied for observe-only components. An application-wide deployment is blocked while any component remains observed. Configuration editing cannot change the management mode or adopted runtime fields.

`POST /api/v1/applications/{id}/management` with `{ "component": "api", "expectedVersion": 1 }` explicitly enables lifecycle management for an existing Coolify image service. This increments the definition version without changing provider ownership/source/routes/variables/volumes or dispatching a deployment. It checks adapter support, exact current image and port, existing OAP ownership markers, and absence of active/attention releases. Source-backed apps stay observed until the source-build contract is implemented. Native operator triggers remain operator-managed; avoid concurrent lifecycle commands from both systems.

Legacy managed bindings and snapshots remain compatible; no migration rewrites old permissions. New registrations start observed. Full owner identity, scoped credentials and mutation audit remain M1 work.

## Bare Docker scope

Owned active OAP containers remain discoverable in their configured target/environment scope. To include existing external containers, configure `settings.observeContainers` with up to 64 full 64-character immutable container IDs. No daemon-wide enumeration is enabled. External containers are readable only by those exact IDs; their names cannot redirect a binding to a replacement. Deploy, restart, stop, retirement and management handoff remain unavailable for external containers. Removed IDs disappear from discovery and existing bindings show unavailable health rather than silently binding a replacement.

Example target settings:

```json
{
  "pullPolicy": "never",
  "hostBindIP": "127.0.0.1",
  "observeContainers": ["<full-existing-container-id>"]
}
```

## Verification

The reproducible local suite includes PostgreSQL reservation conflicts, observe-only lifecycle denial, image/source handoff boundaries, immutable Docker discovery with rejected mutations, and a real Docker browser assembly flow. The optional `TestLiveCoolifyObserveOnlyAssembly` test reads two services in the explicitly configured staging environment, registers them locally, checks health/logs and unchanged safe provider configuration, and verifies no release is dispatched. It does not provision or restart a remote fixture.

Run `make test-local`. For the existing staging configuration, run:

```sh
OAP_LIVE_COOLIFY=1 python3 scripts/run-local.py go test -v ./tests/system -run '^TestLiveCoolifyObserveOnlyAssembly$' -count=1 -timeout=3m
```

This slice does not add target credential entry, owner signup, source builds, database imports, automatic grouping, or topology mutation. Those follow the existing roadmap.
