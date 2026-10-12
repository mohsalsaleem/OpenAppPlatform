# Persistent volumes and resource limits

OAP-owned image components can declare up to four named volumes and explicit
CPU/memory limits. Docker and Coolify adapters support the same contract:

```json
{
  "name": "api",
  "kind": "web",
  "image": "your-registry/api@sha256:<digest>",
  "port": 8080,
  "instances": 1,
  "volumes": [{ "name": "uploads", "mountPath": "/data/uploads" }],
  "limits": { "memoryMiB": 256, "cpuMillis": 500 }
}
```

The controls are in New Application and each component's Runtime Configuration.
Saving changes the definition; deploying applies it. Names are component-local,
not references to existing operator volumes. Docker derives a stable name from
target, application, component and volume name and verifies ownership labels.
Coolify creates application-scoped persistent storage through its native API.
Repeated preparation reuses matching storage; conflicts hold the release.

Persistent components require one instance. Saved mounts cannot be renamed,
removed, moved or extended through configuration editing. OAP never requests
volume deletion, host bind mounts, or shared storage. Restart and standard
replacement reuse the same data. Standard replacement stops the previous writer
before starting the new one and can cause downtime. Coolify persistent
components enable and verify its consistent-container-name setting to prevent
native rolling overlap; unavailable settings hold preparation. Explicit limits
and mounts are rechecked before dispatch and while observing the release. Image rollback for a
persistent component requires operator review; OAP does not restore data or
claim that old application code can read a newer data format.

Adding a mount does not migrate files from a running container. Docker initializes
a new volume from the image directory. Migrate any existing ephemeral data in
the operator before deploying a new mount. An older Docker definition that
omits mounted data is held before replacement.

The image must create its mount directories with permissions for its runtime
user before starting. OAP does not run a root initialization container, chmod
host storage or change application users. A permission failure is a deployment
failure. Data on a named local volume survives container replacement, but not
server or disk loss. Configure and test backups in the operator before storing
valuable data. Backups, migrations, restore, and volume removal remain manual
operator work in this slice.

Limits use integer MiB (64–65536) and millicores (50–64000; 1000 is one CPU).
Omitted limits retain native defaults: new OAP workloads currently start with
128 MiB and 250 millicores; existing Coolify limits are not overwritten when
the field is omitted. Once explicit limits are saved, changes require explicit
replacement values. Limits are caps, not guaranteed capacity or reservations.

Observed, adopted and linked database resources reject these overrides.
Their native storage, limits, credentials and backups stay operator-managed.
Coolify requires its application storage API; unavailable endpoints fail the
release explicitly. Other adapters must advertise capabilities before accepting
the fields. Private registry credential references remain the next P0.4 slice.

See [the persistent-web example](../examples/persistent-web/README.md).
