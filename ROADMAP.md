# Open App Platform Roadmap

Updated 14 October 2026. This document is the authoritative execution order.
Slice IDs (M3.3a, M3.4, …) are stable references used across the docs; the
release grouping below decides the order. Earlier queues, gate receipts and
acceptance notes are archived in
[the 14 October snapshot](ROADMAP-history-through-2026-10-14.md) and
[the 13 October snapshot](ROADMAP-history-through-2026-10-13.md).

## Direction

A lightweight, self-hosted application platform for **one owner and the AI
agents they trust**, on one server, with **standalone Docker as the primary
execution path** (see [positioning](docs/positioning.md) and the
[operating model](docs/operating-model.md)).

- **The application is the unit.** One specification describes every component;
  deploy, scale, release, rollback and health apply to the whole application.
- **Agents are the operator.** Edit spec → validate/diff → plan → apply → verify,
  under bounded, server-enforced authority. The owner sets trust and reviews risky
  changes.
- **Evidence before exposure.** Changes earn recorded evidence before users
  depend on them. Environments are a tool for that, not a required chain
  (see [environments and verified change](docs/environments.md)).
- **Zero to live.** An agent given a VPS, an application and optionally a domain
  should install OAP, build, create data services, deploy and serve it in one
  session, then keep operating it (see [zero to live](docs/zero-to-live.md)).
  Agents trigger releases; Git stores code.
- **Capabilities, not access.** OAP holds the keys (Docker, secrets, control
  database, proxy, builder); agents get narrow, audited, policy-checked operations.
  Host access exists only during bootstrap and is handed back.
- **Small and boring.** One Go service, PostgreSQL/River, Docker Engine, and an
  optional Caddy proxy and isolated builder. Far smaller than Kubernetes; never a
  re-implementation of it.

AI interface means deterministic APIs, a structured CLI, MCP tools and versioned
skills. No embedded model, chatbot or inference subscription is required; core
operation works with AI disabled and through ordinary scripts.

**Parked:** UI expansion (retain the dashboard and essential owner controls; fix
security or functional regressions only) and operator expansion (preserve Coolify
compatibility and native ownership; standalone acceptance never requires Coolify).

## Product and engineering rules

Agents operate only through documented product interfaces. The owner establishes
trust and issues bounded grants; an operating agent never creates or holds owner
authority. Acceptance journeys use one management interface (CLI, API or MCP)
throughout; application traffic probes are not a second management interface.
Missing primitives are product gaps to fix, not permission to use raw metadata,
installer secrets, private endpoints or improvised host changes.

- OAP owns each application specification and its version history; service
  repositories release only their own component's immutable image. No repository
  or Git host is a required or second writable source
  ([spec ownership](docs/spec-ownership.md)).
- One lifecycle implementation serves REST, CLI, MCP and the retained UI. CLI and
  MCP submit and observe durable controller operations; client interruption never
  becomes the source of deployment truth.
- One owner and named agents with bounded grants and an audit trail. No enterprise
  tenancy or agent fleet.
- Two phases of trust: an agent may hold host access only during an owner-present
  bootstrap; installation ends by removing it, and operating agents hold only OAP
  credentials. Prompts and skills are guidance, never guardrails.
- Secrets never reach agents. Agents reference secrets by name; OAP generates
  data-service credentials; human-held secrets arrive through owner-filled requests.
- Builds are untrusted code: they run only in the isolated builder, never with the
  Docker socket or host mounts.
- Authorization is enforced by the server, never by prompts or skills. Routine
  reversible actions may run under explicit owner policy; risky actions need exact
  review. Agents cannot self-approve or write OAP's control database.
- Save, plan, execute and verify stay distinct. HTTP 202 means admission, not
  deployment success or application correctness.
- Observe existing resources without taking ownership. Preserve native builds,
  triggers, configuration, routes, volumes and secrets for operator resources.
- Standard deployment may cause downtime until guarded rollout (M3.3a) ships.
  Blue-green stays optional.
- Ship breadth before depth: one complete workflow per slice, explicit limits,
  meaningful tests and reproducible evidence before expanding it.
- Run the exact-tree local gate before pushing. Automatic Git CI stays disabled.
  Record each passing gate ID in [v0.1 test evidence](docs/v0.1-test-evidence.md)
  or the slice's acceptance doc.

## Where we are

