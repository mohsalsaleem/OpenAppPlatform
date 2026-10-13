# Feature reference

Detailed behavior and limits of features that exist today, including the retained
dashboard and Coolify adapter. The [README](../README.md) gives the overview and
the [roadmap](../ROADMAP.md) gives status and order.

## Current milestone

The supported single-server baseline is being reviewed for **v0.1**. See the
[release acceptance checklist and limits](v0.1-acceptance.md) and
[release candidate notes](releases/v0.1.md). This is a preview release
candidate, not a production-readiness or zero-downtime guarantee.

Implemented: Go API and controller, PostgreSQL persistence, River deployment
jobs, a React dashboard, target-scoped Coolify and direct Docker adapters, versioned configuration editing,
and a read-only MCP interface. Standard releases
support web and worker components and multiple instances, explicit Docker-image resource
adoption, frozen artifacts, idempotency, progress, live instance inspection,
per-instance bounded logs, explicit observation recovery, per-instance restart,
and Docker runtime variables, application DNS connections, and explicit instance retirement.

Verified locally with PostgreSQL and Chromium, and through a real deployment on
an isolated staging project in the owner's existing Coolify.

Bare Docker uses a local Unix socket and supports cached-image deployments without
a registry connection. Dokploy, Dokku, Portainer, scheduled jobs, embedded AI
diagnosis, rolling deployments, and blue-green remain planned. Native Coolify source tracking
and an optional signed immutable-image build lane are implemented and staged;
native source lifecycle handoff and artifact reuse are not.

## Agent interface

The [JSON CLI](agent-cli.md) exposes current lifecycle APIs, scoped capability
inspection and durable operation waiting. Build with `go build -o bin/oap ./cmd/oap`;
`bin/oap help` and `bin/oap schema` work offline. The [headless bootstrap](headless-bootstrap.md) slice adds private owner
setup/login, bounded standalone app-creation grants and external controller rescue.
Existing app-scoped tokens retain their boundaries; broader workspace access is
not granted.

## Examples

For an operator-free setup, follow [standalone Docker](bare-docker.md),
including the persistent-volume example.

See [examples](../examples/README.md) for a single-component app, a two-component
app, target configuration, and adoption. Creating a definition does not deploy.
Standard deployment can restart workloads and cause downtime. Instances are
tracked together; routing and balancing are not provided in this milestone.

```sh
python3 scripts/run-local.py python3 scripts/example.py examples/hello-web/application.json
```

[GitHub Actions release example](../examples/github-actions.yml) builds an image,
then queues its digest through the release API. The operator must be able to
pull that image. Existing independent deployment triggers must be disabled for
controller-owned releases. A 202 response is not deployment success.

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
silently ignored. See the [connected Docker example](../examples/README.md#connected-docker-components).

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

See [discovery and assembly](discovery-and-assembly.md) for API semantics,
Docker observation scope, compatibility, and verification. Examples that combine
new managed components with existing resources require handoff of supported
observed components before application-wide deployment.

## Owner setup and access

The platform now defaults to owner mode. Configure a private `OAP_SETUP_TOKEN`
(at least 24 characters) before first launch, then create the owner account in the
UI. Public signup is disabled; members join with owner-issued invitation codes.
Workspace access provides roles, application-scoped agent tokens, revocation and
audit. Existing resources remain in the installation's single workspace.

See [owner access](owner-access.md) for local HTTP configuration, session and
role behavior, agent scopes, legacy preview compatibility and password recovery.
`OAP_API_TOKEN` no longer grants platform access in owner mode.

## GitHub source activity

Use existing Coolify GitHub Apps for native source deployment. OAP can observe
signed push notifications and correlate exact commits with resource-scoped provider
history while keeping management observe-only. An optional trusted image-builder
command supports durable build requests and selected-component digest releases.
See [GitHub releases](github-releases.md) and [examples](../examples/github-sources).
The optional hosted image-builder lane passed a real GitHub push and redelivery.
Native source handoff/artifact reuse remains unsupported; CI stays disabled.
See the [operator-first audit and roadmap corrections](operator-first-audit.md).

## Application environments

Logical applications group existing staging/production environment records while
preserving resource IDs, native workflows, release histories and credential scopes.
Owners explicitly link or separate environments through the dashboard. Existing
flat application URLs/APIs stay compatible. Target configuration remains server-side.
See [application environments](application-environments.md).

## More references

The [target setup and definition review](target-setup-and-definition-review.md)
flow generates first-connection recipes, verifies configured scopes read-only,
connects additional existing environments through trusted operators, reuses naming
layouts and previews masked configuration changes. Native workflows remain intact.

The four-component example now groups frontend, API, worker and an existing
PostgreSQL dependency with explicit ownership. See [existing database links](existing-database-links.md)
and [the reproducible example](../examples/application-breadth/README.md).

The current standalone agent lifecycle work is documented in [Portable application lifecycle](portable-lifecycle.md), with versioned specifications and shared CLI/API/MCP action plans. See [ROADMAP](../ROADMAP.md) for acceptance status and execution order.

Agent diagnosis uses the [bounded incident view](incident-diagnosis.md), keeping runtime health, release outcome, freshness and external application verification distinct.

The [versioned standalone agent runbook](standalone-agent-runbook.md) and [operation skill](../skills/open-app-platform/SKILL.md) cover the combined headless journey. [Footprint measurements](footprint.md) separate OAP/PostgreSQL from workloads and retain the measured idle CPU gap.
