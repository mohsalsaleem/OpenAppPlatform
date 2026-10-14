# Open App Platform Roadmap

Updated 14 October 2026. This document is the authoritative execution order.
It supersedes earlier operator-first priorities and UI delivery queues.
Historical implementation and acceptance notes are retained in the
[archived roadmap](ROADMAP-history-through-2026-10-13.md).

## Direction

Build a lightweight, self-hosted application platform for **one owner and an
external AI agent**, with **standalone Docker as the primary execution path**.
Agents must be able to install OAP, define applications, deploy, observe,
diagnose, correct and verify through documented machine interfaces. The owner
sets authority and intervenes when required; routine permitted operations should
not depend on dashboard clicks.

The product is **an application platform on one server, operated by an agent**
(see [positioning](docs/positioning.md)). Two pillars define it: the
**application as a unit** (one specification for every component, with app-wide
deploy, scale, release, rollback and health) and **agents as the operator**
(edit spec → validate/diff → plan → apply → verify, under bounded server-enforced
authority). The defining capability is the scale loop: raise a web component's
`instances` in the specification, apply, and every healthy copy serves traffic
behind one stable HTTPS address. Compose files are an import format; OAP must
stay far smaller than Kubernetes and never re-implement it.

AI interface means deterministic APIs, structured CLI commands, MCP tools and
versioned skills that external agents can use. It does not require an embedded
model, chatbot, agent runtime or OAP inference subscription. Core operation remains
available with AI disabled and through ordinary scripts.

UI development is parked. Retain the existing dashboard and essential owner
access, approval, audit and emergency controls. Repair security or functional
regressions, but do not add screens, redesign navigation, build generated UI or
make UI flows prerequisites for the headless journey.

Operator expansion and operator-specific views are parked. Preserve existing
Coolify compatibility and native ownership; do not remove working adapters or
migrate existing deployments. Standalone acceptance must not require Coolify
credentials, resources or proxy access. OAP itself may remain on existing staging
hosting while standalone workload acceptance runs on isolated Docker.

## Product and engineering rules

- OAP owns each application specification and its version history; service
  repositories release only their own component's immutable image. No repository
  or Git host is a required or second writable source of the specification. See
  [spec ownership](docs/spec-ownership.md).
- Keep one Go service, PostgreSQL/River and Docker Engine as the default stack.
  No required model runtime, per-workload agent, separate queue, registry,
  identity service or monitoring cluster.
- One lifecycle implementation serves REST, CLI, MCP and the retained UI.
  CLI/MCP submit and observe durable controller operations; client interruption
  must not become the source of deployment truth.
- Use one owner and named agents, bounded grants and an audit trail. Expand scope
  only enough for the headless journey; no enterprise tenancy or agent fleet.
- Authorization is enforced by the server, never by prompts or skills. Ordinary
  reversible actions may run under explicit owner policy; risky actions need
  exact review. Agents cannot self-approve or write OAP's control database.
- Keep configuration save, plan creation, execution and verification distinct.
  HTTP 202 means admission, not deployment success or application correctness.
- Observe existing resources without taking ownership. Preserve native builds,
  triggers, configuration, routes, volumes and secrets for operator resources.
- Standard deployment remains the initial policy and may cause downtime.
  Rolling or blue-green is an optional later choice, not a forced prerequisite.
- Ship breadth before depth: one complete workflow per slice, explicit limits,
  meaningful tests and reproducible evidence before expanding it.
- Run the required exact-tree local gate before pushing. Automatic Git CI stays
  disabled. New feature acceptance is standalone-first; retain existing regression
  checks, including operator contracts, without expanding their feature scope.

## What exists, and what is still missing

Verified foundation includes Docker and Coolify adapters; web/worker components;
versioned definitions; private runtime/image references; named volumes and resource
limits for supported owned workloads; app-local Docker bridges/DNS; durable jobs,
frozen releases, idempotency, retries, recovery fencing; health and bounded logs;
owner auth, scoped agents, audit and pause/revocation; optional encrypted host-file
secrets; metadata backup/recovery procedures; and a retained dashboard.

The M2 baseline adds scoped diagnostic inspection, optional authenticated remote
read-only MCP and exact reviewed deployment plans. Latest delivered checkpoint:
`41afc75`, local gate `20261013-193721-7ad0c783`, plus scoped Coolify staging
acceptance. See [M2 baseline and limits](docs/m2-agent-operation.md) and
[v0.1 acceptance](docs/v0.1-acceptance.md). These receipts are historical evidence,
not acceptance of the new standalone headless milestones.

