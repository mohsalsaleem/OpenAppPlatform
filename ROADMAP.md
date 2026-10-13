# OpenAppPlatform Roadmap

OpenAppPlatform should be a self-hosted application platform that one person can
operate, manually or through authenticated agents. Its core must stay useful
without AI, external cloud services, or a collection of infrastructure services.

This roadmap captures priorities from 9 October 2026 and accepted updates through
13 October 2026. Phases are
sequenced by dependency and acceptance criteria, not delivery dates. Listed
integrations are candidates; they are not installed or enabled by this document.

## Product rules

- Lead with connecting an existing operator, discovering its resources and grouping
  them into logical applications. OAP adds application-level management while native
  builds, GitHub triggers, domains, volumes and secrets keep working.
- Prefer the operator's verified native build/execution capabilities. A separate
  builder, registry, webhook, router or agent is an explicit optional gap-filler,
  never a prerequisite for grouping existing applications.
- Declare authority per action (observe/build/deploy/restart/configure/route).
  Grouping does not transfer authority or disable native automatic deployments.
  One automatic release sequencer acts on each source event.

- Keep the default stack to one Go service, its dashboard, PostgreSQL, and the
  selected deployment runtime. Reuse an existing operator where possible.
- APIs are the primary automation contract. UI, CLI, MCP, skills, and optional
  model-assisted features use the same lifecycle operations and permissions.
- One owner and individually identifiable agents are the initial operating model.
  Do not introduce enterprise tenancy or a separate identity service by default.
- AI inference is optional. External agents can operate through APIs without OAP
  paying for or selecting a model on their behalf.
- Preserve existing applications, data, and operator ownership during discovery
  and adoption. Observing a resource does not authorize changing it.
- Prefer existing runtime mechanisms over implementing another scheduler,
  consensus system, queue, registry, or secret-management service.
- Make risky behavior explicit: remote exposure, irreversible data changes,
  privileged containers, paid provisioning, and automatic mutations.
- Every capability needs a clear failure mode, recovery path, and small operational
  footprint before it becomes part of the default installation. Define and measure
  that footprint under the lightweight requirement below.

## Cross-cutting requirement: define and preserve lightweight OAP

OAP is an incremental management layer over existing operators. “Lightweight” must
be defined by measured resource use and operational burden, not just a small
container image or a short dependency list. This requirement applies to every
phase; record its acceptance work with M1 visibility/dogfooding without changing
the current feature priority.

### Definition and accounting

- Keep the default deployment to one Go service with its bundled UI and PostgreSQL.
  No required sidecars, per-workload OAP agents, separate queue, identity service,
  model runtime, logging/search cluster, registry or build service.
- Reuse operator-owned builds, execution, routing, certificates, health information
  and logs. Add optional gap-fillers only when needed, with explicit ownership and
  a visible resource/maintenance cost. Standalone Docker follows the same rule;
  its optional ingress and other enabled services are accounted for separately.
- Measure the **incremental OAP footprint**: service CPU/RSS, database CPU/RSS and
  storage, connections, network/operator API calls, image size, startup/recovery
  time, and UI payload/latency. Report the existing operator/runtime and application
  workloads separately. Include dedicated PostgreSQL in the baseline; disclose
  whole-server cost and attributable overhead when sharing a database server.
- Lightweight also means a one-person installation, update, backup and recovery
  process, bounded storage growth, few credentials/configuration decisions, and
  useful core operation with AI and optional integrations disabled.

### Planned measurable budgets

Define and publish a reproducible reference profile before enforcing performance
budgets: a modest single-server host, 10 logical applications / 50 components,
explicitly listed operators and versions, and a fixed observation configuration.
Measure idle, one active dashboard, a deployment burst, target outage/recovery and
controller restart. Keep workload image builds on their existing operator lane;
report OAP-owned optional builds separately rather than hiding their cost.

Use provisional starting goals of at most **256 MiB OAP service RSS**, **512 MiB
combined service + dedicated PostgreSQL RSS at idle**, and **1% of one CPU core on
average at idle**. These are proposed targets, not claims about current behavior;
validate or revise them from measurements and publish the reason. Also establish
budgets for operator API request rate/concurrency, database/storage growth, UI
payload and response latency, and startup/recovery time from the reference runs.

Prefer cached observations, batching, visible-page subscriptions and bounded
polling/backoff. Bound discovery size, log reads, queries, retries and concurrency.
An idle or disconnected browser must not create unbounded operator traffic, and a
target outage must not create a retry storm. Optional AI, indexed logs, analytics,
telemetry and certificate management must not become hidden baseline costs.

### Acceptance and regression checks

Publish a repeatable benchmark command and resource report covering the declared
profile and each scenario. Verify that OAP outages do not stop existing operator
workloads or routes. Track regressions against the agreed budgets alongside local
system/E2E acceptance; document any deliberate exception and its owner-visible
tradeoff. Keep performance checks reproducible and local while automatic Git CI
remains disabled. Do not add a monitoring platform just to measure the footprint.

## Current foundation

Implemented and tested:

- Application and component definitions with target-scoped instance bindings.
- Coolify and direct Docker adapters; standard web-component releases.
- Durable PostgreSQL and River jobs, release snapshots, concurrency protection,
  idempotency, retries, deployment deadlines, and conservative uncertain dispatch.
- Versioned configuration editing, stale-plan preconditions, and checksummed SQL
  migrations.
- Dashboard, live instance health, per-instance bounded logs, OpenAPI, examples,
  and read-only stdio MCP tools.
- Recorded Docker/Coolify restart operations and observation recovery.
- Docker plain runtime variables, app-local DNS connections, verified scale-up,
  and confirmed scale-down with reserved retained bindings and reactivation.
- Local Docker lifecycle and replacement tests, database/race/browser tests, and
  isolated Coolify staging verification.

Owner setup/sign-in, workspace roles, app-scoped agent credentials, and baseline
audit are available. Full multi-tenant isolation, complete stale-state recovery, stateful workload
management, and automatic discovery/assembly are not complete.

## Delivery sequence

| Phase | Outcome | Main work |
| --- | --- | --- |
| M0 | Make everyday application workflows functional | Create, deploy, live health/logs, configuration updates, recovery, manual grouping and supported scaling on Docker and Coolify |
| M1 | Trust OAP with an owner's real applications | Auth, agent principals, audit, recovery, backups, secret references, basic security and observability, dogfooding |
| M2 | Operate safely through remote APIs and agents | Tunnels, remote MCP, action plans and approvals, manual overrides, AI switches, skills, notifications |
| M3 | Assemble existing applications and build from source | Operator discovery, grouping/adoption plans, optional AI assistance, stack detection, build adapters, image lifecycle, more operators |
| M4 | Understand application behavior | Unified monitoring/logs, per-app analytics, custom analytics sinks, optional telemetry, bounded diagnosis |
| M5 | Coordinate more than one server | Target capabilities, transport, taints, placement, fencing, routing, explicit recovery and storage constraints |
| M6 | Support additional execution models | Firecracker investigation, celld and actor workloads, optional Cloudflare Workers/Durable Objects adapter |

Security, recovery, and dogfooding continue in every phase. Basic monitoring and
backup visibility belong in M1; richer analytics can wait until M4.

## M0 Core workflow delivery queue

This is the active execution order. Complete the current adapter work, then take
one reviewable slice at a time. This queue implements the original application
platform design; it does not make every later roadmap integration a prerequisite.
Minimal owner auth/scoped credentials are implemented. Full M1 hardening and AI
assistance remain separate from these everyday workflow batches.

### Completed slice: Coolify runtime configuration and explicit connections

Controller-owned Coolify runtime variables now use recorded provider IDs, value
hashes, and pending intents. Add/update/removal, literal values, unrelated operator
variable preservation, and two-component webhook delivery passed live staging
verification on 10 October 2026. Preview copies remain operator-managed; adopted
configuration is unchanged. Known update/removal outcomes reconcile safely;
ambiguous variable creation without a provider identity remains owner-reviewed.

Connections use explicitly supplied `serviceEndpoints`. Application-scoped private
DNS and network mutation are not advertised by Coolify. Existing endpoints remain
operator/owner-managed. Docker supports both app-local DNS and explicit endpoints.
Guarded retirement is available for supported OAP-owned Coolify image instances;
observed native resources retain their existing authority boundaries. Local Docker, PostgreSQL, browser/mobile,
and MCP regression checks continue to pass.

### Completed slice: guided discovery and manual application assembly