| Milestone | Status | Evidence |
|---|---|---|
| M0–M1: foundation, security and visibility | Complete | [Archived roadmap](ROADMAP-history-through-2026-10-13.md) |
| M2: agent-operated standalone core (CLI, headless bootstrap, portable spec, incident evidence, dogfood, footprint) | Complete at `76b4061` | Gate `20261014-000048-91413718` |
| M3.1: application router and scale loop (Caddy, spec routes, 1 → 3 → 1 scaling, public ACME) | Functionally complete at `1a2344d` | Gate `20261014-094928-0ce51041`, Vultr acceptance at `bac0f62`, [standalone ingress](docs/standalone-ingress.md) |
| M3.2 v0.1 scope: component add/remove, pre-deploy jobs, app-wide image rollback | Complete at `3ac90b1` | Full local acceptance (gate ID to record), [topology](docs/topology-lifecycle.md), [pre-deploy jobs](docs/pre-deploy-jobs.md) |
| M3.2 remaining scope: spec history, diff, revert, export | Deferred to v0.2 (M3.2d) | — |

What the tests already prove, and where, is mapped in
[v0.1 test evidence](docs/v0.1-test-evidence.md).

## Active: v0.1 release closeout

v0.1 is **a multi-component application on one server, operated end to end by an
owner-authorized agent through the CLI**. Feature work for v0.1 is done; what
remains is proving it as one journey, documenting it accurately and cutting the
release. Do these in order. The real VPS journey must target **1 GB RAM**;
constrain the test and move candidate builds off-host rather than requiring a
larger runtime server. See [VPS journey feedback](docs/vps-journey-feedback.md)
for the blocked attempt and acceptance requirements. The runbook is revised
(off-host bundle, memory budget, host monitor); the 1 GB run is not yet validated.

| # | Task | Done when |
|---|---|---|
| 1 | **Record existing evidence** | Gate ID for `3ac90b1` added to [test evidence](docs/v0.1-test-evidence.md) |
| 2 | **One continuous release journey** ([runbook and fixtures](tests/vps/release-journey/README.md)): **locally first**, then **once on a 1 GB VPS** by the testing agent | Full CLI journey with one named agent and one reference app (web, api, notes with a volume, worker, migration job): create → deploy with migration → routed through Caddy → scale api 1 → 3 with traffic on every instance → failing migration and broken release → `incident` identifies them → correct → app-wide image rollback (migration not re-run, volume data kept) → scale 3 → 1 with no failed requests. Plus REST and MCP smoke checks (validate, create, deploy, replay, incident, one denial), coexistence, controller outage and host health (no OOM). Full REST/MCP journeys, parity, the isolation matrix and pause/revoke across interfaces move to Q1/Q3. Writes `release-journey-cli.json`, `smoke-rest.json`, `smoke-mcp.json` and `v0.1-journey.json` |
| 3 | **Disk safety for small servers** | Documented, tested manual procedure to remove old pre-deploy job containers and unused images without touching active, stopped-but-bound or rollback-target artifacts; listed in known limits |
| 4 | **Public docs match reality** | README "What works today", "Next" and scale-loop text; `overview.svg` and `scale-loop.svg` no longer say "planned M3.1"; [v0.1 release notes](docs/releases/v0.1.md) list included features and known limits; [standalone ingress](docs/standalone-ingress.md) and the roadmap agree on multi-app aggregation status |
| 5 | **Release candidate gate** | Fresh exact-tree `make test-local` passes on the final tree, including step 2; gate ID recorded |
| 6 | **Public install on the release tree** | The step 2 journey run at the release commit with `--ingress-tls letsencrypt` covers this; otherwise repeat the [public acceptance](docs/standalone-public-acceptance.md) |
| 7 | **Cut v0.1** | Tag, pinned OAP and Caddy image digests, upgrade from an M2.9-era install tested, release notes published |

**Known limits to publish with v0.1:** standard deploys can cause downtime;
one server is a single failure boundary; automated application-data backup is
not included; idle CPU is above the 1% goal (Caddy reference 2.67% of one core);
scheduled certificate renewal is configured but not yet observed; old job
containers and images are not pruned automatically.

**Not v0.1 blockers:** observing the first scheduled certificate renewal (expected
around mid-December 2026; schedule a check), the idle CPU gap (M3.7), routing
hardening (installed domain-authority and port handoffs, extended drain testing),
and everything below.

## After v0.1

### v0.2 — Safer changes

| Slice | Scope | Acceptance |
|---|---|---|
| **M3.3a Declared checks and guarded rollout** | Spec-declared HTTP and worker checks run by OAP and recorded against the exact release; one-instance-first rollout through the router; automatic rollback on a failed check; a bounded post-release health window | A release rolls out one instance first and completes; an injected failing check rolls back automatically; evidence is recorded and visible to the agent |
| **M3.6a Retention and pruning** | Automatic, bounded retention of job containers and unused images; protect active, stopped-but-bound and rollback-target artifacts; reviewed pruning plans. Foundation for Z4 | Disk use stays bounded across 50 releases; rollback targets survive pruning |
| **M3.2d Spec history** | Author (owner or named agent), time and message per version; history, masked version diff, revert as a new plan; one-way `spec export --dir` | An agent reverts a topology change through a plan; history shows who changed what; export round-trips through validate/diff without secret values |
| **M4.1 Owner notifications** | Durable, deduplicated owner notifications for automatic rollback, operations needing attention, and paused or denied agents; off until configured. Also delivers Z3/Z5 secret requests and approval requests | Each event type is delivered once to a configured channel; delivery failure never blocks operations |