M2.5 and M2.6 add the JSON CLI, shared bounded waits/errors, headless owner
setup, bounded named-agent creation grants and packaged install/update/rescue.
M2.6 passed local gate `20261013-214602-ceb6aceb` at `c2e1eee`.
M2.7 passed local gate `20261013-222818-1c1a5624` at `187e7d2`, adding the
portable lifecycle and shared CLI/API/stdio-MCP action plans. M2.8 passed local
gate `20261013-231136-dca41e73` at `1664ceb`, adding bounded incident evidence and
a named-agent diagnose/correct/verify flow. M2.9 passed combined dogfood and the
exact-tree gate `20261014-000048-91413718` at `76b4061`, including install-to-fix,
compatible update, crash/rescue and metadata restore. Standalone shared ingress/TLS
and the full specification-driven scale loop remain missing.
App-scoped credentials still cannot create applications; creation needs an
explicit bounded onboarding grant. Read-only MCP deployment summaries remain
separate from durable executable plans. Functional M2 acceptance is complete;
the measured combined idle CPU goal remains unmet.

## Delivery sequence

Keep M0/M1 and the shipped M2 baseline as historical foundation. Continue M2 with
new slice IDs so previous M2.1–M2.4 acceptance records retain their meaning.

| Milestone | Outcome | Status |
| --- | --- | --- |
| M2 — Agent-operated standalone core | One agent completes install → deploy → diagnose → correct → verify without dashboard dependence | Complete at `76b4061` |
| M3 — Application unit and standalone delivery | Application router and scale loop, specification-driven topology and pre-deploy jobs, previews/checks/guarded rollout and environment comparison, source-to-release, Compose import, image/storage safety, lightweight recovery | Active: M3.1 |
| M4 — Bounded application insight | Indexed logs when needed, release watches, metrics/analytics and notifications | Deferred |
| M5 — Operator integration return | Capability-aware existing-resource discovery/assembly and operator adapters/views | Parked until standalone gates pass and owner reprioritizes |
| M6 — Additional servers and runtimes | Explicit multi-server placement, policy metadata, Firecracker/actor experiments | Deferred |

Only the ordered active queue authorizes the next implementation slice. Deferred
items are preserved requirements, not parallel work or prerequisites. M2.5 passed
gate `20261013-210851-28d984b5` at `6b9359c`; see [machine CLI](docs/agent-cli.md).
M2.6 passed gate `20261013-214602-ceb6aceb` at `c2e1eee`; see
[headless bootstrap](docs/headless-bootstrap.md). M2.7 passed gate `20261013-222818-1c1a5624` at `187e7d2`; see
[portable lifecycle](docs/portable-lifecycle.md). M2.8 passed gate `20261013-231136-dca41e73` at `1664ceb`; see
[bounded incident diagnosis](docs/incident-diagnosis.md). M2.9 is accepted at
`76b4061`; see the [standalone runbook/skill](docs/standalone-agent-runbook.md)
and [measured footprint](docs/footprint.md). The active slice is **M3.1 — application
router and scale loop**, using Caddy. Owner-configured standalone peers now support
spec-declared routes, durable release publication and named-agent 1 → 3 → 1
scaling with traffic verification and observation-only lost-response recovery.
Default targets remain fail-closed. Automatic proxy packaging, multi-application
lifecycle aggregation, route/domain handoffs, integrated HTTPS and proxy footprint
acceptance remain open. See [standalone ingress](docs/standalone-ingress.md). UI/operator expansion remains
parked. The measured combined idle CPU gap stays open for M3.7.

## Ordered active queue

