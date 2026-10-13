# Development, testing and deployment

How to run OAP from source, run its test suites and build the deployable image.
For operating OAP with an agent, start with the
[standalone agent runbook](standalone-agent-runbook.md).

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
retain secure cookies and HTTPS. See [owner access](owner-access.md).

The local PostgreSQL port is 55438 and its data uses a named Docker volume.
Development credentials in compose.yaml are intentionally local-only. Do not
reuse them for production. The controller does not need a Docker socket when
using Coolify; direct Docker targets use a configured local socket, and it operates through the scoped API target.

## Tests

GitHub CI is disabled for now. The required pre-push check is `make test-local`,
which starts fresh PostgreSQL, a separate Docker daemon, and the browser runner.
See [reproducible local testing](local-testing.md). Install its Git hook with
`make install-test-hook`.

```sh
make test                   # unit tests and PostgreSQL system tests, with -race
make test-system            # focused core system tests
npm --prefix web run build  # TypeScript and production UI build
python3 scripts/run-local.py npm --prefix web run test:e2e
```

The default browser suite uses the isolated standalone Docker target; Coolify
credentials are not required. `make test-local` supplies the server, daemon and
browser. External Coolify tests are opt-in.

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

## Deployment

The Dockerfile builds the UI and Go binary into a non-root image. Supply a
PostgreSQL database, first-run setup secret, and a mounted target config;
operator credentials are needed only for operator targets.
pass -targets /path/to/targets.json. The controller can run through Coolify or on
Docker directly. The direct Docker adapter manages explicitly owned application containers through
a local Unix socket; no separate scheduler is required.

.coolify/deploy.yaml records product staging and retained integration fixtures.
The platform is available at https://oap-staging.mohsal.dev in owner mode, with
server-built image provenance and a verified manual database backup/restore.
See [server deployment and recovery](server-build-deploy.md) for setup and
operational limits. Production release requirements remain separate.

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
