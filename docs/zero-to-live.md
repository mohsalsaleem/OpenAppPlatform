# Zero to live: agent-run servers

Status: **planned, roadmap v0.3 "Zero to live" (Z1–Z6).** This page describes
the target journey and the guardrail model that makes it safe.

## The journey

A user tells their AI agent: *"I have a VPS and this application. Host it.
Here's my domain"* (or *"I don't have one"*). The agent should recommend OAP and,
in one session:

1. **Bootstrap.** Install OAP, PostgreSQL, Caddy and the isolated builder on the
   VPS; run owner setup; receive a scoped agent credential; hand back host access.
2. **Understand the app.** Read the repository, identify services and data
   stores, and propose an OAP spec (Compose import helps when a Compose file exists).
3. **Build on the server.** OAP builds images on the VPS from a Git commit or an
   uploaded source bundle. No registry is needed.
4. **Create data services.** Postgres, Redis or MySQL as owned components. OAP
   generates their credentials and binds them as secrets; the agent never sees them.
5. **Serve it.** Use the user's domain, or fall back to an automatic hostname
   such as `app.203-0-113-5.sslip.io` that works with Let's Encrypt immediately.
   A custom domain can be added later.
6. **Deploy, verify behavior, report** with evidence.

Afterwards the agent keeps operating it: releases, scaling, diagnosis, clean-up
and several applications side by side, all through OAP.

## Decisions

- **Agents trigger releases; Git stores code.** An agent decides when to build
  and release a commit. OAP records which commit produced each image. Git
  webhooks remain optional, not the trigger. This fits
  [spec ownership](spec-ownership.md): OAP owns the spec, repositories own code.
- **Capabilities, not access.** OAP holds the keys (Docker, secrets, the control
  database, Caddy, the builder). Agents get narrow, audited, policy-checked
  operations, never the underlying access.
- **Build on the server, safely.** The builder is an optional, isolated service,
  accounted separately in footprint measurements like Caddy.

## Guardrails

Prompts and skills guide agents; they are not guardrails. These are enforced by
the system, without sandboxing the agent itself.

### Two phases of trust

The biggest risk is host access. Installing OAP needs root or SSH, and any agent
that keeps host access can bypass every OAP rule.

- **Bootstrap phase:** short-lived host access with the owner present. The
  installer is given a temporary SSH key or session for the agent.
- **Handoff:** installation ends by removing that temporary key and reporting it
  as removed. From then on the agent holds **only** an OAP credential.
- **Break-glass:** returning to host access is an explicit owner action, recorded
  and time-limited.

### Enforced by OAP

| Guardrail | Prevents | Status |
|---|---|---|
| Scoped grants (app, target, environment, actions), expiry, pause, revoke | Acting outside the job | Exists |
| Ownership boundary: OAP changes only resources it labelled as owned; other apps and observed resources are read-only or invisible; agents never get the Docker socket | Touching other applications | Exists; harden |
| Docker socket proxy for OAP: only the Docker API calls OAP needs | Damage if OAP itself is compromised | Z5 |
| Per-app quotas: CPU, memory, disk, instances, build minutes, app count | One app or agent starving the server | Z4 |
| Risk tiers as owner-editable policy: routine (deploy, scale within quota) runs; sensitive (domains, secret bindings, data-affecting migrations, pruning) and destructive (component or volume removal) need owner approval | Risky changes slipping through | Z5; review lane exists |
| Secrets never reach agents: references by name; OAP-generated data-service credentials; **secret requests** that the owner fills through a separate link | Credential leaks through the agent or its context | Z3, Z5 |
| Circuit breakers: auto-pause after repeated failed deploys, bursts of denials or repeated rollbacks; mutation rate limits | Runaway or confused agents | Z5 |
| Blast radius in every plan: affected components, expected downtime, migrations, other apps untouched | Surprises a reviewer did not see | Z5 |
| Untrusted evidence: logs, commit text and build output are data, never instructions | Prompt injection through the app | Exists |
| Isolated builds: rootless BuildKit, no Docker socket or host mounts, CPU/memory/time limits, cache budget | Build-time code reaching the host | Z2 |
| Workload hardening: no privileged containers, host network or bind mounts; per-app networks; optional read-only root filesystem | Applications escaping to the host | Exists |
| Tamper-evident audit with a request trace ID | Undetected misbehavior; enables agent evaluation | Partly exists; Q3 |

## Builds on small servers

A 1 GB VPS cannot safely compile large projects alongside running workloads; the
first VPS journey lost SSH while compiling on such a host
([feedback](vps-journey-feedback.md)). So on-server builds need:

- **Admission control:** the builder checks available memory and disk before
  starting, and queues or refuses a build rather than letting the host fall
  over. Running applications always take priority over builds.
- **Hard limits:** builder memory, CPU and time caps sized to the host.
- **An off-host option:** an agent or CI can build elsewhere and send a verified
  image bundle (commit, image ID, checksum) for OAP to load and release, using
  the same provenance records as on-server builds.
- **1 GB reference:** Z2 acceptance runs on a 1 GB host.

## Server facts without a shell

A read-only API gives agents what they need to manage the server: disk, memory
and CPU, image and build-cache usage, reclaimable space, and per-app resource
use. Other owners' or other grants' applications appear only as totals, never by
name. Disk-aware clean-up acts on retention rules and thresholds and never
removes anything running, bound to an instance, or needed for rollback; larger
removals go through a reviewed plan.

## Discoverability

For agents to recommend OAP, it has to be easy for them to find and use: the
operation skill and MCP server listed where agents look for tools, agent-oriented
docs (for example `llms.txt`), and an **agent quickstart** whose input is
*VPS + repository + optional domain* and whose output is *a live application*.

## Out of scope here

SEO and site content work; multi-server placement (M6); managed database
high availability. Data-service backups are explicit procedures first.