Existing Coolify image/source services and explicitly scoped Docker containers
can be grouped through Select → Review → Create observed application. Target-scoped
reservations, health/logs, blocked lifecycle actions, and separate image-backed
Coolify handoff passed local system/browser verification on 10 October 2026.
Read-only live staging verification grouped two existing services without a release
or provider configuration mutation. Source-backed handoff remains deferred.
See [discovery and assembly](docs/discovery-and-assembly.md).

**Accepted priority change:** establish the minimal owner-auth/workspace foundation
before GitHub release ownership, as requested on 10 October 2026. The original M0
queue otherwise stays in order. Full M1 hardening remains separate.

**Completed priority slice:** owner setup/sign-in, invite-only roles,
single-workspace ownership, scoped/revocable app credentials, baseline mutation
audit, queued-operation authorization, and offline password recovery. Local
PostgreSQL, Docker, browser/MCP regressions and owner/mobile workflows passed on
10 October 2026. See [owner access](docs/owner-access.md).

**Completed slice:** M0 batch 2, GitHub-triggered release ownership.

**Completed slice:** M0 batch 3, application and environment organization.

Existing-service grouping leads onboarding. Logical application groups preserve
legacy environment/resource/release IDs and agent scopes. Owner-only versioned
link/unlink, environment navigation, trusted additional-environment registration,
server-assisted first-connection setup, sanitized component layout reuse, masked
configuration diffs and per-component workflow/capability presentation are delivered.
Local migration, concurrency, permission, Docker and browser checks passed.

The explicit staging image/source acceptance passed on 10 October 2026 through a
GET-only provider boundary and a disposable OAP database. Native source/trigger,
domain, variable and storage fingerprints stayed unchanged; health/logs remained
available; deploy/restart/source handoff were rejected with zero provider writes.
Real staging acceptance covers one existing environment. Cross-environment linking
is verified in local system/browser fixtures; no production resource was touched.
See [application environments](docs/application-environments.md) and
[target setup and acceptance](docs/target-setup-and-definition-review.md).

**Support requirement:** Standalone Docker Engine is a first-class execution
path. Core lifecycle, configuration, volumes, limits and recovery must work
without an operator account or credentials. Shared features require native
Docker acceptance coverage; operator adapters add their own capability checks.

**Configuration ownership:** Existing linked/adopted resources inherit their
native runtime configuration. OAP exposes grouping, health/logs and permitted
lifecycle actions; runtime edits stay in the operator (or Docker host). Keep
their Settings view minimal instead of duplicating native Docker configuration
forms. OAP-owned workloads retain controls required for direct execution.

**UI foundation:** Use the shared shadcn controls and field composition. Runtime
configuration for linked resources remains in the operator; direct configuration
uses consistent Button, Field, Select and Collapsible patterns. Prevent legacy
global label/control styles and hand-styled link buttons from returning.

**Current priority:** Deliver and verify the ordered M2 agent baseline. The v0.1 candidate passed exact local gate `20261013-182404-1a29268a` and staging at `275f026`. M1.4–M1.6 passed local and scoped staging acceptance on 13 October 2026; see [v0.1 acceptance](docs/v0.1-acceptance.md). M1.1–M1.3 passed local and scoped staging acceptance on 13 October 2026. The supported M0 exit
review is accepted; P0.1–P0.5 passed their local and scoped staging gates. See
[the exit review](docs/m0-exit-review.md). P0.4 passed the final local/staging acceptance gate. Standalone Docker uses scoped private pull references and Coolify retains its native server login. P0.1 runtime secret references,
P0.2 workers and P0.3 existing database links passed local and isolated Coolify
staging acceptance. The first four-component breadth milestone and repeatable installation are delivered.
Continue with the ordered M1 breadth queue below. The priority correction below is the authoritative execution order.
Batch 5 has an accepted, limited Coolify routing pilot; its remaining capabilities
are deferred and the batch is not complete. Batch 4 is accepted for supported
stateless managed-image workflows; see the [acceptance review](docs/recovery-acceptance.md).
Native observed workloads retain their existing action boundaries. Stateful
provisioning, automatic data restore, source authority transitions and the full M1
credential vault remain separate work.

### Priority correction: breadth before depth (11 October 2026)

The routing pilot received disproportionate implementation time while workers,
linked dependencies, sensitive configuration and repeatable installation remained
unfinished. Freeze its supported scope and follow the queue below. This section
supersedes older “next slice”, “active batch 5” and ordering statements elsewhere
in this document; appended acceptance entries remain historical evidence.

**Next tangible milestone:** one logical application containing a frontend, API,
worker and linked existing database, with coherent UI/API health, logs and supported
lifecycle actions. Native Coolify GitHub builds and deployments keep working.
A database link does not grant OAP authority to provision, redeploy or restore it.

| Priority | Scope and deliverable | Acceptance gate | Stop boundary |
| --- | --- | --- | --- |
| P0.1 — Runtime inputs | Minimal runtime secret references required by the example; resolve only at the authorized adapter boundary, mask UI/API/logs and omit secret values from definitions and exports. Select the smallest compatible mechanism, reuse operator-managed inputs for observed resources and document access/recovery. | Local tests prove access isolation, redaction, missing-reference failure before dispatch and preservation of native variables; an isolated staging example consumes a dedicated test credential. | No general vault, BYOK provisioning, rotation system or new secret service. |
| P0.2 — Worker workload | Add a long-running worker kind to the existing definition, controller and supported Docker/Coolify image execution paths. Define non-HTTP readiness and expose capability-aware lifecycle actions. | Deploy a web/API/worker example; inspect worker health/logs, restart and recover an interrupted operation without duplicate instances. Unit tests cover meaningful kind validation; system and browser tests cover the journey. | No scheduled jobs, new scheduler, autoscaling, draining or advanced rollout policy. |
| P0.3 — Existing dependencies and assembly | Link one existing database to the example, display connection and ownership information and group frontend/API/worker/database in one application. Use explicit endpoints and secret references; dependency health failures must be visible. | Reproducible local fixture and existing Coolify staging acceptance show API/worker connectivity, useful health/logs and unchanged database ownership/data/native triggers. Review the supported actions in the UI. | No database provisioning, migration, queue/storage adapter collection, automatic adoption or data restore. |
| P0.4 — Everyday configuration | Minimum private registry credential references, per-component CPU/memory limits, and explicit named-volume mounts for single-instance OAP-owned image workloads where the adapter supports them. Unsupported capabilities are explicit; existing native configuration is preserved. | One private-image fixture, missing/invalid credentials, supported limit updates and volume data surviving restart/replacement/controller recreation verified locally; scoped staging validation where credentials and native capabilities are available. | No registry service, image optimization/scanning platform, placement engine or broad resource-policy framework. No volume deletion, bind mounts, shared replica storage, storage migration, or database storage takeover. |
| P0.5 — Repeatable installation | Document and package owner bootstrap, standalone Docker socket access, optional operator connection and the complete example. Keep one Go service/UI plus PostgreSQL. Provide update, compatible rollback, external controller recovery and metadata backup/restore instructions. | A clean isolated install completes the owner walkthrough; reproducible E2E covers assembly, supported release, health/logs, restart and recovery. Run the same supported journey in staging and publish evidence and limits. | No production rollout, multi-server packaging matrix, new infrastructure services or automatic application-data restore. |

**P0.1 acceptance:** scoped runtime references, missing-grant rejection before
native mutation, private recovery fingerprints, changed-binding holds and restored
binding recovery, REST/SSE log masking and fail-closed historical logs are shipped.
The full local gate (14 browser tests plus native Docker/system/race checks) passed
on 11 October 2026. A dedicated Coolify staging image workload consumed its granted
input; native delivery, masked logs and reference-only API snapshots passed.
See [runtime secret references](docs/runtime-secret-references.md). General vaults,
rotation and build-time credentials remain deferred. Private pull credential references are the minimum P0.4 scope.

**P0.2 acceptance:** OAP-owned image workers run on direct Docker and Coolify,
with non-HTTP image readiness, health/logs, selected updates and per-instance
restart. The full local gate passed on 11 October 2026: 15 browser tests plus
native Docker, PostgreSQL and race checks, including lost-dispatch-response
recovery with the original worker process and one provider dispatch. Coolify
staging application `application-breadth-preview` contains frontend/API/worker,
all healthy; a connected frontend request and worker-only restart passed with
unchanged frontend/API process identities and no worker public URL. Coolify's
status briefly lagged its healthy native container; the bounded observation gate
completed normally. See [workers](docs/workers.md). The database link remains
P0.3; this acceptance does not claim the four-component milestone is complete.

