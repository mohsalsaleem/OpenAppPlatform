# Install Open App Platform

This package runs one non-root Go service with the bundled UI and PostgreSQL 17
for OAP metadata. Workloads remain in standalone Docker or their existing operator.
It starts in owner mode; no shared preview token or open signup is enabled.

Requirements: Docker Engine compatible with API v1.45, Docker Compose v2 or newer,
Python 3, and a trusted OAP image built for the host architecture. Linux and Docker
Desktop Unix sockets are supported locally; remote SSH/TLS Engine transports are
not supported. First builds/downloads require network access. Application images
can be preloaded for operation without external registries.

## Build and Prepare

From a checked-out source revision:

```sh
docker build --label org.opencontainers.image.revision="$(git rev-parse HEAD)" \
  -t openappplatform:local .
python3 scripts/install.py init --directory .local/install \
  --image "$(docker image inspect --format '{{.Id}}' openappplatform:local)"
(cd .local/install && docker compose up -d --wait)
```

The initializer pins the OAP content ID and a PostgreSQL digest. It refuses to
overwrite an existing installation. Its directory is private (0700) and `.env`
contains generated database/setup secrets (0600); do not commit or share it.
Read `OAP_SETUP_TOKEN` from that private file for the browser's **Setup Secret**.
Open `http://127.0.0.1:8878`, create the owner account, and store the chosen password
in your password manager. Further users join through owner invitations. Preserve
`.env`, `targets.json`, any trusted hook configuration and metadata backups.

Use `--port <port>` to change the loopback UI port. The package does not publish
PostgreSQL. Its owner-only local HTTP declaration is derived from the same
loopback IP used for the published port, allowing the internal container listener
without widening preview mode. This is a trusted deployment declaration, not
a server inspection of Docker port configuration. HTTP cookies are configured for this local HTTP endpoint. Before
hosting the UI externally, provide HTTPS through your operator/proxy, set
`OAP_COOKIE_SECURE=true`, and review the bind address. A public TLS/domain setup is
outside this installation package.

Run Compose from the installation directory so both generated Compose files and
its private environment resolve consistently. The commands above/below use a
subshell to keep your main terminal in the repository root.

## Standalone Docker Permissions

The initializer finds the local Unix socket, or accepts `--docker-socket <path>`.
It probes the socket's group **as seen by a container**, which also handles Docker
Desktop's different host/container ownership. The shipping image must be cached
for that probe. Compose adds that group to UID 10001 and mounts the socket; it
never changes host socket permissions or runs OAP as root.

The daemon/socket must permit that group to connect (normally a group-writable
socket). If the connection check fails, fix access through the daemon's normal
administrative procedure. Do not make the socket world-writable. Docker socket
access is privileged host control; grant it only to a trusted OAP installation.
`read_only`, dropped capabilities and a non-root user do not make the socket a
restricted Docker API. In the dashboard, check **Infrastructure → Deployment
Targets → Check Connection** before deploying.

Application host ports are on the daemon host, not the OAP container. A managed
image can omit host ports for private app-local networking. Persistent volumes
require one instance; standard replacement can cause brief downtime.

## Optional Coolify Connection

For operator-only operation, use `init --operator-only --image <content-ID>`.
This omits the socket overlay and starts with an empty target list. Add a trusted
Coolify target to `targets.json` using [the target example](../examples/targets.json)
and set its referenced token privately in the installation `.env`. Recreate only
OAP to load the configuration:

```sh
(cd .local/install && docker compose up -d --no-deps --force-recreate --wait platform)
```

For an OAP instance hosted directly in Coolify, use the same shipping Dockerfile,
owner-mode environment and a dedicated OAP metadata database; retain secure
cookies on its HTTPS endpoint. Mount the reference-only targets file and configure
`OAP_TARGETS_FILE` accordingly. Existing Coolify application data, GitHub App
builds/triggers and registry login remain native. Connecting does not import
credentials or grant OAP database lifecycle authority. Use a dedicated non-production
scope for testing.

## Complete Example

