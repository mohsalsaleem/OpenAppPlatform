# System design

Open App Platform is a single-workspace application control plane over existing
operators and bare Docker. The supported runtime adapters are Coolify and Docker.
It is verified locally and in isolated staging; owner-grade production hardening
remains bounded by the documented acceptance limits. The
[roadmap](../../ROADMAP.md) defines the delivery order: standalone Docker and
agent-facing contracts first; UI and operator expansion are parked. This document
describes the existing architecture, not completion of the headless agent journey.

![OAP architecture: clients, one Go process, PostgreSQL, Docker Engine](../images/architecture.svg)

## Boundaries

| Package | Responsibility |
| --- | --- |
| `cmd/platform` | Composition, HTTP lifecycle and River workers |
| `internal/domain` | Operator-independent types and validation |
| `internal/operator` | Adapter ports, explicit capabilities and safe projections |
| `internal/operator/coolify`, `internal/operator/docker` | Provider transports and supported lifecycle operations |
| `internal/store` | Domain projection, PostgreSQL persistence and migrations |
| `internal/store/queries`, `internal/store/db` | Named SQL and sqlc-generated queries/types |
| `internal/controller` | Frozen releases, admission, jobs, authority and recovery |
| `internal/httpapi` | Authentication, authorization, transport and SSE |
| `web/src` | React dashboard using the generated OpenAPI client and shadcn primitives |
| `tests/system`, `tests/env` | PostgreSQL/provider contracts and isolated reproducible acceptance |

Domain has no provider, HTTP or database dependencies. Operators do not depend on
controller or store. The browser calls OAP rather than calling providers directly.
One Go process serves the compiled React dashboard, API and River workers.
PostgreSQL supplies state and the durable queue; Redis is not required.

## Operator ownership and source workflows

Connecting and grouping existing resources changes OAP metadata only. Native
builds, GitHub App/webhooks, routes, variables and storage remain operator-owned.
Native Coolify source components are observe-only: OAP observes exact-commit
activity, health and bounded logs, without starting another deployment.

An optional, explicitly configured managed-image lane verifies signed GitHub
notifications, freezes commits and digest artifacts, deduplicates deliveries, and
queues affected components. A separate builder/registry is not required to group
existing applications. Native source mutation and artifact reuse remain future
adapter contracts. Image-backed management handoff requires explicit scope,
definition/version checks and runtime inspection.

## Applications and releases

A logical application group contains environment records. Existing environment,
resource and release IDs and credential scopes survive organization. Each
environment has a manifest and a target; grouping does not create enterprise
multi-tenancy or move a workload between servers.

Releases freeze a complete definition, its version, selected component/instance
steps, readiness policy and optional source provenance. Image overrides are
immutable. An override changes the release artifact, not the saved definition.
A deployment without an override uses saved images rather than automatically
reusing the last release's overrides. The UI distinguishes saved and running images.

A short application transaction lock serializes admission and definition reads.
Admission records the release, its component claims, credential and River job in
one transaction. A retry with the same idempotency key and request returns the
original release even after the saved definition changes.

Active releases claim writes for selected components and reads for transitive
`dependsOn` and declared service connections. Independent components can execute
concurrently; overlapping writers and writes to a dependency being read are
rejected. Full-application releases claim all components. Restart and retirement
claim their selected component. Attention and unresolved abandonment retain their
claims until explicit recovery/reconciliation. Pre-upgrade active releases receive
conservative whole-manifest write claims. Unclaimed work created by an older
controller conservatively blocks new admissions. Only one controller version
should dispatch during an upgrade; mixed-version concurrent writers are unsupported.

Session advisory locks fence each release worker/recovery operation and disappear
when their PostgreSQL connection dies. Claims survive controller crashes. Definition
restoration, management handoff and rollback planning retain conservative
application-wide checks where they depend on a complete compatible snapshot.

## Execution and recovery

Steps prepare resources, persist dispatch intent, dispatch once, then observe the
exact provider operation. A lost dispatch response enters attention instead of
blindly repeating an uncertain operation. Native HTTP/image health configuration,
bounded readiness deadlines and dependency ordering are frozen with the release.
A provider's acceptance is distinct from verified completion.

Cancel before dispatch, owner abandonment, provider reconciliation, reviewed
image rollback and saved-configuration restoration are implemented. Saved
configuration restoration preserves current saved images and changes metadata
only; deployment needs a separate review. Data/schema restoration is a separate
manual compatibility boundary, not an automatic rollback guarantee.

Standard replacement may interrupt service and inherits the operator lifecycle.
Stable routing, graceful draining, rolling and optional blue-green remain queued
work. Docker app DNS and explicit Coolify service endpoints are available; Coolify
private-network mutation and retirement are not claimed. Workers, scheduled jobs
and stateful provisioning remain unsupported until their dedicated contracts pass.

## API, dashboard and streams

`docs/openapi.json` supplies the dashboard's generated types and typed
`openapi-fetch` operations. Generation is reproducible and checked locally.
The UI uses React/TypeScript/Vite, React Router, TanStack Query, Tailwind and
repository-owned shadcn/Radix primitives. Page composition remains local React
code; control behavior and styling come from the shared components.

Application/release updates and selected per-instance logs use SSE with cookie or
bearer authentication. Streams contain replaceable snapshots, bound memory/write
time, flush through proxies, and recheck live credentials before each sample.
Disconnects reconnect to current persisted state; a lost stream cannot lose a
release. Deployment polling is a fallback while disconnected. Provider instance
health and native source activity still use bounded polling, because those
adapters do not expose a portable event stream. Log snapshots contain the last
100 lines; this is not indexed historical search. VictoriaLogs remains planned.

## Security and operational boundaries

One default workspace has an owner, invited operator/viewer members, opaque
session cookies, and app-scoped, expiring/revocable agent credentials. Mutations
use CSRF/origin checks, current authorization, and durable audit intents. Workers
recheck admitted credentials before provider writes. Read streams also respect
revocation and scope. Additional tenants are explicitly unsupported.

Provider credentials are trusted server-side environment references, excluded
from manifests/API projections. HTTP transports require HTTPS except approved
local endpoints, refuse redirects and bound responses/errors/logs. Known tokens
are redacted; arbitrary application secrets are not guaranteed to be recognized.

Existing workloads continue when OAP is down. A single host remains a failure
boundary. Metadata backups and compatible restore verification are required for
staging upgrades. No OAP action implicitly deletes external resources or volumes.
The project is MIT licensed; third-party component notices are retained separately.