**P0.3 acceptance:** frontend, API, worker and an existing PostgreSQL resource now
share one logical application. The owner-reviewed link is observe-only, versioned
and reserved atomically; no database lifecycle authority or credentials are imported.
The full local gate passed on 12 October 2026 with 16 browser tests plus native
Docker/PostgreSQL/race checks. Authenticated API/worker reads, blocked writes using
a restricted application role, visible database failure, a held API release and
recovery passed with native database configuration and seeded data preserved.
Coolify staging `application-breadth-preview` is 4/4 healthy: frontend-to-API SQL
reads, worker SQL reads, database logs and reference-only definitions/releases passed.
Only API/worker were released. The native PostgreSQL acceptance fixture was prepared
through the operator outside OAP; its configuration, storage and seeded data remained
unchanged through linking and consumer deployment. Normal use links an existing
resource; this is not a database-provisioning feature. Compose databases, other
engines, cross-target links and SQL execution remain outside this slice.
See [existing database links](docs/existing-database-links.md) and the
[complete example](examples/application-breadth/README.md).

**P0.4 delivery:** named volumes and CPU/memory limits have passed native Docker
and isolated Coolify acceptance. Private pull references now use exact target,
application, component and registry-host grants for standalone Docker, with
reference-only definitions and private recovery fingerprints. The reproducible
fixture verifies missing/invalid credentials, successful authenticated pull and
execution, redaction and changed-binding holds. Coolify retains its server login;
OAP credential injection is explicitly unsupported and linked configuration remains
native. No registry service, credential rotation or general vault was added.
Final acceptance requires the full browser/owner/system gate and deployed staging
capabilities plus the unchanged four-component journey. See
[private registry access](docs/private-registry-access.md).

P0.1–P0.3 form the first breadth deliverable. P0.4–P0.5 close the remaining
minimum M0 configuration/install gaps. Implement one priority at a time; advance
only a small prerequisite when required, recording its reason here. Do not expand
a completed acceptance gate into another round of hardening unless a reproducible
failure prevents the stated journey or violates an existing security/authority
boundary. Complete the local gate and an authorized staging walkthrough before
claiming delivery; record unsupported capabilities without quietly widening scope.

**P0.5 acceptance:** owner-mode Compose packaging, immutable image references,
container-visible Docker socket group configuration and operator-only setup are
implemented. Containerized local owner HTTP required a small prerequisite: an
explicit loopback-publication declaration derived from the Compose bind IP.
Public insecure-cookie bindings and container-wide preview remain rejected. Quiesced metadata archives restore only into new offline databases;
read-only image preflight and startup reject incompatible migration ledgers.
The fresh packaged owner/four-component journey, process recovery, compatible
image replacement/rollback and isolated metadata restore passed full local gate
`20261013-003113-a84adfa3`. Staging `73e288c` passed read-only schema preflight,
healthy update, owner access, scoped worker restart, health/logs and unchanged
application database configuration/data. This does not automate stale-backup reconciliation
or application-data restore. See [installation](docs/installation.md).

**Parked until this queue is delivered:** general domain creation, connection
draining, general bare-Docker routing, rolling/blue-green deployment, source-backed
management handoff, scheduled jobs, new operators, indexed monitoring, AI diagnosis,
generative UI, tunnels, multi-server coordination and alternate runtimes. Existing
routing tests and supported behavior remain maintained. Safety fixes are allowed;
new route features require an explicit reprioritization. Existing native-source
build/deploy observation remains useful while delegated lifecycle actions are
parked; the breadth milestone must not imply those actions exist.

After P0.5, review the M0 exit gate and remaining capability limits before moving
to the existing M1 trust/recovery/security work. Do not pull M2–M6 features forward
because a library or integration is readily available.

**Batch 4 progress:** optional per-component operator-health gates and bounded
observation deadlines are frozen with release definitions and honored by recovery.
Legacy definitions retain running/health acceptance and a 15-minute window.
Native HTTP health check configuration is delivered for OAP-owned image workloads on
Docker/Coolify. Reproducible native Docker endpoint success/failure/image-return,
controller drift/restart/recovery, browser deployment, and real Coolify HTTP/image-return
checks passed. A single reusable stateless staging fixture is retained; frozen probe
metadata passed backup/restore verification. See [native health checks](docs/native-health-checks.md). Explicit dependency ordering now gates preparation/dispatch, verifies all selected
replicas first, and inspects unselected bound dependencies without redeploying them.
Pre-dispatch dependency holds can resume without repeating an uncertain dispatch.
Read-only frozen release/artifact comparison now includes masked runtime-key diffs
and app-scope checks; comparison itself does not approve or execute rollback. A separate, reviewed image-only
rollback path is implemented for compatible verified snapshots and existing OAP-owned
instances; native Docker and two-replica Coolify rollback/return, duplicate request,
review/acknowledgement and metadata restore checks passed. Cancellation before dispatch and owner abandonment/reconciliation are implemented;
local cancellation, restart, fence, provider reconciliation, concurrency, permissions
and browser checks passed. The controller is healthy in staging; completed-release controls
reject with 409 and the hosted review UI passed without changing stored release state.
Configuration recovery now has a reviewed, version/hash-checked saved-definition
restore path from a fully verified release. It preserves current saved images,
requires unchanged component identities/replica counts, blocks active/uncertain work,
and keeps deployment separate. Native data/schema and volume recovery procedures
subsequently passed reproducible drills; automatic stateful recovery remains
unsupported. See [release controls](docs/release-controls.md),
[controlled image rollback](docs/image-rollback.md) and the final
[batch 4 acceptance review](docs/recovery-acceptance.md).
Readiness and dependency slices passed local/native Docker and staging UI verification;
comparison passed local snapshot/scope/masking and staging UI/API verification. Native HTTP
probes were subsequently provisioned on the separate health-check fixture. The later image-only rollback test deliberately redeployed the
dedicated stateless fixture and returned both replicas to their original digest.
See [release readiness](docs/release-readiness.md) and
[release comparison](docs/release-comparison.md).

**Accepted operator-first correction:** primary onboarding is configure/connect
operator → discover → select/review → group → manage supported capabilities. Native
Coolify build/deploy and GitHub App workflows remain operator-owned by default.
Existing observe-only boundaries stay enforced until explicit native action
capabilities and authority transitions exist. The hosted custom-builder fixture is
optional fallback evidence, not the required Coolify architecture.
See [implementation audit and gaps](docs/operator-first-audit.md).

Batch 3 includes an existing-resource entry flow, visible per-component workflow
ownership/capabilities and environment organization around existing native resources.
First connections use a guided server-file recipe; additional environment scopes
inherit a trusted connection. Do not silently create another build system during
onboarding. Later batches add native delegated actions with scope/drift checks.

**Batch 2 progress:** signed/deduplicated GitHub intake, durable source requests,
selected-component immutable-image release plumbing, explicit uncertain-build
recovery, and read-only existing Coolify GitHub App tracking are implemented.
Native GitHub App source build/exact-commit/healthy runtime observation passed
on Coolify 4.4.6 using the existing personal GitHub App. Signed push intake through the real SSH builder/registry and two healthy Coolify
replicas passed isolated staging verification. Duplicate delivery and saved-build
receipt reuse passed. The first payload was test-signed. The hosted staging builder and dedicated branch repository hook subsequently
passed a real GitHub push, redelivery, two healthy replicas, builder/controller
restart and database restore checks. Existing Coolify App triggers stay unchanged. See
[GitHub releases](docs/github-releases.md). Batch 3 acceptance is complete; native source
management handoff remains unsupported.

### UI audit remediation

The 11 October 2026 UI review is a core usability correction before the next
ordered roadmap slice. Navigation separates Applications from Infrastructure /
Deployment Targets, with Workspace Settings beside the workspace identity.
Use Title Case for navigation, headings, labels and standalone actions; retain
sentence case for explanatory prose and errors. Application health and runtime
come before management guidance; detailed ownership rules and environment
organization live in application Settings. Full identifiers and raw requests
belong in expandable details. Deployment reviews expose running versus proposed
images; saved configuration must not be presented as current runtime.

The remediation includes owner-session log redaction, accurate linked GitHub
release outcomes and rollback labels, effective HTTP readiness gates, retained
settings drafts, keyboard tabs, grouping search, field-specific validation,
contrast tokens, and mobile navigation/wrapping. The reproducible local gate
includes the dedicated UI regression suite alongside native Docker, owner-session
logging, scoped access and recovery tests. Deployment topology remains planned
M4 work; this correction does not advance or reorder roadmap features.

### Original M0 batches (priority correction above governs execution)