### v0.3 — Zero to live

The agent-run server journey and its guardrails; design in
[zero to live](docs/zero-to-live.md). M3.4's external build lane is replaced by
on-server builds (Z2); its component-scoped release grants remain.

| Slice | Scope | Acceptance |
|---|---|---|
| **Z1 Bootstrap and handoff** | Agent-guided install on a fresh VPS (OAP, PostgreSQL, Caddy, builder); owner setup; agent grant; installer removes the temporary host key and reports it; break-glass re-entry is an explicit, time-limited owner action. Read-only **server facts** API (disk, memory, CPU, images, build cache, reclaimable space, per-app usage; other apps only as totals). Fallback hostname via `sslip.io` when no domain is given | An agent goes from a fresh VPS to a live HTTPS app with no domain supplied; after handoff its old host key no longer works; server facts never name another grant's apps |
| **Z2 On-server builds** | Optional isolated builder (rootless BuildKit, no Docker socket or host mounts, CPU/memory/time limits, cache budget) with a one-worker durable build queue on small hosts, limited parallelism inside each build, memory and disk admission control and lower priority than workloads; off-host built image bundles accepted as an alternative; input is a Git commit or an uploaded source bundle; output is a local image with commit provenance; component-scoped release grants (from M3.4); agent-triggered releases, Git webhooks optional | On a **1 GB** host: an agent builds and releases three components from one commit while apps keep serving; a build that would exhaust memory is queued or refused, not run; an off-host built bundle loads and releases with the same provenance; a hostile Dockerfile cannot reach the host or Docker socket; a component credential cannot build or release another component |
| **Z3 Owned data services** | Postgres, Redis and MySQL as owned single-instance components with retained volumes; OAP-generated credentials bound as secrets; dependants wired by reference; documented backup and restore procedure | An app with Postgres and Redis deploys with credentials never appearing in any agent-visible output; data survives app releases and rollback |
| **Z4 Quotas and disk management** | Per-app quotas (CPU, memory, disk, instances, build minutes) and app count per grant; disk-aware clean-up of images, build cache and old job containers under thresholds, extending M3.6a | One app cannot exceed its quota; under disk pressure clean-up frees space without touching running, bound or rollback-target artifacts |
| **Z5 Policy and safety rails** | Owner-editable risk tiers (routine, sensitive, destructive); secret requests filled by the owner through a separate link; circuit breakers and mutation rate limits; blast radius in every plan; Docker socket proxy limiting OAP's own Docker API use | A sensitive change waits for owner approval; repeated failed deploys auto-pause the agent; a secret request never exposes the value to the agent; OAP's Docker access is limited to its allowlist |
| **Z6 Agent discoverability** | Operation skill and MCP server published where agents look for tools; agent-oriented docs (`llms.txt`); an agent quickstart from *VPS + repository + optional domain* to a live app | A fresh agent with only the quickstart completes the Z1–Z3 journey (measured as a Q3 scenario) |
| **M3.5 Compose import** | Proposed spec from `docker-compose.yml` with explicit decisions and unsupported fields ([proposal](docs/compose-import.md)); moved here because agents use it to understand existing apps | The example imports, validates and deploys after decisions are resolved, with no secret values in output |
| **M3.8 Remote agent access** | Authenticated HTTPS API and MCP for agents outside the server (cloud agents, CI), served through the installed Caddy; never exposes Docker or the control database; no mandatory tunnel | A remote agent completes the release journey over HTTPS with the same grants and denials as a local one |

### v0.4 — Verified change and operability

| Slice | Scope | Acceptance |
|---|---|---|
| **M3.3b Previews and promotion** | Temporary environments from production's release plus one change, own route, test secrets, seeded data, cap and expiry; optional rule that production only receives digests that passed in a preview; urgent-fix fast lane with reason and owner approval | A preview runs checks and is cleaned up on expiry; promotion of an unverified digest is refused when the rule is on |
| **M3.3c Environment comparison** | Optional long-lived environments with per-environment overrides; masked comparison sorted into declared, ahead and drift | Comparing two environments reports all three groups without secret values |
| **M3.6b Storage safety** | Runtime artifact inventory, retained known-good releases, automated named-volume and data-service backups; app-data restore separately authorized | A backup and restore drill on the reference app, including its database |
| **M3.7 One-person operability** | Offline rescue, secret/key recovery, backup checks, bounded metadata growth, idle CPU work toward the 1% goal | Measured footprint regression checks in the gate |