| Order | Deliverable | Acceptance gate | Stop boundary |
| --- | --- | --- | --- |
| M2.5 — Machine contracts and CLI | Thin public JSON CLI over existing APIs; explicit target/app/environment context; inspectable schemas/capabilities and permitted actions; stable machine errors, exit codes and operation handles; shared bounded wait behavior | Script creates/reads a supported standalone definition using current authorized APIs, deploys, waits and verifies an outcome; denied/stale/timeout cases return actionable structured errors; client disconnect/retry does not duplicate dispatch | No interactive agent/chat runtime, new scheduler, broad SDK collection or invented API success claims |
| M2.6 — Headless trust and bootstrap | Versioned external install/check/update/rescue commands; headless owner setup and review; named agent grant permitting bounded target discovery and app creation; owner pause/revoke retained | Fresh isolated installation establishes owner and agent without browser clicks; agent creates an app only within allowed target/environment; siblings, self-approval and control DB writes denied; external rescue works with OAP down | No fleet/enterprise IAM, mandatory OAuth/tunnel, unrestricted host executor or agent-issued owner authority |
| M2.7 — Portable application lifecycle | One versioned app specification for components, connections, secret references, storage and ownership; validate/diff/save; consistent action planning for the selected create/configure/deploy/restart/recovery journey; execution-facing MCP backed by the same contracts | Frontend/API/worker and a linked fixture DB deploy on Docker; declared internal connections work; save does not deploy; stale plans rejected; routine policy and explicit review both tested; CLI/API/MCP agree | No DB provisioning/migrations, automatic adoption, arbitrary shell/SQL tools, broad rollout policies or spec rewrite without migration compatibility |
| M2.8 — Diagnose, correct and verify | Bounded incident projection joining current observations, release/change context, operation timeline and selected redacted logs; freshness/missing evidence; explicit verification criteria and allowed recovery steps | Inject release failure; external agent consumes evidence, proposes/executes authorized fix and verifies fixture behavior; prompt injection, missing/stale evidence, scope/redaction, paused/revoked agents and uncertain dispatch covered | No mandatory inference, indexed log cluster, distributed tracing, application DB query tools or autonomous destructive repair |
| M2.9 — End-to-end dogfood and exit | Reproducible headless standalone acceptance; install-to-fix runbook/skill; compatible update, controller crash and external rescue/metadata restore drills; measured footprint report | Clean isolated Docker environment completes the entire journey without dashboard clicks or operator dependencies; destroy/recreate only test fixtures; retain receipts and documented limits; full local gate passes | No production promotion, zero-downtime claim, unverified app-data restore or whole-platform rewrite |

M2.5 may exercise current owner-authenticated APIs to establish contracts. M2.6
adds agent onboarding authority; M2.7 then proves that the full journey works as
the named agent. This sequence must not disguise an owner credential as an agent.
Schema discovery describes available interfaces; effective capabilities must also
reflect principal permissions, workload ownership, runtime support and policy.

The concrete **M2 deliverable** is one owner trust setup followed by an external
agent installing OAP, connecting standalone Docker, creating a multi-component
application, releasing immutable images, diagnosing an injected regression,
correcting it and verifying application behavior. No dashboard interaction is
required. Corrections do not imply authority to change repository code or data:
those permissions remain explicit and source edits/tests stay with the external
agent's development tools.

## M3 Application unit and standalone delivery

Schedule these only after M2 acceptance, one slice at a time. The order puts the
application-unit pillar first: replica routing turns several owned containers into
one scaled application, which is the gap no Compose/Coolify-style tool closes.

1. **Application router and scale loop:** one optional OAP-managed proxy
   (Caddy selected: automatic Let's Encrypt and a live admin API) on the
   application's Docker network. The specification declares an app domain and
   per-component path routes (for example `/` → web, `/api` → api). Only ready
   instances are route members; scale-down removes a member, drains it, then stops
   it. Scale-up and scale-down both run through specification apply, not only the
   dashboard. Routes are persisted on disk and keep serving while OAP is down.
   The owner has provided a public Vultr test host and controlled shipping-site
   domain, authorizing downtime. Use the [documented fresh installation](docs/standalone-public-acceptance.md)
   and the same checked-in CLI acceptance runner used locally. Packaged proxy
   provisioning and explicit owner-selected public ACME authority are implemented;
   full local regression and public issuance/persistence acceptance must pass before
   declaring those verified. Scheduled renewal requires separate evidence;
   local CA or certificate restart persistence does not prove it.
   Workers scale without routes; writable-volume components stay at one instance.
   Fixed host ports remain for single-instance compatibility. Account for the
   proxy as a separate service in footprint measurements. Acceptance: an agent
   applies the reference application, scales the API 1 → 3 with requests reaching
   every instance, scales 3 → 1 with no failed requests, and restarts OAP while
   traffic continues. No blue-green or zero-downtime claim beyond the tested cases.
2. **Specification-driven topology and pre-deploy jobs:** add and remove components
   through the specification save/plan path, with explicit review for removal and
   retained volumes. Add a run-to-completion job kind (for example migrations) that
   a release runs before rolling out dependent components; failure stops the
   release with evidence. Application rollback reuses the existing
   [image rollback](docs/image-rollback.md) across all owned components; schema
   and data are never rolled back implicitly. Make OAP's stored definition
   versions usable as the specification's history: record author (owner or named
   agent), time and message on each version; add history, version diff (masked)
   and revert-as-a-new-plan; and add a one-way `spec export --dir` mirror for
   repositories. Acceptance: an agent reverts a topology change through a plan,
   the history shows who changed what, and the export round-trips through
   validate/diff without exposing secret values.