| Order | Deliverable | Acceptance evidence |
| --- | --- | --- |
| 1 | Guided discovery and manual application assembly | Inspect an existing Coolify environment, select supported resources, preview ownership/configuration mappings, group them under one application, and inspect health/logs without a deployment or provider mutation. Explicit management handoff is separate; source-backed resources stay observe-only until their build/release contract exists. |
| 2 | GitHub activity and explicit release ownership | Repository/branch-to-component mapping, signed event verification, delivery deduplication, exact commit recording, and affected-component releases. Preserve existing operator-owned source builds and observe the requested commit; use an explicitly selected image-builder lane only for OAP-managed workflows. Digest reuse across replicas is verified for that lane, not presumed for native source builds. Test push, duplicate delivery, failure, and recovery locally before a staging handoff. |
| 3 | Application and environment organization | Existing-operator onboarding and grouping first; one logical application with staging/production mappings and visible native workflow ownership/capabilities. Preserve existing application/resource/release IDs, triggers, routes, volumes and secret references through a tested compatibility migration. No custom builder/registry is required for grouping. Reusable definitions and readable diffs reduce repeated setup. |
| 4 | Release verification and recovery controls | Configurable readiness probes, dependency/start ordering, deploy only selected affected components, release/artifact comparison, explicit rollback to a compatible known-good runtime snapshot, and owner-visible cancellation/abandon/reconciliation paths. Exercise process crashes and stale workers without duplicate dispatches or database edits. Rollback does not rewind application data. |
| 5 | Stable endpoints, domains, and replica routing | Reuse operator proxies where supported; add a small routing adapter only where necessary. Keep endpoints stable through replacement and scaling, verify domain/TLS setup, remove stopped backends, and define health-aware routing and draining. Surviving workloads/routes continue if the controller is down. Docker DNS alone is not the completion gate. |
| 6 | First useful workload and configuration breadth | Worker lifecycle, linked existing databases/queues/storage, minimal runtime secret references, private registry access, and configurable resource limits. Add a single execution path at a time with real examples. Scheduled jobs require a durable schedule identity and proof that restart does not create duplicate active schedulers. Stateful provisioning waits for backup/restore and storage guarantees. |
| 7 | Reproducible installation and staging dogfooding | Package/bootstrap one Go service, UI, and PostgreSQL; run OAP through a supported operator in staging. Demonstrate update, compatible rollback, controller recovery from outside OAP, and metadata backup/restore. MIT licensing is established; retain third-party notices. |

### Planned domain feature: automatic Let’s Encrypt certificates

Add opt-in automatic TLS certificate issuance and renewal through Let’s Encrypt
(ACME) to the deferred stable-endpoint/domain work. Provide an explicit automatic
SSL enable/disable control per supported domain or endpoint, based on the target’s
capabilities and who owns its ingress.

- **Operator-managed ingress:** inherit and show the operator’s certificate policy.
  Enable/disable through an adapter only when the operator exposes a supported
  control and the owner explicitly authorizes OAP to manage it; otherwise link to
  the native operator control. Avoid a second certificate issuer for the same domain.
- **Standalone Docker:** offer an optional ACME-capable ingress/proxy integration
  with persistent certificate/account storage and automatic renewal. Automatic SSL
  can be disabled so owners can use supplied certificates or externally terminated
  TLS. Keep HTTP exposure and redirects separate, explicit choices.
- **Disable behavior:** stop OAP-managed issuance/renewal without deleting existing
  certificates or account keys. Explain certificate expiry and require a reviewed
  transition before removing HTTPS or switching certificate ownership.
- **Acceptance:** verify domain ownership/challenge prerequisites, issue using the
  ACME staging environment in reproducible tests, exercise renewal, failure,
  controller/proxy restart and enable/disable transitions, and show redacted
  certificate status, expiry and renewal diagnostics. Keep private keys and any
  DNS challenge credentials out of definitions, audit output and logs.

This remains planned with the deferred routing/domain work; it does not change the
current M1 breadth queue or enable certificate management automatically on existing
operator resources.

Minimum secret references and registry credentials must precede any batch that
needs private artifacts or sensitive runtime inputs. They are not permission to
store secrets in plain manifests. M1 retains the full encrypted store, BYOK,
rotation, owner authentication, agent scopes, and audit work. OAP metadata backup
and an external recovery command are prerequisites to the dogfooding update drill.

Dependencies can advance a small prerequisite, not an entire later phase. For
example, readiness and a routing contract must precede a claimed rolling rollout;
workers must not inherit HTTP deployment assumptions. Keep examples and system
integration tests paired with each slice. Run `make test-local` before pushing,
use only isolated/authorized staging fixtures for live verification, and leave
GitHub CI disabled until the owner changes that instruction. GitHub release
webhooks are product functionality; they do not require enabling repository CI.

### M0 completion and what follows

**Exit gate:** create or assemble an application, configure its supported components
and environment, receive a GitHub release event, build/select the exact artifact,
deploy, reach stable supported endpoints, inspect current health/logs, restart and
scale where supported, and recover a failed update through the UI/API. Run this
journey on Docker and the existing Coolify staging installation, with explicit
capability limits and repeatable installation/examples.

Then proceed to M1 owner-grade operation: production auth/scopes and audit,
stronger crash reconciliation, backups/restore, encrypted/BYOK secrets, container
and image policies, basic monitoring/notification delivery, and production-readiness
dogfooding. The M0 preview token is not a production access model.

M2 follows for remote access, safe agent mutations, guardrails (including database
access), skills, BYOK inference, and ChatGPT/MCP access. Evaluate a read-only OpenUI
workspace after the core and permission boundaries are ready; it remains optional
and existing UI/API operation must work with AI disabled.

M3 extends deterministic discovery/builds with optional AI assembly and stack
optimization, and adds operator adapters incrementally. M4 brings richer monitoring,
per-app/custom analytics, and separately opt-in feedback, diagnostics, and telemetry.
M5 adds multi-server placement/transport and taints/annotations. M6 runtime research
covers Firecracker, celld, and optional Cloudflare Durable Objects independently.

Rolling and blue-green remain selectable later strategies. Neither is enforced.
Implement them only after readiness, stable routing, and rollback/data-compatibility
contracts are verified. Tunnels, enterprise identity services, a new scheduler,
and external analytics/model services are not dependencies of the M0 stack.

## M1 Owner grade operation

### Ordered M1 breadth queue (13 October 2026)

The [supported M0 exit review](docs/m0-exit-review.md) is accepted. This queue is
the next authoritative execution order; keep the existing one-person stack and
native operator ownership. Each slice produces a reviewable UI/API result, a
reproducible local gate and authorized staging evidence. Do not deepen one slice
into a separate platform before covering this queue.

| Priority | Deliverable | Acceptance gate | Stop boundary |
| --- | --- | --- | --- |
| M1.1 — Auth/Scopes and Mutation Audit | Extend the existing owner/agent foundation with explicit action, target and environment boundaries; correlate audit intent with durable operation ID and observed outcome. Preserve existing identity/release IDs and keep values redacted. | Owner walkthrough issues a restricted credential; wrong action/target/environment is denied, revocation/expiry holds queued work before dispatch, and the resulting operation is traceable in audit. Local migration/API/browser and scoped staging checks pass. | No identity service, enterprise tenancy, OIDC/MFA rollout or remote-agent approval framework. |
| M1.2 — Recovery and Target State | Expose a small consistent classification of unreachable/stale/unknown/failed state and safe recovery actions. Reuse the journal, generation checks and uncertain-dispatch holds already delivered. | Target outage and controller restart show useful state, bounded retries/deadlines and an owner recovery path without duplicate dispatch or SQL edits. | No automatic reconciliation of arbitrary old backups, new scheduler or multi-server consensus. |
| M1.3 — Metadata Backup Visibility | Record owner-visible metadata backup/restore receipts and failures, key/config dependencies and basic retention guidance. Reuse the quiesced external helper. | A verified archive/isolated restore and an injected failure are distinguishable in UI/API; metadata versus application-data coverage is explicit. | No automatic workload backup/restore, paid backup service, host storage deletion or broad retention engine. |
| M1.4 — Recoverable Secret Store | Add the minimum encrypted local provider alongside current environment references, with owner-supplied/generated key and separate recovery/export instructions. | Values stay out of definitions/audit/logs; exact grants, missing key/value, restart and isolated restore are tested before live use. | No external vault provisioning, provider collection or automatic key/credential rotation. |
| M1.5 — Container and Image Baseline | Capability-aware policies for owned workloads and explicit auditable exceptions; show resolved artifact/architecture/provenance and protect active/rollback/retained images. | A supported workload runs under policy, an unsafe/incompatible request is rejected before mutation, and native/inherited configuration is preserved. | No scanning platform, arbitrary image pruning or alternate runtime. |
| M1.6 — Basic Visibility and Dogfooding | Controller/target/release/backup visibility, deterministic release/change context and notification event outbox. AI remains optional and no notification is sent without a configured destination. | An isolated failure is explainable from scoped context; a staging update/recovery drill has a usable external path and traceable outcomes. | No indexed logging/analytics stack, model inference, tunnels or notification-provider expansion. |

