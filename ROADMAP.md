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
- **Small and boring.** One Go service, PostgreSQL/River, Docker Engine and an
  optional Caddy proxy. Far smaller than Kubernetes; never a re-implementation of it.

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
release. Do these in order.

| # | Task | Done when |
|---|---|---|
| 1 | **Record existing evidence** | Gate ID for `3ac90b1` added to [test evidence](docs/v0.1-test-evidence.md) |
| 2 | **One continuous release journey** on a real VPS ([runbook and fixtures](tests/vps/release-journey/README.md)), run by the VPS testing agent | One named agent, one reference app (web, api, notes with a volume, worker, migration job; linked PostgreSQL stays covered by the local gate), CLI only: create → deploy with migration → routed through Caddy → scale api 1 → 3 with traffic on every instance → inject a failing release → `incident` identifies it → correct via plan → app-wide image rollback (migration not re-run, volume data kept) → `verify` plus HTTP behavior checks → scale 3 → 1 with no failed requests. Writes `release-journey.json` |
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
| **M3.6a Retention and pruning** | Automatic, bounded retention of job containers and unused images; protect active, stopped-but-bound and rollback-target artifacts; reviewed pruning plans | Disk use stays bounded across 50 releases; rollback targets survive pruning |
| **M3.2d Spec history** | Author (owner or named agent), time and message per version; history, masked version diff, revert as a new plan; one-way `spec export --dir` | An agent reverts a topology change through a plan; history shows who changed what; export round-trips through validate/diff without secret values |
| **M4.1 Owner notifications** | Durable, deduplicated owner notifications for automatic rollback, operations needing attention, and paused or denied agents; off until configured | Each event type is delivered once to a configured channel; delivery failure never blocks operations |

### v0.3 — Real-world workflow

| Slice | Scope | Acceptance |
|---|---|---|
| **M3.4 Source-to-release and component grants** | Component-scoped release grants; explicit GitHub release ownership; documented external build lane; no silent takeover of existing triggers | Three repositories release three components of one app independently; each credential is denied on the others |
| **M3.8 Remote agent access** | Authenticated HTTPS API and MCP for agents outside the server (cloud agents, CI), served through the installed Caddy; never exposes Docker or the control database; no mandatory tunnel | A remote agent completes the release journey over HTTPS with the same grants and denials as a local one |
| **M3.3b Previews and promotion** | Temporary environments from production's release plus one change, own route, test secrets, seeded data, cap and expiry; optional rule that production only receives digests that passed in a preview; urgent-fix fast lane with reason and owner approval | A preview runs checks and is cleaned up on expiry; promotion of an unverified digest is refused when the rule is on |

### v0.4 — Adoption and operability

| Slice | Scope | Acceptance |
|---|---|---|
| **M3.5 Compose import** | Proposed spec from `docker-compose.yml` with explicit decisions and unsupported fields ([proposal](docs/compose-import.md)) | The example imports, validates and deploys after decisions are resolved, with no secret values in output |
| **M3.3c Environment comparison** | Optional long-lived environments with per-environment overrides; masked comparison sorted into declared, ahead and drift | Comparing two environments reports all three groups without secret values |
| **M3.6b Storage safety** | Runtime artifact inventory, retained known-good releases, named-volume backup procedures; app-data restore separately authorized | A volume backup and restore drill on the reference app |
| **M3.7 One-person operability** | Offline rescue, secret/key recovery, backup checks, bounded metadata growth, idle CPU work toward the 1% goal | Measured footprint regression checks in the gate |

Move M3.5 earlier if outside users are a near-term goal; it is the easiest on-ramp.

## Later milestones

| Milestone | Position |
|---|---|
| **M4 Bounded application insight** | Thin slices pulled forward: post-release health window (in M3.3a) and owner notifications (M4.1). Indexed logs, metrics, analytics and telemetry remain deferred. |
| **M5 Operator integration return** | Parked until the owner reprioritizes. Nothing in the current direction depends on it. |
| **M6 Additional servers and runtimes** | Deferred. First trigger: running previews or environments on a second host. |
| **UI** | Parked. Revisit once v0.2 workflows exist; render shared status, evidence and approval models with authority kept server-side. |

## Lightweight requirement

Measure the OAP service, its dedicated PostgreSQL and the optional Caddy proxy
separately from workloads and builds. Reference profile: one declared server,
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
| Security | Docker limits, ownership and no-new-privileges preserved; no privileged/bind/host-network generalization; build isolation and image provenance only by later explicit gates |
| Multi-server, Firecracker, other runtimes | M6 research; optional, never core dependencies |

## Completion discipline

Each slice records implemented behavior, unsupported cases, a runnable example,
meaningful unit/system coverage, reproducible headless end-to-end evidence and an
acceptance receipt, plus the gate ID. Test Docker networking, data preservation,
scope and policy denial, idempotent retries and crash/unknown outcomes where
relevant. Never mark an operation healthy only because a provider call succeeded.

When a slice passes, update this roadmap and take the next one. Do not expand a
parked item because part of it is convenient. A priority change needs an explicit
roadmap update.
