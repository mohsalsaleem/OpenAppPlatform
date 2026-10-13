# Headless standalone bootstrap and recovery: M2.6

One owner establishes trust; a named external agent can then discover its Docker
target and create/manage a bounded set of new applications. No browser, model
runtime, Coolify account or operator proxy is required. The default service stack
stays one Go service and PostgreSQL/River plus the existing Docker Engine.

This slice adds bootstrap/credentials and external recovery. The full portable
multi-component specification, consolidated diagnosis and complete install-to-fix
acceptance remain M2.7–M2.9. This is a preview, not production or zero-downtime
acceptance.

## Host setup

Use a compatible immutable OAP image and matching `oap` CLI binary. The current
repository distribution builds the CLI with `go build -o bin/oap ./cmd/oap` and the
image with the shipping Dockerfile. Published binary/image distribution is not
claimed. Python 3, local Unix Docker access and Compose v2 are required; remote
public Docker endpoints are not supported.

```sh
python3 scripts/install.py init --json --protocol-version 1 \
  --directory .local/standalone --image "$OAP_IMAGE_DIGEST"
python3 scripts/install.py start --directory .local/standalone
python3 scripts/install.py check --directory .local/standalone
```

`init` prepares a private installation, exact image, local Docker target, generated
setup/database secrets and Compose package; it does not start it. The default
loopback port is 8878 and Docker pull policy is `never`. Preload workload images or
explicitly configure an appropriate pull policy before deployment. Socket access
is host-level authority, not an ordinary app credential.

Provide `{email, name, password}` to owner setup on **private stdin**, not in command
arguments, agent chat or public files. The helper obtains the setup secret from the
private installation `.env`; it never prints that file or its contents.

```sh
python3 scripts/install.py owner-setup --directory .local/standalone \
  --cli-bin bin/oap --out .local/standalone/owner.json < private-owner-input.json
```

Keep the input file private (0600) and remove it when appropriate; the helper does
not persist the password. Alternatively `oap setup` consumes the same JSON with
`OAP_SETUP_TOKEN` supplied privately. Existing owners use `oap login` with
`{email,password}`; setup cannot replace an existing owner. Sessions expire after
24 hours and are subject to owner account/session validity.

```sh
bin/oap --url http://127.0.0.1:8878 \
  --credential-file .local/standalone/owner.json \
  --file examples/agents/standalone/grant.json \
  --out .local/standalone/agent.json grant
```

Generated session/agent files are origin-bound JSON, exclusively created with mode
0600 under an existing 0700 directory and fsynced. Existing files and direct
symlinks are refused before issuance; redirects and remote plaintext HTTP are
refused. Stdout reports public identity/credential metadata, never cookies, tokens
or passwords. Do not combine credential-file, bearer-token or owner-session inputs.
If credential persistence fails after issuance, inspect/revoke the reported
credential ID through owner access rather than silently issuing replacements.

## Agent scope

The onboarding grant has a fixed 24-hour lifetime, one Docker target/environment,
explicit actions and a maximum of 1–10 applications. Discovery exposes that target's
approved resource metadata; it does not adopt or change existing containers.
An empty action list permits discovery/creation but no lifecycle mutation. Allowed
initial actions are deploy, restart, configure and recover. Grant creation is
human-owner-session only; an agent cannot issue or approve its own authority.

Only applications **created by that credential** are readable/manageable through
this grant. Existing owner applications and apps created by other grants are
excluded even on the same target. Listing is filtered, target/environment pins are
checked, and membership is reconstructed from durable metadata on every request.
Creation is limited atomically under the credential lock; different concurrent
requests cannot exceed the cap.

```sh
bin/oap --url http://127.0.0.1:8878 \
  --credential-file .local/standalone/agent.json targets
bin/oap --url http://127.0.0.1:8878 \
  --credential-file .local/standalone/agent.json \
  --target docker-local target-capabilities
```

Set the example manifest's target to `docker-local` and use a preloaded compatible
image. For agent creation, supply a stable `--key`; identical normalized manifest
and key reuse the original application. Different content on the same key conflicts.
Creating or saving a definition does not deploy it.

