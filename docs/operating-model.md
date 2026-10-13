# Operating model, principles and guardrails

OAP is built for one owner and the AI agents they trust to run their
applications. This page covers who does what, the principles behind the design,
what an agent gets, and the guardrails that keep it within bounds. For command
details, see the [standalone agent runbook](standalone-agent-runbook.md).

## Principles

1. **The application is the unit.** Components that make up one system are
   defined, released, scaled, rolled back and diagnosed together.
2. **Agents operate; owners decide.** Routine work runs through the agent. The
   owner sets authority, reviews risky changes and can stop the agent at any time.
3. **The server enforces authority.** Permissions live in OAP, not in prompts,
   skills or client code. A clever or confused agent cannot talk its way past them.
4. **Declarative and reviewable.** Changes start as a spec edit, become a stored
   plan with a hash, and run only as that exact plan.
5. **Each step means one thing.** Saving, planning, executing and verifying are
   separate. Saving never deploys. HTTP `202` means accepted, not succeeded.
6. **Durable truth.** Every operation is recorded server-side and survives client
   disconnects and controller restarts. An agent reconnects and asks; it never
   guesses.
7. **Never repeat an uncertain action.** If OAP can't tell whether a dispatch
   happened, the operation goes to attention instead of being retried.
8. **Honest evidence.** State is `healthy`, `issue` or `inconclusive`, with
   freshness and missing data stated explicitly. A healthy process is not proof
   the application works.
9. **Small and boring.** One Go service, PostgreSQL and Docker Engine. No
   required model runtime, queue, registry or cluster. Apps keep running when OAP
   is down.
10. **AI is optional.** Everything an agent does is a deterministic API call a
    script can make too. OAP embeds no model.

## Who does what

| Role | Responsibilities | Uses |
|---|---|---|
| **Owner** | Installs OAP, issues and scopes agent grants, approves risky plans, pauses or revokes agents, owns host access and backups | Owner session (CLI or dashboard), install and rescue scripts |
| **Agent** | Edits app specs, validates and plans changes, deploys, waits on results, verifies behavior, diagnoses incidents and proposes fixes | Named agent credential via JSON CLI, MCP or REST |
| **OAP** | Checks every request, freezes releases, dispatches once, observes health, records audit, serves bounded evidence | Go service + PostgreSQL |
| **Docker Engine** | Runs the containers, networks and volumes | Local socket, used only by OAP |

The agent also keeps its normal development tools. Changing source code,
running tests and building images happen outside OAP, in the agent's own
environment; OAP takes the resulting immutable image.

## What the agent gets

**Interfaces**
- A [JSON CLI](agent-cli.md) where every command returns one JSON envelope with
  stable error codes and exit codes. `oap help` and `oap schema` work offline.
- MCP over stdio, with lifecycle tools only when explicitly started with
  `--operate`, plus an optional read-only remote MCP.
- The REST API with its [OpenAPI contract](openapi.json).
- A versioned [operation skill](../skills/open-app-platform/SKILL.md) and runbook.

**Contracts**
- One portable spec (`oap.dev/v1alpha1`) for the whole application.
- Validate and diff before saving; diffs mask secret values.
- Stored, hashed action plans for create, configure, deploy, restart and recover.
- Operation handles to `wait` on, with bounded waits and idempotent retries.
- Effective capabilities: what this credential may do on this target right now.

**Evidence**
- Current health per instance and bounded logs.
- An [incident view](incident-diagnosis.md) that combines runtime observations,
  recent operations, masked release changes, events and an optional redacted
  log sample, with freshness and missing data stated.
- Explicit verification criteria, including a reminder that application
  behavior must be tested separately.

## Guardrails

![Guardrails on every agent request](images/guardrails.svg)

Every agent request passes the same server-side checks:

1. **Identity.** The credential must be live: not expired, paused or revoked.
   Workers recheck it before any runtime change, not just at admission.
2. **Scope.** Grants are tied to a target and environment, a set of actions
   (`configure`, `deploy`, `restart`, `recover`) and an app limit. An onboarding
   grant can only see and manage the apps it created, and expires after 24 hours.
3. **Freshness.** Changes carry the expected definition version, so stale plans
   are rejected. Deploy plans require immutable image references.
4. **Policy.** Routine actions run under the owner's grant. Plans marked for
   review run only after the owner approves the exact plan hash in an owner
   session. An agent cannot approve its own plans.
5. **Safety.** Idempotency keys make retries return the original result.
   Component claims stop conflicting operations from overlapping. Dispatch
   happens once; an uncertain outcome goes to attention.

Additional protections:

- **Out of reach:** the control database, host shell, issuing or widening
  grants, owner settings, adopting existing resources, provisioning databases
  and privileged containers.
- **Untrusted content stays data.** Logs, image references, commit metadata and
  release text are marked `untrustedEvidence`. Instructions inside them are never
  executed, and never count as authority.
- **Secrets stay masked.** Literal environment values, managed runtime and
  registry secrets are masked in diffs and incident views; sampled logs are
  redacted before they are returned.
- **Bounded reads.** Incident requests have deadlines, size limits and per-
  credential rate limits.
- **Audit and control.** Every mutation is recorded with its credential. The
  owner can pause or revoke an agent at any time; new work is denied immediately.

## Limits

Guardrails cover the OAP API. They do not cover:

- **Host or Docker administration.** Anyone with root or Docker socket access can
  bypass OAP. Never give that access to an otherwise restricted agent.
- **Work already dispatched.** Pausing or revoking stops new work; a container
  change already sent to Docker may still complete.
- **Application correctness.** OAP verifies runtime health, not business logic.
  The agent must test the app's real behavior.
- **Data.** Rollback restores images, not schemas or data. Data restore is a
  separate, explicitly authorized procedure.
