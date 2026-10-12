# Bare Docker targets

OpenAppPlatform now deploys web components directly on Docker Engine without
Coolify, Dokploy, Dokku, Portainer, Swarm, or Kubernetes. The same application,
release, and instance-binding model is used for Coolify and Docker.

## Connect

Use `examples/docker-target.json` and set the URL to the local Docker Unix socket.
Run OAP with that target file; no Coolify URL, token, project or server ID is required.
Linux commonly uses `unix:///var/run/docker.sock`. For Docker Desktop, use the
Unix endpoint reported by `docker context inspect --format '{{.Endpoints.docker.Host}}'`. Remote SSH and mutually authenticated
TLS transports are not implemented yet.

The adapter uses Engine API v1.45 and requires a compatible daemon. Socket access
is privileged; configure targets on the server rather than accepting arbitrary
socket paths from application manifests.

## Start without an operator

From the repository root, prepare the Docker target and the compiled UI:

```sh
mkdir -p .local
cp examples/docker-target.json .local/docker-targets.json
# Set url in that file to your local Unix Docker endpoint.
docker compose up -d --wait postgres
npm --prefix web ci
npm --prefix web run build
```

Start the controller as your normal OS user with access to that socket:

```sh
DATABASE_URL='postgres://oap:oap-local@127.0.0.1:55438/oap?sslmode=disable' \
OAP_ADDR=127.0.0.1:8787 OAP_AUTH_MODE=owner OAP_COOKIE_SECURE=false \
OAP_SETUP_TOKEN='<your-random-setup-secret-of-at-least-24-characters>' \
go run ./cmd/platform -targets .local/docker-targets.json
```

Open `http://127.0.0.1:8787`, complete owner setup on a fresh metadata database
(or sign in to an existing workspace), and select **Local Docker**
when creating an application. This command uses environment variables directly;
it does not load `.env.local` or require any operator credentials. PostgreSQL
stores OAP metadata; the workload adapter talks directly to Docker Engine.
The Compose file starts the metadata database only. OAP manages application
containers through its release API, not through a generated Compose stack.
Use the development database credentials only for this local walkthrough.

A containerized OAP installation needs an explicitly mounted Unix socket and
permission for its non-root runtime to access it; a target URL alone does not
provide that access. Do not make the socket world-writable. Container installation
packaging is tracked in P0.5.

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
never requests volume deletion. Explicit named volumes and resource limits are
supported for single-instance owned components. Database provisioning, backups
and automatic data restore remain outside this slice.

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
Explicit startup dependencies gate dispatch using all dependency replicas;
unselected bound dependencies are inspected without deployment. Standard updates can interrupt connectivity.

Variables are bounded plain configuration, visible in application definitions
and release history. Use [runtime secret references](runtime-secret-references.md) for sensitive inputs.
Docker image defaults remain
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

Named mounts and CPU/memory limits are available for owned image components. See
[persistent volumes and resource limits](persistent-volumes.md) for retention,
permissions, one-writer restrictions and operator-managed backups.

## Standalone persistence example

```sh
docker build -t oap-persistent-web:dev examples/persistent-web
```

[The Docker manifest](../examples/persistent-web/application.docker.json) uses
`docker-local`, the cached image above, a retained `data` mount at `/data`,
128 MiB / 250 millicores, and loopback port 8793. Create the equivalent component
in the dashboard or submit the manifest using a scoped API credential. The
example helper requires that credential; an owner session does not automatically
create an API token.

Deploy, read `http://127.0.0.1:8793`, and restart from OAP. The boot counter must
increase. Build a distinct image revision and review a deployment to exercise
replacement while retaining the same data:

```sh
docker build --label io.openappplatform.example.revision=2 \
  -t oap-persistent-web:dev examples/persistent-web
```
 OAP supports multiple stateless instances, workers,
app-local component DNS, logs, health and release recovery directly on Docker;
local writable volumes require one instance. This does not add a load balancer,
remote socket transport, or recovery from server/disk loss.

The reproducible `make test-local` suite runs these lifecycle and persistence
checks against its own Docker daemon with Coolify credentials removed. See
[local testing](local-testing.md).