## QA track

Runs alongside the product releases; see the [QA strategy](docs/qa-strategy.md).
Testing is **local-first**: integrity tests and agent evaluations run in the
isolated local environment. A real VPS is used once per release candidate for
what only real infrastructure proves (public ACME and DNS, real small-host
behavior, renewal).

| Slice | Scope | Acceptance |
|---|---|---|
| **Q1 Integrity checker** (with v0.2; includes the cross-interface isolation matrix and pause/revoke checks deferred from v0.1) | Read-only, test-only checker comparing OAP's records with Docker, Caddy, audit and interface outputs; invariants for records vs reality, routes vs health, no double dispatch, audited mutations, canary-secret leaks, scope isolation, data survival, no early success and exact plans; fault scenarios through CLI, REST and MCP | The existing gate scenarios run the checker after every step; each invariant has a scenario that deliberately breaks it and is caught |
| **Q2 Agent-facing environment** (with v0.2) | `qa-env up/reset/down` with `local`, `small` and `vps` profiles returning one JSON context; cached images and a post-install snapshot | `up` under 5 minutes from nothing, `reset` under 1 minute, identical results across repeated runs |
| **Q3 Agent evaluations** (with v0.3; Z6 is measured by it; includes full REST/MCP journeys and interface parity deferred from v0.1) | Scenario library, client transcript plus server audit trace joined by a request trace ID, outcome/process/honest-claims grading mapped to the operating-model principles, results per OAP version and per agent | Every scenario in the library runs unattended for at least one agent, with a report citing evidence for each grade |

## Later milestones

| Milestone | Position |
|---|---|
| **M4 Bounded application insight** | Thin slices pulled forward: post-release health window (in M3.3a) and owner notifications (M4.1). Indexed logs, metrics, analytics and telemetry remain deferred. |
| **M5 Operator integration return** | Parked until the owner reprioritizes. Nothing in the current direction depends on it. |
| **M6 Additional servers and runtimes** | Deferred. First trigger: running previews or environments on a second host. |
| **UI** | Parked. Revisit once v0.2 workflows exist; render shared status, evidence and approval models with authority kept server-side. |

## Lightweight requirement

Measure the OAP service, its dedicated PostgreSQL, the optional Caddy proxy and
the optional builder separately from workloads and builds. Reference profile: one declared server,
10 applications / 50 components. Exercise idle, agent observation, deployment
burst, target outage and controller recovery. Goals: ≤256 MiB service RSS,
≤512 MiB service plus PostgreSQL at idle, ≤1% of one core average idle CPU. The
[reference measurement](docs/footprint.md) meets the memory goals and misses the
idle CPU goal; keep the goal and track the gap in M3.7. OAP failure must not stop
existing containers or routes. Do not add monitoring infrastructure to benchmark.

## Deferred requirements and their return gates

| Area | Return gate |
| --- | --- |
| Git-sourced specifications | After M3.2d: one repository/path per app as the spec source; commits become proposals OAP plans and reviews; exactly one authority per app; no bidirectional sync |
| Blue-green and weighted traffic | After M3.3a; optional owner policy, never enforced |
| Tailscale, OAuth, ChatGPT apps/plugins | After M3.8; never expose Docker or the control database; no mandatory tunnel |
| Embedded inference and BYOK models | Optional after deterministic diagnosis is proven useful; AI can always be disabled |
| Skills | Versioned standalone skill exists; client-specific packaging later |
| Database guardrails | Enforced throughout. Owner-enabled application query tools need least-privilege identity and bounded results first |
| Destructive actions | Data deletion, destructive migrations, rotations and pruning need separate exact authority and recovery checks |
| Indexed logs, metrics, analytics, telemetry | M4; opt-in, bounded retention, local diagnosis always available |
| AI-assisted discovery and assembly | M5; deterministic proposals first, no implicit adoption |
| Security | Docker limits, ownership and no-new-privileges preserved; no privileged/bind/host-network generalization. Build isolation and commit provenance arrive with Z2; image signing and vulnerability scanning stay later explicit gates |
| Multi-server, Firecracker, other runtimes | M6 research; optional, never core dependencies |

## Completion discipline

Each slice records implemented behavior, unsupported cases, a runnable example,
meaningful unit/system coverage, reproducible headless end-to-end evidence and an
acceptance receipt, plus the gate ID. Once Q1 lands, each slice also adds its
integrity invariants; once Q3 lands, at least one agent scenario. Test Docker networking, data preservation,
scope and policy denial, idempotent retries and crash/unknown outcomes where
relevant. Never mark an operation healthy only because a provider call succeeded.

When a slice passes, update this roadmap and take the next one. Do not expand a
parked item because part of it is convenient. A priority change needs an explicit
roadmap update.