3. **Verified change flow and environments:** make "evidence before exposure"
   the default without forcing a traditional dev → staging → production chain.
   (a) **Preview environments:** a temporary environment built from production's
   current release plus one change (image and/or spec), on its own route
   (for example `pr-42.<domain>`), with non-production secret bindings, seeded or
   empty data by default, a concurrent-preview cap and automatic expiry/cleanup.
   Never attach a live production database. (b) **Declared checks:** the spec
   declares deterministic application checks (HTTP requests with expected status
   and body fragments, worker job probes) that OAP runs and records as evidence;
   an agent's claim is not evidence. (c) **Guarded rollout:** using the M3.1
   router, a release updates one instance first, runs checks against it, then the
   rest; a failed check rolls back automatically to the previous release. Schema
   changes must be backward compatible while old and new instances overlap.
   (d) **Promotion of verified releases:** production may require that the exact
   image digests passed checks in a preview (owner policy, off by default); a fast
   lane for urgent fixes skips the preview only with a recorded reason and exact
   owner approval. (e) **Optional long-lived environments and comparison:** named
   environments (for example staging) remain supported, never required. A read-only
   comparison of any two environments' current releases classifies differences
   as declared per-environment overrides, changes one side has that the other
   lacks, or undeclared drift, with values masked. Feature flags, not divergent
   environments, are the recommended way to hold unreleased work. Acceptance: an
   agent opens a preview for an API change, declared checks pass, the release rolls
   out one instance first and completes; an injected failing check rolls back
   automatically; the preview expires and its containers are removed; comparing
   two named environments reports expected, ahead and drift entries without secret
   values. See [environments and verified change](docs/environments.md).
4. **Source-to-release:** explicit GitHub release ownership, signed/deduplicated
   events, frozen commits and immutable artifacts. Start with a documented external
   build lane; evaluate Railpack/Nixpacks alternatives later. Source detection
   must not silently take over an existing trigger or add a mandatory builder.
   Add component-scoped release grants so a service repository's CI or agent can
   release only its own component's image, never edit the specification.
   Acceptance: three repositories release three components of one application
   independently; a credential for one is denied on the others.
5. **Compose import:** convert a `docker-compose.yml` into a proposed specification
   with explicit decisions and unsupported fields, per the
   [Compose import proposal](docs/compose-import.md). Import never deploys,
   adopts running containers or reads secret values.
6. **Image and storage safety:** runtime artifact inventory, retained known-good
   releases, bounded cache/retention, reviewed pruning and named-volume backup
   procedures. Protect active/stopped workloads and recovery artifacts. App-data
   restore is separately authorized and verified; metadata restore is not data restore.
7. **One-person operability:** offline-compatible rescue, secret/key recovery,
   backup checks, bounded metadata growth, retries/timeouts and measured resource
   regression checks. Add optional external secret providers only for a verified need.

Exit: the reference application (web, API, worker, linked PostgreSQL) runs on one
small server behind one HTTPS domain; an agent scales it through the
specification, ships one source change through a preview and guarded rollout as a verified release, and recovers from
an injected regression, with documented recovery and measured operating cost.
Optional ingress and build services have separate resource accounting. This is
not a multi-server gate.

## Lightweight requirement

Measure OAP service and dedicated PostgreSQL separately from Docker workloads,
builds and optional ingress/logging. Reference profile: a declared single-server
host, 10 applications / 50 components, fixed observation configuration. Exercise
idle, agent observation, deployment burst, target outage and controller recovery.
Record RSS/CPU, database growth/connections, runtime request rate, image size and
startup/recovery latency. No browser is required for the baseline measurement.