M1.1 now includes transactional operation/source audit correlation plus explicit agent action allowlists and target/environment pins. Legacy identity/release IDs and permissions remain intact. Its acceptance checks cover narrow issuance, wrong action/pin denial, queued expiry/revocation/permission holds, GitHub build authority and exact-operation audit controls. Local migration/API/browser and scoped Coolify staging acceptance passed. The temporary restart-only acceptance credential was revoked; legacy credentials and existing database configuration/data were preserved.

M1.2 now records bounded target observations, classifies unknown/connected/unreachable/failed/stale evidence, and links held operations to existing recovery controls. Local outage/restart/browser and scoped staging acceptance passed; rechecking does not redispatch work. M1.3 owner-only bounded host reports, private staging archive/isolated restore, checksum failure and workload preservation passed local/staging acceptance. M1.4–M1.6 minimal baseline passed full local gate `20261013-174758-e3e11828` and scoped staging acceptance at `0f02a83`: optional encrypted host-file values with exact grants and recoverable keys; capability-aware owned-Docker no-new-privileges and human-owner exceptions while inheriting operator settings; bounded release/change diagnostics and a local durable notification outbox. Richer architecture/provenance policy and measured lightweight budgets remain open; no pruning API or performance guarantee is claimed. Scanning/signing, external vaults, notification delivery and AI diagnosis remain beyond this minimal slice. Review the M1 exit gate after these slices; do not pull M2–M6
work forward. General routing and advanced rollout strategies stay parked under
the accepted breadth-first correction.

### Authentication and agent identity

The minimal owner/session and scoped-agent foundation is now implemented. Extend
owner authentication and separately issued
agent/service credentials. Support revocation, expiry, and scopes for applications,
environments, targets, and actions. Store credential verifiers rather than raw
bearer tokens. A secure browser session must not expose operator credentials.

Start with a small owner setup and scoped tokens. Existing OIDC integration can be
optional; standing up a dedicated identity platform is not a default requirement.

Audit every mutation with the authenticated subject, credential identity, source,
operation ID, reviewed plan, resource references, and observed outcome. Agent names
come from issued credentials, not text supplied by a model. Never log secret values.
Audit append-only behavior through OAP does not imply protection against a database
administrator altering records.

### Crash recovery and stale states

Add a recorded operation journal and adapter-specific reconciliation. Recover
queued and observing work after restart; distinguish stopped, deleted, unhealthy,
unreachable, stale, and unknown resources. An unknown result is not success or a
reason to blindly repeat a mutation.

Use generation checks and execution ownership to fence stale workers. Reconcile
uncertain dispatch only with provider evidence tying the operation to the intended
resource and release. Otherwise keep it paused for owner review.

Classify retryable, permanent, and uncertain failures. Specify per-stage deadlines,
bounded backoff with jitter, retry budgets, cancellation behavior, and circuit
breaking for unavailable targets. A timeout cannot be assumed to have cancelled an
already dispatched provider operation.

Provide inspect, resume observation, retry a proven safe step, acknowledge drift,
and abandon/reconcile workflows with recorded reasons. An owner can resolve stale
state without editing database tables directly.

### Backups and secrets

Back up OAP configuration, database state, bindings, release history, and recovery
metadata. Application databases and volumes have separate backup policies; an OAP
metadata backup is not an application-data backup. Record retention, encryption,
backup failures, and restore results. Test restoration into a fresh installation.

Keep secret references in manifests and return redacted metadata through APIs.
Provide a small encrypted local store, environment references, and an external
secret-provider interface. BYOK means the owner can supply the encryption or
provider key. When absent, local setup may generate the necessary key securely
and persist it separately from the database with restrictive access.

Automatic creation must include a recovery/export path. Losing the only key must
not be hidden behind a successful database backup. External secret stores or paid
resources are provisioned only through an approved setup plan. Rotate keys and
credentials without exposing values in logs, model context, or audit payloads.

### Container and image minimums

Make runtime limits configurable. Add policies for non-root execution, capabilities,
privileged mode, writable mounts, Docker socket exposure, network access, and
resource budgets. Compatibility exceptions are explicit and auditable.

Track image references, resolved artifact identities, architecture, registry access,
and provenance. Never prune an active, pinned, rollback, or retained artifact
implicitly. Start with existing registries and a small cache/retention policy;
private pulling and scanning can be adapter capabilities rather than new services.

### Dogfooding and basic visibility

Run OAP through OAP in a staging installation first. Verify that a failed update
leaves a usable recovery path outside OAP itself. Keep a last-known-good artifact,
a backup, and a direct operator/bootstrap command for recovery.

