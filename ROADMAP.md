# OpenAppPlatform Roadmap

OpenAppPlatform should be a self-hosted application platform that one person can
operate, manually or through authenticated agents. Its core must stay useful
without AI, external cloud services, or a collection of infrastructure services.

This roadmap captures the proposed priorities as of 9 October 2026. Phases are
sequenced by dependency and acceptance criteria, not delivery dates. Listed
integrations are candidates; they are not installed or enabled by this document.

## Product rules

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
  footprint before it becomes part of the default installation.

## Current foundation

Implemented and tested:

- Application and component definitions with target-scoped instance bindings.
- Coolify and direct Docker adapters; standard web-component releases.
- Durable PostgreSQL and River jobs, release snapshots, concurrency protection,
  idempotency, retries, deployment deadlines, and conservative uncertain dispatch.
- Versioned configuration editing, stale-plan preconditions, and checksummed SQL
  migrations.
- Dashboard, bounded logs, OpenAPI, examples, and read-only stdio MCP tools.
- Local Docker lifecycle and replacement tests, database/race/browser tests, and
  isolated Coolify staging verification.

The current access token is a local single-owner preview mechanism. Agent identity,
production authorization, complete stale-state recovery, stateful workload
management, and automatic discovery/assembly are not complete.

## Delivery sequence

| Phase | Outcome | Main work |
| --- | --- | --- |
| M1 | Trust OAP with an owner's real applications | Auth, agent principals, audit, recovery, backups, secret references, basic security and observability, dogfooding |
| M2 | Operate safely through remote APIs and agents | Tunnels, remote MCP, action plans and approvals, manual overrides, AI switches, skills, notifications |
| M3 | Assemble existing applications and build from source | Operator discovery, grouping/adoption plans, optional AI assistance, stack detection, build adapters, image lifecycle, more operators |
| M4 | Understand application behavior | Unified monitoring/logs, per-app analytics, custom analytics sinks, optional telemetry, bounded diagnosis |
| M5 | Coordinate more than one server | Target capabilities, transport, taints, placement, fencing, routing, explicit recovery and storage constraints |
| M6 | Support additional execution models | Firecracker investigation, celld and actor workloads, optional Cloudflare Workers/Durable Objects adapter |

Security, recovery, and dogfooding continue in every phase. Basic monitoring and
backup visibility belong in M1; richer analytics can wait until M4.

## M1 Owner grade operation

### Authentication and agent identity

Replace the shared preview token with owner authentication and separately issued
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
logs, and backup status. Add notification events through a transactional outbox.
No email/chat notification is sent until the owner configures that destination.

**Exit gate:** restore OAP on a fresh server, recover an interrupted release without
uncontrolled duplication, revoke an agent credential, and explain every mutation
in the audit trail. Then pass a real staging update and rollback/recovery drill.

## M2 Safe remote and agent operation

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
approval, and disabling AI produces no OAP model calls.

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
release correlation, bounded queries, retention, and export. Avoid requiring a
full monitoring stack for basic health visibility; integrate existing monitoring
backends where useful.

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
| KISS, one-person, self-hosted stack | Product rules and every phase |
| Auth; agent identity, auth, and audit | M1, extended in M2 |
| Crash recovery and stale states; retries and timeouts | M1 |
| Backups and restore | M1 |
| Secret store; BYOK and automatic local key creation | M1, provider extensions later |
| Container security | M1, build isolation in M3, runtime isolation in M6 |
| Docker image management | M1 baseline, M3 full lifecycle |
| Dogfooding | M1 and each release |
| Tunnels and Tailscale for remote AI access | M2 |
| AI-native lifecycle; destructive agent actions | M2 |
| Manual overrides | M2, placement extensions in M5 |
| Disable AI; BYOK models; API-first agents | M2 and product rules |
| Skills | M2 |
| ChatGPT plugins/apps/MCP integration | M2 |
| AI-enabled discovery, grouping, and application assembly | M3 |
| Automatic stack detection and optimization; Railpack/Nixpacks alternatives | M3 |
| Monitoring and logs | M1 baseline, M4 richer integration |
| Notifications | M1 event foundation, M2/M4 delivery and workflows |
| Per-app analytics; custom analytics backend | M4 |
| Optional telemetry | M4 |
| Opt-in feedback and diagnostic sharing | M4; independent consent, preview/redaction, local diagnosis |
| OAP taints and annotations | M5, sensitive app policies begin in M1/M2 |
| Multi-server support where applicable | M5 |
| Non-Docker platforms and Firecracker | M6 |
| celld | M6 |
| Cloudflare Durable Objects | M6 |

## Next implementation slice

Implement owner/agent authentication, scoped credentials, and a mutation audit trail
alongside a safe attention-state recovery workflow. Add backup/restore coverage and
local encrypted secret references before remote mutation access or autonomous
lifecycle management. Start dogfooding in staging once those gates pass.

Open decisions include the owner login method, first external secret provider,
first analytics export, default inference policy, tunnel choice for each client,
acceptable recovery objectives, and the project's open-source license. Evaluate
runtime research independently so it does not block the core owner experience.
