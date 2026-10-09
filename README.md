# OpenAppPlatform

An application-first control plane for Docker infrastructure. Group web services
into applications, track releases, and keep your existing deployment operator.
The product experience is inspired by DigitalOcean App Platform.

## Current milestone

Implemented: Go API and controller, PostgreSQL persistence, River deployment
jobs, a React dashboard, and a target-scoped Coolify adapter. Standard releases
support multiple web components and instances, explicit Docker-image resource
adoption, frozen artifacts, idempotency, progress, and bounded logs.

Verified locally with PostgreSQL and Chromium, and through a real deployment on
an isolated staging project in the owner's existing Coolify.

Bare Docker is a first-class planned target; its adapter is not implemented yet.
Dokploy, Dokku, Portainer, workers, jobs, direct Git push webhooks, source builds,
MCP, AI diagnosis, rolling deployments, and blue-green remain planned.

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

Open http://127.0.0.1:8787 and connect using OAP_API_TOKEN from your local
configuration. Use a random token of at least 24 characters. The UI stores it in
sessionStorage for that browser tab. This is a single-owner local preview;
production identity, authorization, and audit retention are not implemented.

The local PostgreSQL port is 55438 and its data uses a named Docker volume.
Development credentials in compose.yaml are intentionally local-only. Do not
reuse them for production. The controller does not need a Docker socket when
using Coolify; it operates through the scoped API target.

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
OAP_LIVE_COOLIFY=1, the real operator test explicitly skips. CI provides a local
PostgreSQL service and runs the core suite and dashboard build; it does not
mutate an external Coolify installation. Live fixtures are retained, not deleted.

## Documentation

- [Product and architecture](docs/product-and-architecture.md)
- [System design](docs/architecture/system-design.md)
- [Low level design](docs/architecture/low-level-design.md)
- [OpenAPI contract](docs/openapi.json)
- [Bare Docker design](docs/bare-docker.md)
- [Verification report](docs/verification.md)
- [Editable design Page](https://chatgpt.com/space/page_48ca8aa82f6881918cae167d00860fe9)

## Deployment

The Dockerfile builds the UI and Go binary into a non-root image. Supply a
PostgreSQL database, API token, operator credentials, and a mounted target config;
pass -targets /path/to/targets.json. The controller can run through Coolify or on
Docker directly. Running the controller on bare Docker is distinct from the
future adapter that manages application workloads on bare Docker.

.coolify/deploy.yaml records the retained staging test fixture. The platform
itself has not been deployed to Coolify. Backups, restore verification, and
production authentication remain release requirements.

## License

The repository is public. An open-source license has not yet been selected.