Show controller health, target reachability, stale observations, failures, recent
logs, and backup status. Add the deterministic release/change diagnostic projection
from [AI diagnosis context](#release-and-change-context-for-ai-diagnosis), without
requiring inference or an indexed logging backend. Include collector/backend health, ingest lag, collection
gaps and disk/retention status when optional indexed logging is configured.
See [lightweight logging and search](docs/logging-and-search.md).
Add notification events through a transactional outbox.
No email/chat notification is sent until the owner configures that destination.

**Exit gate:** restore OAP on a fresh server, recover an interrupted release without
uncontrolled duplication, revoke an agent credential, and explain every mutation
in the audit trail. Then pass a real staging update and rollback/recovery drill.

## M2 Safe remote and agent operation

### Ordered M2 baseline queue (13 October 2026)

Continue breadth-first from the verified v0.1 candidate. Keep deeper integrations
separate from the single-server agent baseline.

| Order | Deliverable | Gate and stop boundary |
| --- | --- | --- |
| M2.1 | Scoped diagnostic API/MCP inspection with missing/freshness evidence | Cross-app/pin/redaction and inert prompt-injection tests; no model calls or indexed log backend |
| M2.2 | Exact immutable deployment plans, human-owner approval and idempotent execution | Stale/version/hash/expiry/creator checks and traceable operation; no arbitrary/destructive executor |
| M2.3 | Persistent owner pause and scoped agent revocation | Deny agent reads/new dispatches, preserve owner recovery and disclose in-flight effects; no control DB access |
| M2.4 | Opt-in authenticated remote inspection and versioned runbook | Named agent inspects and executes one authorized REST plan on staging; existing HTTPS ingress, no mandatory tunnel or identity service |

M2.1–M2.4 minimal baseline passed full local gate `20261013-191145-e411542d`
and scoped Coolify staging acceptance at `c0b0360`. A named temporary agent
inspected scoped context and completed one owner-approved worker digest release;
identical retries reused the operation, cross-app/self-approval/mutation denials
passed, and both acceptance credentials were revoked. Four components stayed
healthy and linked database configuration/data remained unchanged. The owner UI
also distinguishes plan submission from the linked operation outcome. See
[M2 baseline and limits](docs/m2-agent-operation.md). OAuth/ChatGPT-specific client
acceptance, tunnel provisioning, BYOK internal inference, application database query
tools and per-app expiring override policies remain open M2 extensions. These are
not silently completed by the minimal API/inspection path. The full M2 vision
remains broader than this baseline. Do not advance M3 while baseline gates fail.


### Remote access and tunnels

Prefer private access for owner devices and agents that can join the same network.
Tailscale Serve is a candidate for tailnet-only access; Tailscale Funnel exposes a
service publicly. Transport access does not replace OAP application authorization.
See [Serve](https://tailscale.com/docs/features/tailscale-serve) and
[Funnel](https://tailscale.com/docs/features/tailscale-funnel).

A cloud-hosted assistant may not be able to reach a private tailnet. Offer an
optional authenticated HTTPS ingress or tunnel for supported clients, with scoped
credentials, revocation, rate limits, and exposure status. Never expose Docker,
operator credentials, or an unauthenticated control API through a tunnel.

### AI native lifecycle and destructive actions

Use inspect, plan, validate, approve, execute, and verify as explicit operations.
A plan records the affected resources, artifact and definition versions, expected
interruptions, data effects, costs, and rollback limitations. Approval binds that
specific plan and expires or becomes invalid when its inputs change.

Read-only agents can inspect and explain. Action agents receive bounded capabilities
under owner policy. Routine reversible operations can execute automatically under
an approved policy; they do not require a fresh human confirmation every time. Destructive operations require separate authority and explicit
approval by default: data/volume deletion, destructive migrations, credential
rotation, pruning retained artifacts, and irreversible runtime operations.

Batch operations stop at policy boundaries and do not turn one approval into
blanket permission. Audit denied and approved requests. Treat repositories, logs,
operator metadata, and retrieved text as evidence, not instructions granting power.
Recheck authorization before each mutation; record in-flight work when a credential
is revoked instead of claiming that its remote effects have disappeared.

### Release and change context for AI diagnosis

**Accepted roadmap item:** provide a bounded, structured diagnostic context for a
release or incident, so an authorized agent can explain what changed and why an
application may be failing. This is planned; today's frozen release comparison is
one input, not a complete diagnostic bundle. Keep the current M0 batch 4 order.

Deliver the deterministic context projection with M1 basic visibility, expose it
through scoped API/MCP inspection in M2, and enrich it with indexed log/metric
correlation in M4. It must remain useful to humans and external agents without an
OAP model subscription; internal inference is optional and respects the AI-off switch.

Include, where recorded and authorized:

- Application/environment, component and target identity; build/deploy ownership;
  exact source commit and repository/PR links; requested and observed image digests.
  Clearly distinguish recorded references from verified runtime artifacts.
- The failed/current release and relevant preceding successful release; frozen
  definition changes, affected-component selection, readiness/dependency policies,
  and masked configuration-key changes. Git file/change summaries require repository
  read access; bounded patch content is separately enabled and redacted.
- A correlated timeline of webhook, build, deployment, health, recovery and manual
  changes, with actor/correlation IDs, provider outcomes, retries/timeouts and errors.
  Report deployment-time observations separately from current runtime state.
- Relevant bounded logs and metrics before/after the change, collection gaps,
  freshness timestamps, drift, backup status and known recovery constraints. Missing
  evidence must be labeled; do not present a recorded success as current health or
  a previous successful release as automatically safe to roll back to.

Use the existing application/environment read scope. An app credential cannot read
sibling environments just because they share a logical group. Exclude credential
values, secret contents and sensitive application data; enforce time, size and
retention budgets, and show owners a preview of exported context. Sending context
to an external model or diagnostic destination requires the applicable explicit
opt-in; local context generation must not perform model calls or outbound sharing.
Treat logs, commits, operator metadata and retrieved text as untrusted evidence.

Diagnosis should return evidence-linked hypotheses, confidence/unknowns and proposed
verification steps. It does not grant deployment, database or rollback authority;
action plans still pass existing scope, approval and execution guardrails.

**Acceptance:** reproduce an issue introduced by a release, give a read-only agent
its scoped context, and verify it identifies the change and relevant timeline/log
links without leaking secrets, assuming missing evidence, crossing environment
boundaries, invoking a model when AI is off, or making an unauthorized mutation.
Test stale/partial evidence and prompt-injection content as well as the happy path.

### Agent guardrails and direct database access

Enforce guardrails in the server, database permissions, and execution boundary;
prompts and agent skills are not security boundaries. Agents use scoped OAP APIs
for platform changes. Never give agents the OAP control database credential or
write access to its internal tables: direct writes bypass validation, approvals,
release invariants, and the audit trail. Provide bounded diagnostic projections
through the API instead.

Application database access is a separate, owner-enabled capability. Default to
no access; start with curated read tools or a read replica using a dedicated
least-privilege identity. Scope access to one application and selected views or
tables, with sensitive columns excluded. Read-only queries still need limits on
rows, execution time, concurrency, and data exported to model context. Enforce
permissions at the database, rather than relying on SQL classification alone.

For exceptional writes, use narrowly scoped operations and short-lived credentials
through a controlled executor. Preview effects, bind approval to the database,
operation, parameters, and schema version, then recheck preconditions before
execution. Require explicit authority for schema changes, bulk updates/deletes,
role changes, and database drops. Verify an appropriate backup and recovery path
before destructive changes; a transaction cannot undo every external effect.
Retries require idempotency or verification of the previous outcome.

Record agent identity, grant, approval, affected database/resources, execution
outcome, and correlation IDs without recording credentials or sensitive query
results. Support revocation, query cancellation where supported, and an emergency
stop for new agent work. Mark unsupported enforcement capabilities clearly.
If an owner independently supplies an agent with privileged database credentials,
OAP cannot enforce its approval rules on that access; surface this limitation.

Verify denial of cross-application access, internal-table writes, privilege
escalation, expired grants, excessive exports, and prompt-injection attempts.
Include tests for stale approvals, partial writes, and interrupted execution.

### Manual overrides, switches, and skills

Support managed, observe-only, and paused automation modes. Manual changes produce
visible drift rather than being immediately overwritten. Overrides have an owner,
reason, scope, and optional expiry; returning to management requires reconciliation.

Separate OAP model inference from agent access. An AI-off setting disables internal
model calls and AI-assisted features while keeping UI/API/CLI operation available.
Agent credentials and automation can also be disabled or revoked independently.
The owner can turn off both. These switches are enforced server-side.

Publish versioned skills/runbooks for discovery, planning, deployment, verification,
recovery, and backup restoration. Skills describe workflows and expected evidence;
they do not grant privileges or override policy. Keep agent tooling compatible
with OAP API versions.

### BYOK and ChatGPT integration

Optional OAP inference uses owner-supplied model/provider keys, explicit budgets,
context controls, and usage reporting. The default platform does not require a
model subscription. Keys are stored as secret references and never sent to agents
as tool results.

Extend stdio MCP with an authenticated remote transport and scoped OAuth/token
support appropriate to each client. Target ChatGPT's current MCP/app integration
rather than committing to the older plugin format. Start with inspection and
planning; enable mutations only after policy, approvals, and audit are ready.
[ChatGPT MCP integration](https://help.openai.com/en/articles/12584461-developer-mode-and-mcp-apps-in-chatgpt).

**Exit gate:** a named remote agent can inspect and perform one authorized deployment;
a revoked or out-of-scope agent cannot. A destructive request needs an exact valid
approval, and disabling AI produces no OAP model calls. Database tools enforce
application boundaries and query budgets; agents cannot mutate OAP's control
database or obtain elevated access through retrieved instructions.

## M3 Discovery, assembly, builds, and image lifecycle

### Discover and assemble existing applications

Read resources from configured operators and normalize stable IDs, ownership,
repository/image references, domains, ports, network relationships, dependencies,
and configuration-key names. Secret values are excluded from grouping context.

Use deterministic evidence first: Compose membership, operator projects,
repositories, network links, explicit labels, and dependencies. Optional AI can
suggest groups or clarify ambiguous relationships with confidence and evidence.
A low-confidence guess remains a proposal.

Allow policy-approved high-confidence grouping and observe-only application
assembly automatically. Keep ambiguous groups reviewable. Create versioned
application-assembly plans that show component boundaries, shared services,
adoption bindings, and conflicts. Assembly must not silently restart
resources, move data, change secrets, or disable deployment triggers. Approval is
required before adoption changes control; permissions may allow automatic read-only
inventory updates.

Add Dokploy, Dokku, and Portainer adapters with contract tests. Discovery support
can ship before deployment mutations. Existing operator capabilities and ownership
remain authoritative.

### Stack detection and builds

Inspect repositories for manifests, lockfiles, Dockerfiles, process definitions,
frameworks, commands, ports, and health endpoints. Propose build/runtime settings
and resource changes with evidence, and allow manual overrides.

Evaluate Railpack as the first automatic image builder. The supplied "rollpack"
name is treated as a probable reference to Railpack. Nixpacks explicitly recommends
Railpack as its replacement; Dockerfiles and other buildpack paths remain supported
choices. [Railpack](https://railpack.com/), [Nixpacks status](https://nixpacks.com/docs/getting-started).

Builds are code execution. Isolate them, limit resources and credentials, pin builder
versions, and produce immutable artifacts. Optimization suggestions must be tested
against startup, health, memory/CPU, and application behavior before adoption.
Never rewrite product behavior or migrations to satisfy a guessed optimization.

### Image management

Add registry credentials, architecture checks, artifact retention, cache visibility,
controlled garbage collection, provenance/SBOM hooks, and optional vulnerability
or signature policies. Prefer existing tooling and registries. Cleanup supports
dry-run and keeps active, referenced, and recovery artifacts.

**Exit gate:** assemble an existing multi-component application with an evidence-backed
plan, preserve its data and behavior, and produce a reproducible verified image.

## M4 Monitoring, analytics, and telemetry

Unify application/component logs and operation events with timestamps, source,
release correlation, bounded queries, retention, and export. Enrich the scoped
[release/change context](#release-and-change-context-for-ai-diagnosis) with these
evidence links so agents can correlate symptoms with deployment changes. Avoid requiring a
full monitoring stack for basic health visibility; integrate existing monitoring
backends where useful.

**Accepted logging requirement:** native OAP log search backed by optional
single-node VictoriaLogs, with proper field/full-text indexing, one host collector,
trusted app/release correlation, bounded retention and scoped queries. Keep live
operator logs available without this backend. VictoriaMetrics remains a separate
optional metrics integration. Backend and collector deployment are planned, not
implemented. See [logging and search design](docs/logging-and-search.md). The
current M0 batch order stays unchanged.

### Deployment Topology View

Requested on 11 October 2026: add a flow diagram to the deployment UI showing
components, their running instances, and how they are connected. Schedule this in
M4 after core workflows; it does not change the current delivery order.

Start with a read-only, application/environment-scoped topology built from OAP
definitions and adapter-reported runtime state. Group replicas under their component
and show instance health, deployment state, platform/target placement, and available
endpoints. Selecting a node opens its existing instance details or logs; retain a
compact table alternative for accessibility and small screens.

Label edges by their actual meaning: declared service links, deployment dependencies,
or adapter-confirmed network membership/routing. A deployment dependency is not proof
of a network connection; shared network membership is not proof of communication.
Distinguish configured relationships from observed runtime evidence, including its
source and freshness. Show unknown or unavailable data explicitly rather than infer
traffic from environment variables or imply that a configured connection is working.

This is deployment topology, not distributed tracing. The initial view requires no
application instrumentation, traffic capture, AI, or new monitoring service. Traffic
rates, latency, and request paths may be optional later overlays only when supported
by an explicitly configured data source. Never expose secret values in node or edge
labels; apply the same application scopes and permissions as the underlying APIs.

**Exit gate:** reproducible E2E fixtures cover multiple components and replicas,
declared links versus deployment dependencies, healthy/failed/missing instances,
stale or unsupported adapter evidence, and cross-application scope isolation. The
diagram and table agree with runtime data and remain usable with AI disabled.

### Bounded post-release monitoring mode

**Accepted product goal:** make useful release-focused logging and assisted debugging
accessible to small teams that cannot justify an enterprise observability subscription.
Keep the default self-hosted footprint and operating cost small; reuse existing
operator/monitoring sources, with optional indexed logs and BYOK inference. This is
planned M4 work, built on M1 visibility and M2 scoped diagnosis; the M0 order stays
unchanged. This does not claim enterprise APM coverage or measured cost savings.

An owner can enable a monitoring window for one release, or explicitly configure an
application policy to watch subsequent releases. Default off; duration, sampling,
retention and notification/model budgets are configurable (for example, a 15–60
minute window). The window is separate from rollout readiness and does not delay
or change the operator's deployment workflow. Support operator-owned releases as
well as OAP-managed ones where the adapter can correlate the actual deployed commit.

Watch release-correlated runtime/build/operation logs, health, restarts, resource
pressure, and request/error/latency signals when available. Compare with an authorized
pre-release baseline; link anomalies to exact commits, artifacts and configuration
changes using the [diagnostic context](#release-and-change-context-for-ai-diagnosis).
Start with deterministic checks and use optional AI to summarize evidence, cluster
new errors and suggest verification steps. Missing traffic/instrumentation, stale
observations or collection gaps produce an inconclusive result, not an all-clear.

Expose watch progress, evidence links, findings and a final healthy/issue/inconclusive
report in UI/API, with deduplicated owner-configured notifications. A completed watch
only describes its coverage/window; it is not proof that the release has no defects.
Persist release identity, start/expiry, sampling cursor and findings so controller
restart resumes only the remaining window without duplicate alerts or inference.
Owners can stop or extend a watch. Apply scope, redaction, rate/concurrency limits,
local retention and AI cost caps; external model sharing needs its own opt-in. AI-off
keeps deterministic monitoring and manual/API diagnosis available with no OAP model
calls. Monitoring grants no restart, rollback or database mutation authority.

**Acceptance:** stage a regression that passes initial readiness but develops errors
or latency afterward. Detect it within the configured window, connect findings to
the release/change evidence, and notify once. Test expiry/stop, restart recovery,
healthy and inconclusive cases, absent metrics, bounded resource/model usage,
AI-off operation, secret redaction, and cross-environment access denial. No new
monitoring cluster or automatic remediation is required for this gate.

Keep three distinct data classes:

| Data | Purpose | Default direction |
| --- | --- | --- |
| Operational monitoring | Health, resource usage, deployment and recovery failures | Local collection and bounded retention |
| Per-app analytics | Requests, errors, latency, and owner-selected product events | Explicit instrumentation and consent |
| OAP product telemetry | Optional diagnostics/usage sent outside the installation | Disabled unless enabled by the owner |

OAP cannot infer all traffic or product behavior from a running container. Per-app
analytics requires a gateway, application instrumentation, or an operator data
source. Distinguish missing data from zero usage.

Provide a custom analytics backend interface with a small local implementation
first and optional event/OTLP exports. Batch writes and retention are bounded.
Introduce a separate store only after measured volume requires it. Keep PII and
secrets out of default events and AI context.

Notifications use deduplicated events for failed releases, stale states, unhealthy
components, backup failures, approvals, security exceptions, and recovery. Support
owner-configured destinations, suppression, escalation, and delivery failures.

### Opt-in feedback and diagnostic sharing

Feedback submission and outbound diagnostic uploads are disabled by default.
Keep their consent controls independent of product telemetry and AI context
sharing. Local diagnostics remain available without external sharing or AI.

Feedback is user-initiated, with optional contact details and attachments. Before
sending a diagnostic bundle, let the owner select the logs and time window,
preview its contents, and redact data. Exclude secrets, personal data, and
application content by default; keep aggregate diagnostics separate from
identifiable feedback. State the destination, purpose, and retention before
consent. Consent can be revoked, and enabling sharing must not upload historical
backlogs without explicit consent. Do not automatically submit tickets or send
messages on the owner's behalf.

**Exit gate:** trace a failed request or release across relevant logs/events, export
analytics to a chosen sink, preview and redact an explicitly requested diagnostic
export, and disable all outbound sharing without losing local operability.

## M5 Multi server coordination and policy metadata

Support multiple targets where their runtime and transport capabilities allow it.
Begin with explicit placement. Add private connectivity, target health/capacity,
artifact distribution, generation fencing, and capability-aware recovery plans.

Do not imply a cross-host network or distributed volume from a Docker bridge or
local disk. Stateless relocation is different from stateful failover. Cross-server
routing, shared data, uploads, queues, and recovery objectives must be explicit.
Delegate native scheduling to runtimes that already provide it.

Introduce namespaced OAP annotations and enforced taints. Candidate policies
include production, manual-management, AI-disabled, pinned-placement,
stateful-local-storage, and requires-owner-approval. Annotations carry descriptive
metadata and ownership evidence; they are not credentials or authorization claims.

A taint blocks or changes eligibility at planning and execution. Tolerations require
appropriate authority and appear in the plan/audit trail. In-flight plans become
stale when applicable placement or policy generations change. Preserve existing
Docker ownership labels rather than relabeling resources destructively.

**Exit gate:** losing one eligible server does not create duplicate execution or
pretend that inaccessible local state is recoverable. Placement respects taints,
and manual overrides survive reconciliation.

## M6 Additional runtimes and stateful actors

Keep operator adapters, direct execution runtimes, builders, transport, secrets,
and analytics as separate seams. Extend the workload/artifact model only when an
implemented runtime requires it; preserve existing image-based definitions.

### Firecracker

Investigate a microVM adapter on eligible Linux/KVM hosts. Evaluate guest kernels,
root filesystems, networking, storage, jailer/resource policies, images, snapshots,
and crash recovery. Firecracker is a Linux KVM virtual machine monitor, not a
Docker API substitute. [Firecracker project](https://github.com/firecracker-microvm/firecracker).

### celld and Cloudflare Durable Objects

Evaluate celld as an optional self-hosted actor runtime. Its project describes a
Durable Objects model with per-cell state and bucket-backed durability; validate
compatibility and failure behavior before supporting production workloads.
[celld](https://celld.dev/).

Add optional Cloudflare Workers/Durable Objects deployment support using the owner's
account and credentials. Durable Objects have a stateful programming model, so
capabilities must describe actor state, bindings, and lifecycle rather than
pretending they are ordinary containers.
[Cloudflare Durable Objects](https://developers.cloudflare.com/durable-objects/).

These are application-runtime integrations. They do not replace PostgreSQL/River
or become required coordination/storage services for OAP. The self-hosted core
must continue without a Cloudflare account or celld installation.

**Exit gate:** each runtime has a real example, capability contract, isolation review,
backup/state recovery test, and documented unsupported behavior. Run only eligible
workloads on eligible targets.

## Complete request coverage

| Requested area | Primary phase |
| --- | --- |
| KISS, one-person, self-hosted stack; measured lightweight footprint | Product rules and every phase; reference budgets/benchmarks in M1 visibility and dogfooding |
| Auth; agent identity, auth, and audit | M1, extended in M2 |
| Crash recovery and stale states; retries and timeouts | M1 |
| Backups and restore | M1 |
| Secret store; BYOK and automatic local key creation | M0 minimum runtime references where needed; M1 store/BYOK, provider extensions later |
| Container security | M1, build isolation in M3, runtime isolation in M6 |
| Docker image management | M1 baseline, M3 full lifecycle |
| Dogfooding | M1 and each release |
| Tunnels and Tailscale for remote AI access | M2 |
| AI-native lifecycle; destructive agent actions | M2 |
| Agent/AI guardrails, especially direct database access | M1 identity/permissions; M2 bounded access and execution |
| Manual overrides | M2, placement extensions in M5 |
| Disable AI; BYOK models; API-first agents | M2 and product rules |
| GitHub release triggers; repositories and exact build artifacts | M0 core ownership; M3 build/stack extensions |
| Automatic Let’s Encrypt SSL; operator/standalone enable-disable controls | Deferred M0 stable-endpoint/domain work; capability-aware ingress ownership |
| Environments, routes, optional rollout strategies | M0 environment/routing contracts; optional rolling/blue-green after verification |
| Workers, jobs, linked dependencies | M0 verified workload slices; stateful provisioning after backup/storage gates |
| Skills | M2 |
| ChatGPT plugins/apps/MCP integration | M2 |
| Discovery, grouping, and application assembly | M0 guided/manual; M3 optional AI assistance |
| Automatic stack detection and optimization; Railpack/Nixpacks alternatives | M3 |
| Monitoring and indexed native log search | M1 health/collection baseline; M4 optional VictoriaLogs, collector, scoped search and log alerts |
| Notifications | M1 event foundation, M2/M4 delivery and workflows |
| Per-app analytics; custom analytics backend | M4 |
| Deployment topology flow diagram: components, instances, and connections | M4; read-only declared/runtime topology first, optional measured traffic overlays later |
| Optional telemetry | M4 |
| Opt-in feedback and diagnostic sharing | M4; independent consent, preview/redaction, local diagnosis |
| OAP taints and annotations | M5, sensitive app policies begin in M1/M2 |
| Multi-server support where applicable | M5 |
| Non-Docker platforms and Firecracker | M6 |
| celld | M6 |
| Cloudflare Durable Objects | M6 |

## Historical implementation slice (superseded by the M1 breadth queue)

The following batch 5 notes are historical; the current priority and ordered M1 queue above govern new work.

The UI audit, architecture alignment corrections and supported batch 4 recovery
acceptance are delivered. The former next step was M0 batch 5: stable endpoints, domains and
replica routing. The first slice fixes native Coolify primary endpoint projection
without changing provider domains. Shared component routing remains incomplete;
follow the [endpoint acceptance order](docs/stable-endpoints.md).
The [connected preview](examples/connected-preview/README.md) is now a retained
staging deliverable: one frontend, two API instances and a reviewed shared route
on Coolify's existing proxy. Selected-component update, rollback/return, a stopped
backend, recorded restart and continued application traffic while OAP is down are
verified. See the [acceptance record and walkthrough](docs/connected-preview-acceptance.md).
Retain the demo for owner testing;
manual native route configuration is explicit and does not complete batch 5's
automatic membership/domain/draining gates.
The next slice adds [reviewed route membership](docs/route-membership.md): owner
CLI plans derive healthy managed bindings, retain retirement ownership evidence,
and reject stale app/target/proxy/file state before native publication. A real
two→three→two replica cycle passed on the retained demo, including idempotent
retry, stale-plan rejection and unchanged frontend process/shared URL. Coolify's
recorded retirement capability is implemented with ownership checks and Docker
cleanup disabled; the native application and binding are retained, while Coolify
removes stopped containers. This is a staging publisher pilot, not background
reconciliation or dashboard/agent route authority; batch 5 stays active.
The following [Endpoints workflow](docs/endpoint-management.md) brings existing
staging route review/apply into the dashboard and API with immutable saved plans,
owner-session publication audit, native drift checks and durable crash fencing.
Its supported scope is an administrator-bound native Coolify route; generic
domain/TLS setup, automatic membership reconciliation, draining and bare Docker
shared proxy routing remain the next batch 5 acceptance gates.
The configured staging UI/API flow passed a live two→three→two cycle, stale-review
rejection, publication retries, controller-offline traffic and metadata restore.
See the acceptance section in `docs/endpoint-management.md`; this does not close
the remaining batch 5 gates.
The next [Domain & TLS readiness](docs/endpoint-verification.md) slice adds saved,
explicit-owner checks of configured DNS addresses, TLS and HTTPS readiness. It
separates membership from endpoint reachability, refuses unexpected DNS/redirects,
and expires evidence after configuration changes or ten minutes. Native issuance
remains delegated to the existing operator; domain creation, automatic membership,
draining and bare Docker routing remain open.
The [health-aware reviewed routing](docs/health-aware-routing.md) slice now excludes
unhealthy/stopped owned instances from a reviewed backend set, rechecks health
before apply and preserves the existing route when none are healthy. Recovery
requires a new owner review; background reconciliation and draining remain open.
The [opt-in automatic membership](docs/automatic-route-membership.md) slice adds
owner-controlled minute checks using River, pinned native authority, drift pauses
and durable publication recovery. Automatic updates remain off until explicitly
enabled. Domain creation, draining and general bare Docker routing remain incomplete and
are parked under the breadth-first priority correction.


Batches 2 and 3 are already delivered; follow the priority correction above. Source-backed management handoff and GitHub sequencing must
be verified before disabling an existing operator trigger. Preserve the KISS stack,
local pre-push gate, and capability-aware behavior. Full owner-grade hardening,
remote agents, AI guardrails, and generative UI remain later work; production
exposure still requires M1 gates.

Recovery acceptance includes native Docker lost-response/controller reconstruction
and an [owner recovery runbook](docs/recovery-runbook.md). A child controller
process-kill drill interrupts a completed native dispatch before its response is
persisted, checks worker-session locking/release and durable attention fencing,
and requires an unchanged runtime boot identity after exact operation recovery.
This uses a controller `Advance` subprocess, not the full HTTP/River daemon.
Native PostgreSQL data/schema recovery acceptance now includes a reproducible
custom-format backup/restore into an isolated database, incompatible migration
and corrupt archive checks, post-backup data-loss visibility and read/write
verification. An OAP system regression verifies image/configuration recovery does
not rewind application data/schema. See the [example](examples/stateful-recovery/README.md).
Native Docker volume recovery acceptance adds a stopped synthetic writer,
quiesced tar backup, restored uploads/ownership, source preservation, corrupt
archive rejection and a non-root single-writer cutover. It retains an archive and
SHA-256 receipt in each local run. Coordinated database/filesystem recovery,
real multi-writer application cutover and arbitrarily stale metadata reconciliation
remain unsupported. These are native-procedure checks, not OAP automatic restore
or stateful provisioning. The final supported-recovery acceptance review closes
batch 4 for stateless managed-image workflows. Batch 5 routing is now frozen at
its accepted pilot scope while the breadth-first queue is active. Do not expand
these drills into the later stateful-provisioning or owner-backup implementations.

Open decisions include the owner login method, first external secret provider,
first analytics export, default inference policy, tunnel choice for each client,
and acceptable recovery objectives. Evaluate
runtime research independently so it does not block the core owner experience.

## Architecture alignment correction

The implementation must use the agreed Tailwind/shadcn UI foundation, pgx/sqlc,
OpenAPI-generated dashboard contracts, and SSE application/release/log updates.
These are core corrections rather than optional new roadmap integrations.
Component write claims and dependency read guards replace the previous application-wide
active-release constraint; short application admission/configuration locks remain.
The operator-first boundary continues to preserve existing native workflows.

The architecture correction itself did not advance M0 beyond batch 4. Subsequent
supported recovery acceptance now permits the batch 5 handoff. Stable routing, native source
mutation, workload breadth, indexed logging, AI diagnosis and additional adapters
remain scheduled by their existing acceptance gates. See the
[alignment audit](docs/architecture/alignment-audit.md) for implementation and limits.
