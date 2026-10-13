# Open App Platform Roadmap

Updated 13 October 2026. This document is the authoritative execution order.
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
Current gaps include portable lifecycle/MCP parity acceptance, unified incident
evidence, standalone shared ingress/TLS and the complete install-to-fix dogfood.
App-scoped credentials still cannot create applications; creation needs an
explicit bounded onboarding grant. Read-only MCP deployment summaries remain
separate from durable executable plans. No whole M2 acceptance is implied.

## Delivery sequence

Keep M0/M1 and the shipped M2 baseline as historical foundation. Continue M2 with
new slice IDs so previous M2.1–M2.4 acceptance records retain their meaning.

| Milestone | Outcome | Status |
| --- | --- | --- |
| M2 — Agent-operated standalone core | One agent completes install → deploy → diagnose → correct → verify without dashboard dependence | Active; ordered queue below |
| M3 — Standalone delivery and operability | Source-to-release, optional public ingress/TLS, image/storage safety, lightweight recovery | Planned after M2 acceptance |
| M4 — Bounded application insight | Indexed logs when needed, release watches, metrics/analytics and notifications | Deferred |
| M5 — Operator integration return | Capability-aware existing-resource discovery/assembly and operator adapters/views | Parked until standalone gates pass and owner reprioritizes |
| M6 — Additional servers and runtimes | Explicit multi-server placement, policy metadata, Firecracker/actor experiments | Deferred |

Only the ordered active queue authorizes the next implementation slice. Deferred
items are preserved requirements, not parallel work or prerequisites. M2.5 passed
gate `20261013-210851-28d984b5` at `6b9359c`; see [machine CLI](docs/agent-cli.md).
M2.6 passed gate `20261013-214602-ceb6aceb` at `c2e1eee`; see
[headless bootstrap](docs/headless-bootstrap.md). The active slice is **M2.7**;
see [portable lifecycle](docs/portable-lifecycle.md). Its full local standalone
acceptance is required before M2.8 begins. M2.8–M2.9 remain unaccepted.

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

## M3 Standalone delivery and operability

Schedule these only after M2 acceptance, one slice at a time:

1. **Source-to-release:** explicit GitHub release ownership, signed/deduplicated
   events, frozen commits and immutable artifacts. Start with a documented external
   build lane; evaluate Railpack/Nixpacks alternatives later. Source detection
   must not silently take over an existing trigger or add a mandatory builder.
2. **Stable endpoints:** optional standalone ingress with replica membership,
   health/readiness evidence, explicit domain ownership and automatic Let's Encrypt
   certificates. Allow enable/disable; account for its extra service and preserve
   routes while OAP is down. Operator-owned ingress/TLS stays native when operators
   return. No forced blue-green or claim that a bridge is a load balancer.
3. **Image and storage safety:** runtime artifact inventory, retained known-good
   releases, bounded cache/retention, reviewed pruning and named-volume backup
   procedures. Protect active/stopped workloads and recovery artifacts. App-data
   restore is separately authorized and verified; metadata restore is not data restore.
4. **One-person operability:** offline-compatible rescue, secret/key recovery,
   backup checks, bounded metadata growth, retries/timeouts and measured resource
   regression checks. Add optional external secret providers only for a verified need.

Exit: one source change becomes a verified standalone release with a stable
endpoint, documented recovery and measured operating cost. Optional ingress and
build services have separate resource accounting. This is not a multi-server gate.

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
in M2.9; revise goals explicitly if evidence requires it. Bound polling, log reads,
retries, concurrency and retained metadata. OAP failure must not stop existing
containers or routes. Do not install monitoring infrastructure just to benchmark.

## Deferred requirements and their return gates

| Area | Later scope / return gate |
| --- | --- |
| UI and generated UI | Parked. Revisit after M2/M3 headless workflows; render shared status/topology/evidence/approval models, keep authority server-side. Existing functional/security fixes remain allowed |
| Coolify, Dokploy, Dokku, Portainer | M5 after owner reprioritization: preserve native configuration, resource discovery/grouping, capability contracts and operator-specific acceptance; no adapter expansion now |
| AI-assisted discovery and application assembly | M5: deterministic proposals first, explicit ownership/handoff; no implicit adoption |
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
| Rolling and blue-green | Optional later policy after stable endpoints/verification; controlled by owner, not enforced |

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
