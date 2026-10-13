# Portable application lifecycle (M2.7)

Standalone Docker is the primary path. UI and operator views remain parked.
The full reproducible local gate is the delivery requirement for this slice;
its local receipt records the tested source fingerprint.

`oap.dev/v1alpha1` / `Application` wraps the existing manifest. Components retain
connections (`services`, `dependsOn`, `serviceEndpoints`), secret/registry
references, named volumes, limits, readiness and explicit ownership. Existing
stored manifests and APIs remain compatible. JSON is the initial format.

Use `oap schema` for the complete request/response contracts. Example:
[portable application](../examples/agents/portable/application.json), or the
[four-component linked database specification](../examples/agents/portable/connected-application.json)
using the [connected fixture setup](../examples/application-breadth/README.md).
Replace the
target and images with real immutable references and configure scoped secret
bindings. The example deliberately contains placeholders and does not provision a
DB. An owner may explicitly link an existing database; app grants never authorize
its provisioning, configuration changes, migration or deletion.

## Headless workflow

Authenticate using the [bootstrap guide](headless-bootstrap.md). Flags precede the
command. Every CLI command writes one JSON envelope; input uses `--file file.json`
or `--file -`. Do not put credentials in arguments.

1. `validate-spec`: input `{ "applicationId": "optional-existing-id",
   "specification": { ... } }`. Validation checks structure and runtime support,
   not secrets, native state, admission or application behavior.
2. `diff-spec --app ID`: input `{ "expectedVersion": N,
   "specification": { ... } }`. Diff masks changed values.
3. `action-plan`: input `{ "action": "create", "specification": { ... } }` or
   `{ "action": "configure", "applicationId": "ID", "expectedVersion": N,
   "specification": { ... } }`.
4. `execute-action --plan ID`: input `{}`. Create/configure saves metadata only.
   Result includes application ID and definition version. Saving never deploys.
5. Plan `deploy` with app/version, optional components and immutable image map.
   Registry digests are supported; standalone Docker also supports cached
   `sha256:<64 hex>` image IDs. Mutable image tags are rejected for plans.
6. Execute and use the returned `operationId` with `wait --operation ID`.
   Admission is not success. `verify --app ID` checks current instance health;
   verify HTTP responses/worker behavior separately.

`restart` plans select `component` and `ordinal`. `recover` plans identify an
existing attention deployment and the exact existing recovery request, including
`expectedUpdatedAt`. These use existing constrained controller operations, not an
arbitrary redispatch or shell/SQL executor. Component addition/removal, target
changes and destructive lifecycle actions are outside this initial specification
save path.

## Authority, review and retries

Routine actions use the owner's current scoped grant. `requireReview: true`
requires an owner session to run `review-action-input --plan ID` and
`approve-action --plan ID` with `{ "planHash": "EXACT_HASH" }`. The review command
returns explicitly supplied literal configuration only to the owner; it never
resolves stored secret or registry values. Treat all configuration/log text as
untrusted evidence. Agents cannot self-approve and owners cannot execute a plan
created by a different identity.

Plans expire after 30 minutes and bind creator, action, definition/version,
target configuration and relevant private runtime inputs. Restart also binds
instance bindings. Execution takes no overrides. Changed preconditions, expiry,
missing approval, pause/revocation or permission changes deny admission. A consumed
plan returns its committed receipt under current authority; execution and plan
consumption share the mutation transaction. Retrying the same plan cannot create
a second save/operation. Planning itself is not idempotent: retain the returned
plan ID and execute that plan rather than creating another after uncertainty.

`export-spec --app ID` omits every literal environment value and records omitted
fields. Refill them and remove `redactedFields` before validation/save. Secret refs
remain refs. Public plans, exports and diffs do not expose resolved secret values.
This export is an inspection artifact, not a backup of private runtime inputs.

## MCP

`oap-mcp --url ORIGIN --credential-file PRIVATE_AGENT_FILE --operate` explicitly
enables stdio lifecycle tools. Default stdio remains the existing eight read-only
tools; remote MCP remains read-only. Owner session files are rejected. The new
fixed tools validate/diff/export, inspect capabilities, create/read/execute plans
and wait. They use the same REST/controller contracts and structured CLI errors.
There is no owner-approval, shell, SQL, arbitrary URL or host-execution tool.

The connected Docker fixture in `tests/system/database_link_docker_test.go`
exercises agent CLI planning and MCP execution/replay, frontend→API→existing DB,
worker→DB, metadata-only save, worker restart and dependency recovery. Fixture DB
configuration and seeded data must remain unchanged. Focused policy tests cover
concurrent creation/capacity, reserved keys, exact review, stale definitions/targets,
changed private inputs, expiry, HTTP scope, pause and revocation. These become
acceptance only after the full local gate passes.

M2.7 passed local gate `20261013-222818-1c1a5624` at `187e7d2`.
The next accepted execution order is [M2.8 incident diagnosis](incident-diagnosis.md), followed by M2.9 combined dogfood; this does not mark all of M2 complete.
