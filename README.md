# Open App Platform

An application-first control plane for self-hosted runtimes and deployment operators. Group web services
into applications, track releases, and keep your existing deployment operator.
The product experience is inspired by DigitalOcean App Platform. The primary
operator-backed journey is connect/configure your existing operator, discover
running resources and group them into applications. Preserve native builds, GitHub
triggers, domains, volumes and secrets. A custom builder is optional.

## Current milestone

Implemented: Go API and controller, PostgreSQL persistence, River deployment
jobs, a React dashboard, target-scoped Coolify and direct Docker adapters, versioned configuration editing,
and a read-only MCP interface. Standard releases
support multiple web components and instances, explicit Docker-image resource
adoption, frozen artifacts, idempotency, progress, live instance inspection,
per-instance bounded logs, explicit observation recovery, per-instance restart,
and Docker runtime variables, application DNS connections, and explicit instance retirement.

Verified locally with PostgreSQL and Chromium, and through a real deployment on
an isolated staging project in the owner's existing Coolify.

Bare Docker uses a local Unix socket and supports cached-image deployments without
a registry connection. Dokploy, Dokku, Portainer, workers, jobs, embedded AI
diagnosis, rolling deployments, and blue-green remain planned. Native Coolify source tracking
and an optional signed immutable-image build lane are implemented and staged;
native source lifecycle handoff and artifact reuse are not.

## Run locally

Requires Go 1.26, Node 22, Docker Engine or Docker Desktop, and Python 3 for the
local helper scripts. These scripts work with environment variables directly in
CI; .env.local is a convenience for local credentials.

```sh
cp .env.example .env.local
mkdir -p .local
cp examples/targets.json .local/targets.json
# Edit local credentials and non-production target IDs.
docker compose up -d --wait
npm --prefix web ci
npm --prefix web run build
make dev
```

Open http://127.0.0.1:8787 and create the owner account using the private
OAP_SETUP_TOKEN from your local configuration. Use a random setup secret of at
least 24 characters. Subsequent visits use email/password sign-in with an HttpOnly
session cookie. Local HTTP uses OAP_COOKIE_SECURE=false; hosted installations
retain secure cookies and HTTPS. See [owner access](docs/owner-access.md).

The local PostgreSQL port is 55438 and its data uses a named Docker volume.
Development credentials in compose.yaml are intentionally local-only. Do not
reuse them for production. The controller does not need a Docker socket when
using Coolify; direct Docker targets use a configured local socket, and it operates through the scoped API target.

## Examples

See [examples](examples/README.md) for a single-component app, a two-component
app, target configuration, and adoption. Creating a definition does not deploy.
Standard deployment can restart workloads and cause downtime. Instances are
tracked together; routing and balancing are not provided in this milestone.

```sh
python3 scripts/run-local.py python3 scripts/example.py examples/hello-web/application.json
```

[GitHub Actions release example](examples/github-actions.yml) builds an image,
then queues its digest through the release API. The operator must be able to
pull that image. Existing independent deployment triggers must be disabled for
controller-owned releases. A 202 response is not deployment success.

## Tests

GitHub CI is disabled for now. The required pre-push check is `make test-local`,
which starts fresh PostgreSQL, a separate Docker daemon, and the browser runner.
See [reproducible local testing](docs/local-testing.md). Install its Git hook with
`make install-test-hook`.

```sh
make test                   # unit tests and PostgreSQL system tests, with -race
make test-system            # focused core system tests
npm --prefix web run build  # TypeScript and production UI build
python3 scripts/run-local.py npm --prefix web run test:e2e
```

Browser tests require the local server and a configured Coolify staging target;
they create application definitions and perform read-only discovery. Install
Chromium once with cd web && npx playwright install chromium.

The opt-in live suite requires COOLIFY_URL, COOLIFY_TOKEN, COOLIFY_PROJECT_ID,
COOLIFY_SERVER_ID, COOLIFY_ENVIRONMENT=staging, COOLIFY_TEST_IMAGE as an immutable
digest, and optionally COOLIFY_TEST_PORT and COOLIFY_TEST_RESOURCE_ID for reusing
a retained Docker-image fixture:

```sh
make test-live
```

Without TEST_DATABASE_URL, database system tests explicitly skip. Without
OAP_LIVE_COOLIFY=1, the real operator test explicitly skips. The isolated local harness supplies PostgreSQL and Docker without developer
credentials. It does not mutate an external Coolify installation. Live fixtures are retained, not deleted.

