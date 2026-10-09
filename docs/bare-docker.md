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