Provisional goals remain <=256 MiB service RSS, <=512 MiB service plus dedicated
PostgreSQL RSS at idle, and <=1% of one CPU core average idle usage. These are
unmeasured goals, not guarantees. Publish repeatable commands and measurements
in M2.9; revise goals explicitly if evidence requires it. The
[reference measurement](docs/footprint.md) meets idle memory comparisons but misses
the combined one-core idle CPU goal (about 1.86% versus 1%). Keep the 1% goal
unchanged and retain the gap for M3.7 one-person operability. Bound polling, log reads,
retries, concurrency and retained metadata. OAP failure must not stop existing
containers or routes. Do not install monitoring infrastructure just to benchmark.

## Deferred requirements and their return gates

| Area | Later scope / return gate |
| --- | --- |
| UI and generated UI | Parked. Revisit after M2/M3 headless workflows; render shared status/topology/evidence/approval models, keep authority server-side. Existing functional/security fixes remain allowed |
| Coolify, Dokploy, Dokku, Portainer | M5 after owner reprioritization: preserve native configuration, resource discovery/grouping, capability contracts and operator-specific acceptance; no adapter expansion now |
| AI-assisted discovery and application assembly | M5: deterministic proposals first, explicit ownership/handoff; no implicit adoption |
| Git-sourced application specifications | After M3: an app may declare one repository/path as its specification source. New commits become proposals that OAP validates, plans and reviews; the spec is read-only in OAP while Git-sourced. Exactly one authority per app; no bidirectional sync |
| Remote transport, Tailscale, OAuth, ChatGPT apps/plugins | Optional follow-up after M2 headless acceptance. Existing authenticated transport remains supported; never expose Docker/control DB or make a tunnel mandatory |
| Embedded inference and BYOK models | Optional after deterministic diagnosis is useful; disable AI without losing APIs; model/context sharing and budgets explicitly opt-in |
| Skills | Minimal versioned standalone operation skill/runbook in M2.9; client-specific packaging later |
| Database guardrails | Enforced throughout. No agent control DB access. Owner-enabled application query tools deferred; least-privilege DB identity and bounded results/time before any future access |
| Manual overrides and destructive actions | Scoped grants/plans/pause now; expiring per-app overrides later. Data deletion, destructive migration, rotations and pruning need separate exact authority and recovery checks |
| Logs, monitoring, post-release watch | M4: bounded deterministic checks first; optional VictoriaLogs collector/index, retention and scoped search; expiring release windows report healthy/issue/inconclusive, no silent remediation |
| Per-app/custom analytics | M4: explicit instrumentation/data source, small local sink and optional exports; missing evidence is not zero traffic |
| Notifications | M4: reuse durable outbox, deduplicated owner-configured delivery, budgets/retries; no outbound messages until configured |
| Telemetry, feedback and diagnostic sharing | M4: separate opt-ins, preview/redaction and bounded retention; default off; local diagnosis remains available |
| Topology diagram | Read models may serve agents in M2; visual UI in later work. Distinguish declared links, network membership and measured communication |
| Security | Existing Docker limits/ownership/no-new-privileges preserved; no privileged/bind/host-network generalization. Build isolation and image provenance/scanning introduced only by later explicit gates |
| Multi-server, taints and annotations | M6: explicit placement, capacity/health, generation fencing and enforced policy; no implied cross-host networking or replicated local volumes |
| Firecracker, celld, Cloudflare Durable Objects | M6 research after Docker operability; independent runtime/artifact/state contracts, real recovery/isolation examples; optional, never core dependencies |
| Blue-green and broader rollout policies | Guarded one-instance-first rollout is M3.3. Blue-green, weighted traffic splitting and other policies stay optional later work controlled by the owner, not enforced |

## Completion discipline

Each slice records implemented behavior, unsupported cases, a runnable example,
meaningful unit/system coverage, reproducible headless E2E and acceptance receipts.
Test Docker networking, persistent data preservation, scope/policy denial,
idempotent retries and crash/unknown outcomes where relevant. Never mark an
operation healthy only because its provider call succeeded. Keep fixtures isolated
and preserve existing staging/production resources and private credentials.

When a slice passes, update this queue and take the next listed slice. Do not
expand a parked item because part of its implementation is convenient. A priority
change needs an explicit roadmap update. Historic evidence and technical specs
remain useful, but this document decides what is next.
