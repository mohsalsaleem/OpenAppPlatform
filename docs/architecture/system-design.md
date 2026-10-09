# System design

OpenAppPlatform is an application control plane. This milestone implements a
single-workspace API, dashboard, PostgreSQL persistence, durable standard
release execution, Coolify and Docker adapters, versioned configuration, and read-only MCP tools. It is intended for local evaluation
and isolated staging, not unattended production adoption.

## Boundaries and dependency direction

```text
cmd/platform          composition root and process lifecycle
internal/domain       operator-independent types and validation
internal/operator     adapter port, capabilities, safe resource projections
internal/operator/coolify
                      REST implementation; no controller or database dependency
internal/store        PostgreSQL persistence and ownership constraints
internal/controller   release snapshots, jobs, state transitions, adapter calls
internal/httpapi      authentication, transport validation, response projection
web/src               application-oriented dashboard
examples              runnable definitions and CI integration examples
tests/system          PostgreSQL, HTTP, durable jobs, and opt-in real operator tests
```

Domain has no database, HTTP, or provider imports. The operator interface imports
only domain. Controller orchestrates persistence and adapters; UI code never
calls Coolify directly. The direct Docker adapter implements the same port.

## Process and persistence

One Go process serves the built React UI, authenticated API, and River workers.
PostgreSQL stores application definitions, configured targets, frozen releases,
instance bindings, and River jobs. Operator credentials remain in environment
variables referenced by trusted target configuration. They are not stored in API
projections or application manifests.

One active release per application is enforced by a partial unique index.
Enqueue takes an application-scoped transaction lock, checks idempotency, and
inserts the deployment and River job in one transaction. Workers claim jobs
through River. A session advisory lock fences duplicate execution of the same
release. Worker and API can be separated later without changing the domain.

Explicit SQL uses pgx in this milestone. sqlc generation remains a planned
refinement once persistence queries stabilize. There is no separate Redis or
workflow service.

## Release semantics

Standard releases use a frozen manifest. The API can provide component image
overrides by immutable digest for CI releases without editing the application's
base definition. New idempotency hashes bind the declared release request and preserve retries
after definition edits. Legacy manifest hashes remain readable. A repeated key
with a different request is rejected.

A new release without overrides uses the application's base definition; it is
not an implicit rollback or redeployment of the latest artifact override. UI
configuration and release views must distinguish those versions.

Components run sequentially, with one independently managed Coolify resource
per instance. This groups instances but does not add service routing, balancing,
or zero-downtime behavior. Workers and scheduled jobs are rejected until their
lifecycle rules are implemented.

A release succeeds only after Coolify reports deployment completion and the
workload reports running or running:healthy. Running without a configured
health check means process availability, not application readiness. The live
system test additionally verifies HTTP externally; the core does not yet have
user-configurable HTTP readiness probes.

Provider failures retain per-instance outcomes and permit later components to
be observed. Ambiguous dispatch produces attention and halts automatic
sequencing. A failed deployment is not assumed to have preserved the old
container. Standard lifecycle availability remains operator-dependent.

## GitHub integration

The current executable path is GitHub Actions building and publishing an image,
then calling the authenticated release API with its digest and an idempotency
key. See examples/github-actions.yml. Repository-push webhook verification,
repository-to-component mapping, and operator source builds are future work.

For controller-owned releases, independent operator deployment triggers must be
disabled. This milestone adopts Docker-image applications only; it does not
change source integrations or production auto-deployment settings.

## Operator support

Coolify is implemented and tested against the owner's staging installation.
Discovery is scoped to a configured project and environment. Names alone do not
confer ownership: generated resources must have the expected ownership marker.
Adopted resources retain provider configuration and cannot have their artifact
changed through release overrides yet.

Bare Docker is implemented over a local Unix socket. It manages labeled
containers and application networks using cached images or an explicit public
pull policy. Dokploy, Dokku, and Portainer are also planned.
Unsupported strategies or operators return explicit errors.

## Security and operational limits

API authentication uses one configured access token with constant-time
comparison. It is suitable for a local single-owner preview; teams, roles, OIDC,
credential rotation, and production audit retention are future work. The UI
stores the platform token in sessionStorage for the current browser tab.

Provider connections require HTTPS except localhost. Redirects are not followed.
Responses and logs are bounded. Provider errors exclude upstream response bodies
because they may contain credentials. Logs redact known connection and platform
tokens but do not guarantee redaction of every application secret.

The controller does not delete external resources or volumes. Target files are
trusted server configuration, not arbitrary user-controlled endpoints. Target
URLs and resource names returned by the API are still operational metadata.

Existing workloads continue when the controller is unavailable. A single host
remains a failure boundary. PostgreSQL data needs backups before production use.
Local development uses a named database volume and loopback-only ports.
