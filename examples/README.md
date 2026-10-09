# Examples

These are executable JSON manifests for the first milestone. Both examples use
prebuilt Nginx images and standard deployment. They do not configure public
routing or automatic balancing. Workers, scheduled jobs, source builds, and
advanced deployment strategies are intentionally not accepted yet.

## Configure a target

Copy targets.json to .local/targets.json, replace the placeholder IDs with a
non-production project and server, and set COOLIFY_TOKEN in .env.local. The token
is referenced by environment-variable name; never include it in target JSON.

Application environment must match the target environment. Example image tags
are convenient for exploration; resolve and use an image digest before a release
that needs exact artifact identity.

## Create and deploy

Run the platform as described in the root README. Then:

```sh
python3 scripts/run-local.py python3 scripts/example.py examples/hello-web/application.json
```

The script creates the application definition and prints its ID. It does not
start workloads unless --deploy is supplied:

```sh
python3 scripts/run-local.py python3 scripts/example.py examples/two-components/application.json --deploy
```

Standard deployment can restart existing managed resources and cause downtime.
The examples create resources only inside the configured target. They never
remove resources or durable storage.

## Adopt a Docker-image resource

Add resourceId to a component using an existing resource UUID from the target's
Inspect resources view. Set image to the resource's existing image reference and
instances to 1. Adoption preserves provider configuration; this milestone can
redeploy the adopted image resource but does not edit its image or Git source.
Disable any independent automatic deployment trigger before controller-managed
releases. Application port must describe the actual existing workload port.

## Build your own API image

[Hello API](hello-api/README.md) includes a minimal Go service, readiness endpoint,
graceful shutdown, and Dockerfile. Use it to experiment with a custom image;
publishing an image is a separate registry workflow.

[GitHub Actions](github-actions.yml) shows how to submit an immutable image release
from CI. Rename the example web component key if your application uses another
component name, and poll the returned deployment ID before declaring success.

## Bare Docker

Use [docker-target.json](docker-target.json), set your local Unix socket path,
and build [Hello API](hello-api/README.md). Then create and deploy the
[docker-hello definition](docker-hello/application.json). With pullPolicy never,
this path uses cached images and requires no registry access.

## Connected Docker components

Build the Hello API image, configure the `docker-local` target, and deploy:

```sh
docker build -t oap-hello-api:dev examples/hello-api
python3 scripts/run-local.py python3 scripts/example.py examples/docker-connected/application.json --deploy
curl http://127.0.0.1:8792/upstream
```

The frontend receives `UPSTREAM_URL=http://api:8080` from its `services` map.
The API stays private and receives `VERSION=api-v1` from `env`. Its response
through the frontend demonstrates actual DNS connectivity between components.
In Configuration, increase API instances to 2 and deploy to scale up; the frontend
continues using the same component alias. Docker DNS is not a health-aware load
balancer. Restart one instance from its overview row to restart its existing
configuration without applying saved changes.

`env` is plain non-secret configuration stored in definitions and release
snapshots. Keep credentials out of these manifests. Managed variables and
application DNS connections currently require the Docker adapter; Coolify
variables and endpoints remain managed in Coolify. Native restart is available
on both adapters. Docker scale-down is available from the overview; stateful volumes remain subsequent work.

After scaling the connected API to two instances, choose **Scale down api** and
retain one instance. `/upstream` continues using the remaining API. The retired
instance remains visible and its logs are readable. Increase the count and deploy
again to reactivate it. Scale-down does not implement traffic draining or delete
containers or volumes.