```sh
bin/oap --url http://127.0.0.1:8878 \
  --credential-file .local/standalone/agent.json \
  --key create-example --file application.json create
```

Use M2.5 deploy/wait/verify with the returned app ID and current definition version.
A reviewed lane is also headless: agent `plan`, owner `get-plan` and exact-hash
`approve`, same agent `execute-plan`, then `wait` and verify. All plan inputs and
hash checks are the existing controller implementation; the CLI does not approve
implicitly. `approve` takes `{planHash}` and `execute-plan` takes `{}` on `--file -`.
Plans still require immutable registry digest references; current Docker cached
content IDs can use the existing ordinary deployment lane instead.

The grant excludes existing-resource adoption, database links, privileged security
exceptions, direct SQL/control database access, owner settings, grant issuance and
arbitrary host commands. An owner may issue a normal app-scoped credential through
`app-grant` for an existing app, including one whose onboarding grant expired.
That command takes the existing `/access/credentials` request and saves its token
privately. It does not transfer the original onboarding grant's membership.

`agent-control` / `set-agent-control` and `revoke --credential ID` are owner commands.
Pause/revocation deny agent reads and new work; already dispatched runtime effects
may continue. Workers recheck the durable credential before mutation. Creation
also rechecks expiry, revocation, account activity, pause, target and count in its
transaction. An API success does not prove current app behavior.

## External update and rescue

These commands run with separately authorized **host/Docker administration**.
An API token cannot authorize them. Host administration can bypass API protections,
including accessing platform metadata; do not grant it to an otherwise restricted
agent or claim that OAP can enforce its API rules over root/Docker access. Native
host operations have JSON receipts, not authenticated OAP mutation audit entries.

```sh
python3 scripts/install.py update --directory .local/standalone \
  --image "$COMPATIBLE_OAP_IMAGE_DIGEST"
python3 scripts/install.py recover --directory .local/standalone
```

Update/recover perform the candidate's read-only schema preflight before modifying
installed image configuration. Image changes are atomic/private and retain the
previous image reference. The controller is recreated; PostgreSQL/data volumes and
application containers are preserved. Command failure retains configuration and
recovery references; inspect them before another change. Recover defaults to the
current installed image, or takes an explicitly selected compatible image. Its
metadata database must be initialized and reachable; use `start` or the separate
metadata recovery procedure first if PostgreSQL is unavailable.

Unknown/changed applied migrations block downgrade. In particular schema 22
onboarding grants require a controller that understands them; an older image must
not interpret these credentials as broad access. Recover does not automatically
activate a previous image or restore data. Metadata restoration remains the
separate quiesced backup/isolated restore-check procedure; secret/key configuration
and application data have their own recovery requirements.

Installation commands use protocol version 1 and bounded subprocesses. An advisory
host lock releases on process death; it does not require deleting stale lock files.
`check` returns selected service state/health metadata and exit 4 when unhealthy.
New lifecycle commands and `--json` return structured, non-secret JSON; failed
Compose/provider output is withheld. A successful healthcheck is controller
readiness, not verification of application data or business behavior.

## Acceptance

`make test-local` runs a fresh shipping Compose install in the isolated Docker
Engine. `tests/env/headless.py` proves owner setup, private named-agent issuance,
filtered target discovery, idempotent creation, deployment and live HTTP behavior,
a distinct compatible image variant/controller update, API-offline external
rescue with unchanged native container IDs and boot times, owner pause/resume and revocation. It uses no browser or operator account
and writes `headless-acceptance.json` beside normal suite reports.

The owner-mode system suite proves exact headless plan review/execution, sibling
and owner-control denial, expiry, creation audit correlation and concurrent cap
admission. Unit checks cover private origin-bound files, refusal to overwrite,
no credential stdout, immutable candidate validation, and failed preflight
preservation. The retained browser/operator suites remain regression checks, not
new UI or adapter work. M2.6 advances only after the full exact-tree local gate.
