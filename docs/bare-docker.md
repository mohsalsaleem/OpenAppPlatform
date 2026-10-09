# Bare Docker targets

OpenAppPlatform now deploys web components directly on Docker Engine without
Coolify, Dokploy, Dokku, Portainer, Swarm, or Kubernetes. The same application,
release, and instance-binding model is used for Coolify and Docker.

## Connect

Use examples/docker-target.json and set the URL to the local Docker Unix socket.
Linux commonly uses unix:///var/run/docker.sock. Docker Desktop on this Mac uses
unix:///Users/samsal/.docker/run/docker.sock. Remote SSH and mutually authenticated
TLS transports are not implemented yet.

The adapter uses Engine API v1.45 and requires a compatible daemon. Socket access
is privileged; configure targets on the server rather than accepting arbitrary
socket paths from application manifests.

## Offline example

Build the Hello API example and keep pulling disabled:

```sh
docker build -t oap-hello-api:dev examples/hello-api
python3 scripts/run-local.py python3 scripts/example.py examples/docker-hello/application.json --deploy
```

The example publishes port 8791 on loopback. Host port 0 keeps the component
private. Fixed host ports require one instance; multi-instance routing is not
implemented. Docker networks provide component aliases but are not health-aware
load balancers.

pullPolicy defaults to never. if-missing enables public registry pulling; private
registry authentication is a future capability. Existing cached images can be
used without any registry connection. Image tags are convenient for development;
use digest references for portable artifact identity.

## Standard replacement

Instances use stable resource references rather than container IDs. Ensure
creates a stopped candidate when runtime configuration differs. Deploy stops the
active instance, retains it under a previous name, promotes the candidate, and
starts it. Observation verifies the resulting container ID and Docker health.
An unchanged configuration reuses the active instance.

This is standard replacement and can cause downtime. It is not blue-green.
Previous stopped containers are retained for inspection, not exposed as an
automatic rollback guarantee. Uncertain dispatch is handled by the controller's
attention state rather than blind retry.

Discovery and mutation require target, environment, reference, and ownership
labels. Unrelated containers are never adopted implicitly. Container cleanup
never requests volume deletion. Mount configuration and durable upload/storage
management are not implemented yet; do not use this milestone to provision
stateful services.

Application networks are scoped by target and application identity. Networks do
not extend across hosts. Engine restart policies provide local process recovery;
the controller does not provide recovery from host failure or automatic scaling.

## Runtime configuration and connections

Managed Docker components accept `env` string maps and `services` maps from
variable names to managed component names. A connection such as
`{"API_URL":"api"}` becomes `API_URL=http://api:<release-port>` inside the
application network. References must stay within the application and cannot
point to adopted resources. They are resolved from each frozen release, so a
port update and its callers' configuration are applied together by that release.
Deployment order is the manifest order; dependency readiness ordering is not
implemented. Standard updates can interrupt connectivity.

Variables are bounded plain configuration, visible in application definitions
and release history. They are not a secret store. Docker image defaults remain
available unless overridden. Changing or removing runtime variables changes the
container revision and requires deployment; saving alone does not apply them.

`POST /api/v1/applications/{id}/restarts` accepts `component`, `ordinal`, and
`expectedVersion`, with an `Idempotency-Key`. The durable operation restarts just
the bound active container, preserves its identity and current configuration,
and observes its health. An uncertain restart enters attention instead of being
reissued. No prepared replacement is promoted by restart.

Scale up from the configuration editor and deploy. Existing unchanged containers
are reused and new ordinals receive their own instances. Fixed host ports still
require one instance. Explicit scale-down is available from the overview. Traffic draining and replica
routing are not implemented.

Scale-down retains containers, volumes, and instance bindings. The definition's
count is committed only after every selected container is stopped and identified.
Partial progress stays visible; uncertain stop outcomes require observation
recovery. Stopped bindings remain reserved and can be reactivated during later
scale-up. Retired instances started directly in Docker are shown as drift.
