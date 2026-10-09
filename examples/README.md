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
