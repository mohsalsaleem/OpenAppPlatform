# Reproducible local verification

GitHub CI is disabled for now. Changes are verified locally before pushing.
The repository includes an isolated full test environment and a pre-push guard.

## One command

```sh
make test-local
```

Requirements are Docker with Compose v2, Python 3, and Git. Host Go, Node,
PostgreSQL, browser installations, .env.local, Coolify credentials, and an existing
application server are not used by this command.

The first bootstrap downloads pinned public development images and locked Go/npm
dependencies. Subsequent builds reuse Docker layers. Runtime tests run on an
internal network without registry pulls or external service access. This is
reproducible local verification, not a guarantee of bit-identical build artifacts
or a fully offline first installation.

## Isolation

Each run creates a unique Compose project containing:

- Ephemeral PostgreSQL with test-only credentials and no published host port.
- A dedicated Docker-in-Docker daemon reached only through a shared Unix socket.
- A runner with precompiled race-enabled Go tests and the matching Playwright
  browser/package version.

The runner shares the test daemon's network namespace so real fixture endpoints
are reachable on loopback. It never receives the host Docker socket or developer
secrets. Fixture images are built locally, exported, and loaded into the isolated
daemon before tests; the adapter uses pullPolicy never.

Docker-in-Docker requires a privileged test container. Run only trusted repository
code in this harness. Its Docker objects belong to the disposable test daemon,
not your application daemon. Default cleanup removes only the unique test
project, its network, and its socket/data volumes.

## What runs

The image build checks Go static analysis and TypeScript/Vite compilation. The
runner executes all packages containing Go tests with the race detector, including
PostgreSQL system tests and the real Docker lifecycle/replacement test. It then
starts a fresh platform and runs browser setup/configuration/target-discovery
checks and a real MCP stdio handshake/application read.

The external Coolify mutation test remains an explicit opt-in test outside this
harness. Its skip is recorded and expected. Other unexpected skips fail the full
local run, so missing database or Docker configuration cannot look like success.

## Reports and cleanup

Reports are written to .local/test-env/runs/<run-id>/:

- result.json: source fingerprint, image pins, outcome, and suite results.
- suite.json: individual test execution results.
- core-*.log, browser.log, platform.log, services.log.
- Desktop/mobile browser screenshots.

Reports and fixture archives are ignored by Git. A failed run invalidates the
previous success stamp. Source changes during verification also invalidate it.
Every normal run begins with fresh service state and removes it afterward. Builds
use a captured Git-visible source snapshot, not arbitrary ignored files from the
developer checkout. Test images/build caches remain available for later runs.

For diagnosis, retain only a failing run's own environment:

```sh
python3 scripts/test-env.py run --keep
```

The report records its Compose project name. Cleanup can be performed using that
exact project and tests/env/compose.yaml; do not use blanket Docker pruning.

## Push gate

Install the repository-local hook once per clone:

```sh
make install-test-hook
```

The intended workflow is:

```sh
make test-local
git add <changed-files>
git commit -m 'Describe the change'
git push
```

The hook requires a clean working tree and a matching passing fingerprint for the
committed source. The fingerprint includes tracked and non-ignored source files
and executable bits, while excluding Git metadata and generated ignored artifacts.
Committing unchanged tested content does not invalidate the result.

This is a developer workflow guard, not a security boundary: an owner can bypass
Git hooks. For this project, bypassing it is not the normal push workflow.

## Version pins

Test image versions and digests live in tests/env/images.env. npm and Go dependencies
use their committed lock/checksum files. The runner verifies that its Playwright
package matches the pinned browser image. Update pins deliberately and rerun the
suite before pushing.

GitHub's workflow is disabled in repository settings and has no push/PR triggers.
It retains workflow_dispatch for a future deliberate re-enable; ordinary pushes
must not start remote CI.

## Native HTTP probe coverage

`make test-local` runs the native Docker HTTP probe system test in the isolated engine, alongside race-enabled controller and adapter checks. A fixture image carries a healthy image check, while `/health/custom` succeeds and `/health/fail` returns 503. The test configures an override, proves that the failing endpoint fails the release despite the image's healthy check, and returns explicitly to the image check while preserving the stable binding. Browser E2E edits probe settings, rejects an external URL, deploys a real native Docker runtime, and verifies healthy completion. Controller fixtures cover native configuration drift, frozen snapshots, process restart and owner recovery without duplicate dispatch. Coolify adapter tests read back the configured native fields and reject drift/out-of-scope resources; live Coolify verification is recorded separately and is not part of the offline baseline.

Core Go test packages have a 180-second deadline and a 240-second runner guard. Reports, screenshots and logs are retained under `.local/test-env/runs/<runId>/`; test databases and Docker state are isolated and removed on completion.

To exercise a real operator after deploying OAP to staging, run the opt-in script with a private owner credential JSON file and a pinned image that already includes a healthy image check:

```sh
python3 scripts/verify-staging-health-checks.py \
  --credentials-file .local/staging-workspace-credentials.json \
  --target-id coolify-staging \
  --image 'registry.example/fixture@sha256:<64-hex-digest>' \
  --port 8025 --path / \
  --receipt .local/staging-health-check-receipt.json
```

It creates/reuses `native-health-check-staging`, configures an HTTP check, deploys through the operator, returns explicitly to the image check, and verifies healthy completion plus the same resource binding. It retains one stateless fixture for subsequent runs. It refuses non-staging targets and incompatible existing topology. A failed/uncertain release requires inspection; the script does not delete it or automatically reissue its dispatch. Owner credentials and receipts remain private/ignored; the offline suite needs neither internet nor these credentials after its pinned images are cached.