Follow [frontend/API/worker/existing PostgreSQL](../examples/application-breadth/README.md).
Prepare the database and its restricted application role in the operator first.
For Docker, explicitly allow its immutable container ID in the target's
`observeContainers` list and provide network reachability outside OAP. Linking
alone does not attach networks or inject credentials.

Create the three image components, link the database and grant the two scoped
`DATABASE_URL` references using [runtime inputs](runtime-secret-references.md).
Set the private values and `OAP_RUNTIME_INPUTS` in the installation `.env`, then
recreate the controller as above. Deploy the supported image components and check
frontend connectivity, worker logs and 4/4 health. Docker private pull grants use
[private registry access](private-registry-access.md); Coolify uses native login.

## Update and Compatible Image Rollback

Record the current immutable image identity and source revision. Build/pull the
candidate, preserve the private configuration and take a quiesced metadata backup
as below. Edit only `OAP_IMAGE` to its immutable digest/content ID. Preflight the
candidate against the applied migration ledger:

```sh
(cd .local/install && docker compose run --rm --no-deps -T \
  --entrypoint /app/oap-admin platform check-schema)
(cd .local/install && docker compose up -d --no-deps --wait platform)
```

Preflight is read-only. It permits known pending migrations for an update and
rejects unknown or changed applied migrations. Startup also rejects unknown
migration versions. Verify owner login, connection checks, release states, health
and logs after replacing OAP; native workloads should retain their identities.

For rollback, stop OAP, pin the recorded previous compatible image, run its
`check-schema`, then recreate only the platform. If preflight fails or the image
has no preflight command, keep the controller stopped and review compatibility.
There is no automatic SQL downgrade. A matching ledger is a required check, not
proof of arbitrary application or semantic downgrade compatibility; use a tested
release pair. The reproducible drill uses the same binary/schema with a different
image identity to test the packaging replacement path.

## Metadata Backup and Isolated Restore

Pause OAP before taking a backup. Its native workloads can continue running:

```sh
(cd .local/install && docker compose stop platform)
mkdir -p .local/install/backups
python3 scripts/install.py backup --directory .local/install \
  --archive .local/install/backups/metadata.dump
python3 scripts/install.py restore-check --directory .local/install \
  --archive .local/install/backups/metadata.dump --database oap_restore_verified
```

Backup saves a PostgreSQL custom archive and SHA-256 receipt privately. The helper
rejects a running platform, an existing archive destination and checksum/format
errors. Restore creates a **new** `oap_restore_<name>` database, never overwrites
an existing database, and checks identity counts and the migration ledger. It
leaves that database offline and retains unsuccessful copies for inspection.
Copy the archive/receipt and private configuration to durable storage outside the
host. The helper does not schedule backups, encrypt them or back up workload
volumes/databases; use your storage/operator procedure for those.

A historical OAP backup may predate native operations. Before activating a restored
copy, keep all original controllers stopped, account for those operations with the
operator, verify bindings/claims and image compatibility, restore the exact secret
grants, and arrange a single-controller cutover. Do not edit release rows or run
controllers against competing copies. See [recovery boundaries](recovery-runbook.md).
Automatic reconciliation of an arbitrarily stale backup is not implemented.

After an accepted clean backup/restore check, resume the original current database
with `(cd .local/install && docker compose up -d --wait platform)`.
The restored copy is not activated by this command. Native crashes are handled
by restart policies; an interrupted release can still require explicit observation
recovery. Keep native operator access available independently of OAP.

## Acceptance

`make test-local` builds the shipping Dockerfile with pinned build/runtime images,
loads it into the disposable Engine and exercises these exact Compose files. A
fresh metadata database completes browser owner setup and the four-component
example. The drill checks non-root socket access, logs, worker restart, process
crash recovery, compatible image replacement/rollback, corrupt archive rejection,
isolated metadata restoration and restored owner login. Native workload IDs and
existing application database configuration/data must stay unchanged. Reports and
screenshots are under `.local/test-env/runs/<run-ID>/installation/`.

This is the single-server M0 package. Multi-server installation matrices,
PostgreSQL major upgrades, automatic data restore and general routing remain
separate roadmap work.
