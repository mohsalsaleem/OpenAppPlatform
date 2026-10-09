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