## Documentation

- [Roadmap](ROADMAP.md)

- [Product and architecture](docs/product-and-architecture.md)
- [System design](docs/architecture/system-design.md)
- [Low level design](docs/architecture/low-level-design.md)
- [Native HTTP health checks](docs/native-health-checks.md)
- [Release cancellation and abandonment](docs/release-controls.md)
- [OpenAPI contract](docs/openapi.json)
- [Bare Docker targets](docs/bare-docker.md)
- [Read-only MCP tools](docs/mcp.md)
- [Verification report](docs/verification.md)
- [Reproducible local test environment](docs/local-testing.md)
- [Editable design Page](https://chatgpt.com/space/page_48ca8aa82f6881918cae167d00860fe9)

## Deployment

The Dockerfile builds the UI and Go binary into a non-root image. Supply a
PostgreSQL database, first-run setup secret, operator credentials, and a mounted target config;
pass -targets /path/to/targets.json. The controller can run through Coolify or on
Docker directly. Running the controller on bare Docker is distinct from the
future adapter that manages application workloads on bare Docker.

.coolify/deploy.yaml records product staging and retained integration fixtures.
The platform is available at https://oap-staging.mohsal.dev in owner mode, with
server-built image provenance and a verified manual database backup/restore.
See [server deployment and recovery](docs/server-build-deploy.md) for setup and
operational limits. Production release requirements remain separate.

## License

The repository is public. An open-source license has not yet been selected.

## Configuration and release preconditions

The configuration editor updates future releases without restarting components.
Updates require the current definition version. Existing releases retain their
snapshots, and a deployment can require expectedVersion to reject a stale plan.
Topology changes, scale-down, target migration, and adopted runtime mutation are
not silently performed through this editor.

SQL migrations now have immutable checksums and version records. Existing preview
schemas are adopted by the idempotent first migration, then upgraded additively.

## Inspect and recover

The application overview polls current instance health independently of release
history. Select an instance to read its latest 100 log lines, including when a
newer release failed before reaching that instance. The API exposes
`GET /api/v1/applications/{id}/instances` and
`GET /api/v1/applications/{id}/logs/{component}?ordinal=2`.

An attention-state release exposes recovery controls in its release history.
Recheck the recorded provider deployment; if the dispatch response was lost,
inspect the operator and supply the exact deployment ID for that instance.
Docker validates the active container ID; Coolify validates deployment-to-resource
association within its latest 100 resource deployments. Selecting the correct historical deployment remains the owner's
responsibility. Recovery never issues another uncertain dispatch. Preparation
can be retried only when it failed before dispatch. Remaining instances resume
through durable jobs once all attention states are resolved.

`POST /api/v1/deployments/{id}/recover` accepts `component`, `ordinal`, and the
release's current `expectedUpdatedAt`, plus either an optional
`remoteDeploymentId` or `retryPreparation: true` where supported. Stale recovery
requests conflict. Recovery does not roll back data or cancel provider work.
If no provider operation can be identified after uncertain dispatch, the release
remains blocked; automatic provider-history reconciliation is still planned.

Target capabilities are exposed at `GET /api/v1/targets/{id}/capabilities` and
checked when configurations and operations are admitted. Native restart is
supported by Docker and Coolify; managed `env` configuration is supported by Docker and controller-owned Coolify
resources. Docker supports application DNS; Coolify connections require explicit
`serviceEndpoints` for referenced components. Unsupported fields are rejected rather than
silently ignored. See the [connected Docker example](examples/README.md#connected-docker-components).

## Scale down and retained instances

Managed Docker components with multiple instances have a **Scale down** action in
the overview. Review the highest ordinals to retire and the remaining count.
OAP stops only those instances, confirms their container identities, and then
commits the new count as a new application definition version. Remaining instances
keep their current configuration. Traffic draining is not implemented.

Retired instances and logs remain visible. Their bindings stay reserved, preventing
accidental adoption by another application. To scale up again, increase the count
in Configuration and deploy; unchanged retained containers can be reused. Restart
cannot reactivate a retired binding. Unexpectedly running retired instances are
reported as drift rather than hidden or automatically stopped.

`POST /api/v1/applications/{id}/scale-down` accepts `component`, `instances`, and
`expectedVersion` with an `Idempotency-Key`. Desired counts remain unchanged until
all stops succeed. A partial or uncertain retirement blocks competing operations
and definition edits. Recovery rechecks the exact stopped container without
another stop; if every stop completed but final persistence failed, use
`retryFinalization: true` with the current `expectedUpdatedAt` on the recovery API.
The operation's `definitionVersion` identifies the admission version, while its
manifest records the intended lower count. Coolify retirement remains unsupported.

## Coolify runtime variables and connections

OAP records provider variable IDs and value hashes before updating owned Coolify
runtime configuration. Existing operator variables are not taken over, even when
their values match. Shared, hidden, preview, renamed, or externally changed
variables are protected. Only previously recorded OAP production-runtime variables
can be removed. Coolify may create preview copies; those remain operator-managed.
Adopted resource configuration is unchanged.

`services` maps a variable to a component. On Coolify, also supply
`serviceEndpoints`, for example `{"api":"https://api.example.com"}`. OAP injects
that explicit URL; it does not infer private DNS, modify networks, or create a
route. URLs must not contain credentials, queries, or fragments. Endpoints remain
under operator/owner management. Docker may use either its application DNS or
an explicit endpoint.

Preparation verifies applied values and preserves unrelated variables. Known
variable IDs and recorded intents allow safe update/removal retries. An ambiguous
creation without a captured provider ID remains blocked for operator review;
inspect and resolve the unverified key through Coolify before retrying preparation.
The ownership ledger is part of OAP metadata and must be included in backups.

## Group existing services

Deployment targets → Group services → Select → Review creates an observe-only
application. Health and logs are available immediately; registration does not
restart or deploy anything. Existing Coolify image services can receive lifecycle
management through a separate reviewed handoff. Source-backed Coolify apps and
explicitly scoped external Docker containers remain observe-only.

See [discovery and assembly](docs/discovery-and-assembly.md) for API semantics,
Docker observation scope, compatibility, and verification. Examples that combine
new managed components with existing resources require handoff of supported
observed components before application-wide deployment.

## Owner setup and access

The platform now defaults to owner mode. Configure a private `OAP_SETUP_TOKEN`
(at least 24 characters) before first launch, then create the owner account in the
UI. Public signup is disabled; members join with owner-issued invitation codes.
Workspace access provides roles, application-scoped agent tokens, revocation and
audit. Existing resources remain in the installation's single workspace.

See [owner access](docs/owner-access.md) for local HTTP configuration, session and
role behavior, agent scopes, legacy preview compatibility and password recovery.
`OAP_API_TOKEN` no longer grants platform access in owner mode.

## GitHub source activity

Use existing Coolify GitHub Apps for native source deployment. OAP can observe
signed push notifications and correlate exact commits with resource-scoped provider
history while keeping management observe-only. An optional trusted image-builder
command supports durable build requests and selected-component digest releases.
See [GitHub releases](docs/github-releases.md) and [examples](examples/github-sources).
The optional hosted image-builder lane passed a real GitHub push and redelivery.
Native source handoff/artifact reuse remains unsupported; CI stays disabled.
See the [operator-first audit and roadmap corrections](docs/operator-first-audit.md).

## Application environments

Logical applications group existing staging/production environment records while
preserving resource IDs, native workflows, release histories and credential scopes.
Owners explicitly link or separate environments through the dashboard. Existing
flat application URLs/APIs stay compatible. Target configuration remains server-side.
See [application environments](docs/application-environments.md).

## Local test cache housekeeping

The test build caches Go compilation independently of root documentation and UI
changes. Test build stages carry `io.openappplatform.test-cache=true`, so obsolete
dangling test layers can be reclaimed on the local Docker endpoint with:

```sh
docker image prune --filter label=io.openappplatform.test-cache=true
```

This targets unused untagged test images; it does not prune application volumes.
Keep the latest test runner tag and recorded screenshots/results for reproducibility.
Failed build containers can retain cache layers and should be inspected before
removal. Do not use volume pruning as a general disk-cleanup command.

The [target setup and definition review](docs/target-setup-and-definition-review.md)
flow generates first-connection recipes, verifies configured scopes read-only,
connects additional existing environments through trusted operators, reuses naming
layouts and previews masked configuration changes. Native workflows remain intact.
