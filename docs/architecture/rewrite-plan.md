# Rewrite plan: tasks, acceptance and guardrails

This plan turns the [agent-native rewrite](agent-native-rewrite.md) into tasks
that any agent can pick up, in any environment, without relying on conversation
memory. The repository is the only shared state: this plan, its status table and
the rules in [`CLAUDE.md`](../../CLAUDE.md).

## How work proceeds

- **One task at a time, in order** unless the plan marks tasks as parallel. A task
  starts only when the tasks it depends on are `done`.
- **Each task ends with evidence:** the acceptance commands, run on the task's final
  commit, with their result recorded in the status table.
- **Branch:** all rewrite work lands on `rewrite`. Each phase is merged to `rewrite`
  through a pull request the owner reviews. `main` is untouched until cutover.
- **Commit trailer:** every commit names its task, for example `Task: R1.4`.

## Guardrails

These hold for every task. Breaking one is a defect, even if all tests pass.

### Scope

1. **Only what the task says.** Work not listed in the current task is out of scope.
   If it is needed, stop and propose a plan change (below); don't do it "while here".
2. **Changing the plan is a pull request to this file**, approved by the owner
   before any code relies on it. This includes adding, splitting or dropping tasks
   and editing any acceptance criterion.
3. **Architecture changes need an ADR** in `docs/architecture/adr/`, approved by the
   owner. Anything that contradicts [the rewrite doc](agent-native-rewrite.md) is an
   architecture change.
4. **The old tree is frozen.** No edits under `internal/` packages that exist on
   `main` today, `cmd/platform`, `cmd/oap`, `cmd/oap-mcp`, `cmd/oap-admin`,
   `cmd/oap-build-client` or `web/`, except security fixes the owner approves. The
   new tree uses new paths (below). R0.3 enforces this.

### Tests and acceptance

5. **Never skip, disable, weaken or delete a test or acceptance check to get green.**
   No `t.Skip` in the new tree except adapter tests gated on an explicit
   environment variable (for example `OAP_TEST_DOCKER=1`). Every gated skip is
   listed in the task's evidence.
6. **Failing first for bug fixes.** A fix for a known failure (such as VPS-H-001)
   lands after a test that reproduces it, and the evidence shows that test failing
   on the commit before the fix.
7. **Report results as they are.** A task with a failing or unrun acceptance check
   is `blocked` or `in progress`, never `done`.

### Code

8. **Layer rules** from the rewrite doc's section 12, enforced by R0.2:
   - `internal/core` imports only the standard library (minus I/O, time and
     randomness: no `os`, `net`, `io/fs`, `database/sql`, `time.Now`, `math/rand`)
     and generated message types.
   - `internal/engine` imports `core` and `ports`, never `adapters`, `service` or
     `surface`.
   - `internal/adapters` imports `ports` and generated types, never `core`
     internals, `engine`, `service` or `surface`.
   - `internal/surface` imports `service` and generated code only.
9. **Dependencies are allowlisted.** The project runs on two third-party modules
   today, and the rewrite keeps that discipline. Allowed: `connectrpc.com/connect`,
   `google.golang.org/protobuf`, `github.com/hashicorp/go-plugin` (phase 4), a
   pure-Go SQLite driver (`modernc.org/sqlite`), one YAML library
   (`gopkg.in/yaml.v3`) and their transitive dependencies. Tools (not linked):
   `buf`, `protoc-gen-go`, `protoc-gen-connect-go`, pinned in `tools/`. Anything else
   needs owner approval. Approved for the CLI only (owner, 2026-10-20): Charm's
   `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2` and `charm.land/bubbles/v2`
   for `oapctl`'s terminal interface; `tests/arch` keeps them out of `oapd` and
   `internal/`. go-plugin's API exposes two of its own dependencies,
   `google.golang.org/grpc` and `github.com/hashicorp/go-hclog`; the extension
   host imports them as part of using go-plugin (R4.1).
10. **Generated code is reproducible.** `make gen` on a clean checkout produces no
    diff. Generated files are never edited by hand.
11. **No secrets anywhere:** not in code, fixtures, logs, test output or evidence.
    R3.9 adds a scan test.

### Environments

12. **Here (cloud session):** phases 0–1 and the simulation parts of phase 2. Push
    every finished task; the container is temporary.
13. **Owner's machine (local Claude Code):** adapter contract tests against real
    Docker and Caddy, and local acceptance journeys. The owner's test server
    (`vultr_oap`, reached over SSH from the owner's machine) may also run them
    (owner decision, 2026-10-19); test binaries are cross-compiled locally, not
    built there, and the server rules below apply.
   On 2026-10-20 the owner had `oapd`'s image built on the test server,
   when local CPU emulation hung.
14. **Owner's server:** phase 5 deploy and the release journey. On the server, an
    agent touches only resources carrying OAP labels, never builds there unless the
    task says so, never edits databases or Caddy by hand, never prints credentials,
    and asks before anything destructive or outside OAP.

### Stop and ask the owner when

- an acceptance criterion can't be met as written;
- a task needs to touch packages it doesn't list;
- a decision from the rewrite doc's section 17 is needed (see "Decisions by task");
- a security or data-loss question comes up;
- a new dependency seems necessary.

## Definition of done (every task)

- [ ] Acceptance checks pass on the final commit and are recorded below.
- [ ] `make rewrite-check` passes (from R0.4 on).
- [ ] No guardrail broken; no out-of-scope changes in the diff.
- [ ] Docs touched by the task are updated (rewrite doc, this plan's status).
- [ ] Pushed to the remote.

## Phase 0: Branch and guardrails

| ID | Task | Acceptance |
|---|---|---|
| R0.1 | Create `rewrite` from `main` with the rewrite docs. Add empty new-tree paths: `proto/oap/v1`, `gen/`, `internal/{core,engine,ports,adapters,service,surface}`, `cmd/oapd`, `cmd/oapctl` (the new CLI's working name until cutover) | `go build ./...` and the existing `go test ./...` (without database-backed tests) pass unchanged |
| R0.2 | Architecture test (`tests/arch`) enforcing the layer rules of guardrail 8 with `go list -deps` | Passes on the tree; a commit-local experiment adding a forbidden import (for example `os` in `internal/core`) makes it fail, shown in the evidence and not committed |
| R0.3 | Frozen-path check: fails if the diff from the merge base with `main` touches frozen paths, unless a commit carries `Owner-Approved: security` | Passes on the tree; a trial edit to `internal/controller` fails it |
| R0.4 | `make rewrite-check`: `gofmt` check, `go vet`, `go test -race` for the new tree, the architecture test, the frozen-path check, `buf lint` (once protos exist) and the generated-code diff check. No CI workflow for now: GitHub CI is disabled (owner decision, 2026-10-19); run the target before every push | Target passes locally |
| R0.5 | `CLAUDE.md` with the rules agents follow on this branch | File exists and points to this plan |

## Phase 1: Schema and core

Depends on phase 0. Runs entirely here.

| ID | Task | Acceptance |
|---|---|---|
| R1.1 | Protos: `world.proto`, `agent.proto` (seven RPCs), `journal.proto`, `envelope.proto`, error details (`why` codes, remedy). `buf.yaml`, `buf.gen.yaml`, pinned tools, `make gen` | `buf lint` clean; `make gen` reproducible; JSON golden tests show the agreed conventions: field names as written, enums as strings, explicit presence, no int64 except where documented |
| R1.2 | Desired-state format: strict YAML subset, JSON Schema validation, canonical form, content hash; converter from today's portable spec | Property test: parse → canonical → parse is identical; implicit booleans, floats, anchors and duplicate keys are rejected with `why` codes; every example spec converts and validates, except those that depend on Coolify-only resource adoption, which fail with `convert.unsupported` (amended by the owner, 2026-10-19) |
| R1.3 | World model (`core/model`): the graph from desired state plus observations, with desired, actual, health, freshness and affordance slots | Golden graphs for three reference apps (`two-components`, `topology`, `pre-deploy-jobs`), including stale observations and mismatches |
| R1.4 | Intent compiler: scale, deploy release, roll back to release, configure, add component, remove component, spec patch | Table tests per intent; an unknown or invalid intent returns a typed error with a remedy; output is a desired-state diff, never side effects |
| R1.5 | Planner: step vocabulary, ordering rules, blast radius, computed undo, reversibility | Property tests over generated topologies: (a) no plan stops an instance that is still a route member; (b) every removal runs `route-remove` → `edge-reload-confirmed` → `drain` → `stop`; (c) for reversible plans, applying undo to the result gives back the original desired state; (d) volume deletion and destructive migrations are marked irreversible. Golden plans for scale 1→3→1, deploy with pre-deploy job, rollback and component removal |
| R1.6 | Policy engine: envelope parsing; scope, invariants, risk tiers, change-fact mapping, breaker inputs; verdict with reasons | Table tests for each rule; irreversible changes always need the owner whatever the envelope says; a risk value supplied by the agent is ignored; verdicts are deterministic |
| R1.7 | Proposal identity: content hash over base revision, intent, plan, change-fact snapshot and envelope version | Changing any input changes the hash; re-evaluating a stored proposal reproduces its verdict exactly |

**Phase exit:** `make rewrite-check` green; the architecture test shows `core` has
no I/O imports; the owner reviews the phase pull request.

## Phase 2: Engine, storage and simulation

Depends on phase 1. R2.1–R2.8 run here; R2.9–R2.11 need real Docker and Caddy.

| ID | Task | Acceptance |
|---|---|---|
| R2.1 | Ports: `Runtime`, `Edge`, `Builder`, `DesiredRepo`, `OpStore`, `Secrets`, `FactSource`, `Notifier`, `Clock` | Interfaces only; the architecture test confirms nothing else lives in `ports` |
| R2.2 | SQLite operational store: schema, migrations, journal with monotonic revisions, WAL | Crash-consistency test with faults injected at the storage port; reopened databases always pass integrity checks; revisions never go backwards |
| R2.3 | Job queue on SQLite: leases, attempts, single dispatch | Randomized test (at least 10,000 seeded runs, `-race`) with concurrent dispatchers and lease expiry: no job runs twice concurrently, none is lost |
| R2.4 | Step executor: intent before effect, observe before re-dispatch | In simulation, a crash between every pair of steps of every golden plan converges with no duplicate effects (fake runtime counts creations) |
| R2.5 | Per-application serializer | Concurrent applies on one app run in order; different apps run in parallel; property test over random schedules |
| R2.6 | Simulation harness: fake Runtime and Edge with a traffic generator, fault injection and seeds; in-memory `DesiredRepo` and `Secrets`; a contract suite per port that both the fakes and the real adapters must pass | The same seed gives an identical trace; faults include reloads that drop in-flight connections, exits during drain, slow readiness and lost responses; every fake passes its port's contract suite |
| R2.7 | Observer: runtime events, periodic resync, freshness | In simulation, missed events are healed by resync within one interval; freshness is reported on every node |
| R2.8 | VPS-H-001: reproduce, then fix | Commit 1: the harness shows lost requests when scaling 3→1 with a naive order (removal without confirmed reload and drain) across seeds. Commit 2: with the planner's ordering rule, 0 failed requests over at least 1,000 seeds. If simulation can't reproduce the loss, stop and report: the root cause lies elsewhere |
| R2.9 | Docker runtime and Caddy edge adapters, with one contract suite run against both the fakes and the real systems. Docker is the only runtime operator; Coolify is not carried over | The same contract suite passes against the fakes and against real Docker and Caddy (`OAP_TEST_DOCKER=1`); resources are adopted only by OAP labels |
| R2.10 | Desired-state Git repository adapter | Commits carry author and message; history, diff and revert work; export to a mirror is one-way |
| R2.11 | Secret store adapter: encrypted file store, key held separately | Secrets round-trip through the store and a reopened store; a wrong key fails closed; secrets never appear in journal entries or errors (test). Compatibility with today's store was dropped with migration (owner, 2026-10-19) |

**Phase exit:** with real Docker and Caddy on the owner's machine, scaling 1→3→1
under continuous traffic shows 0 failed requests; OAP with SQLite stays under its
60 MiB footprint budget during that run.

## Phase 3: API, identity and people

Depends on phase 2.

| ID | Task | Acceptance |
|---|---|---|
| R3.0 | First launch: `oap` with no arguments shows the onboarding journey and its next step, guided on a TTY and as JSON for agents ([onboarding](onboarding.md#first-launch)) | Golden output for each journey state in both modes; owner-only steps say why and what to ask; exit codes 0, 3, 4 and 5 as documented; state survives an interrupted run |
| R3.1 | Service layer: the seven primitives over core and engine | Service tests for each primitive using simulation adapters |
| R3.2 | Connect server in `cmd/oapd`; errors with typed `why` and remedy | The same request works over gRPC, gRPC-Web and Connect JSON (curl), with identical results |
| R3.3 | `watch` and `await` as server streams, resumable from a revision | A client that disconnects and resumes from its last revision misses no event and sees none twice |
| R3.4 | MCP surface generated from the protos (stdio and HTTPS) | Parity test: one scenario through Connect JSON, gRPC, MCP and the CLI produces identical journals |
| R3.5 | CLI (`cmd/oapctl`): generated client, human output, `--json`, `--events` | Golden output tests for each mode |
| R3.6 | Owner one-time login on the server; device login for agents; short-lived scoped tokens; session approval modes; pause and revoke | Expired, revoked and out-of-scope tokens are rejected; pause takes effect on the next request. **Decision needed first:** default session approval mode |
| R3.7 | `ask` with a `Notifier` port (log and webhook first) | Ask, answer and expiry flow through `watch`; an expired ask can't be answered |
| R3.8 | Briefing: generated Markdown per server and app, served by `observe`, at `/.well-known/oap/briefing.md` and as an MCP resource | Golden briefings for the reference apps; owner `briefing.md` content appears labelled as owner-written |
| R3.9 | Security tests for the whole surface | An agent can't act beyond its scope through any interface; a scan of every response and log in the acceptance run finds no secret values. **Decision needed first:** default autonomy for a fresh install |
| R3.10 | `oap host connect`: bootstrap key, pinned host key, password from the TTY only, break-glass ([onboarding](onboarding.md)) | A test drives `connect` without a TTY and the password is refused; the key is in `authorized_keys` with its tag; `~/.ssh` is untouched |
| R3.11 | `oap host check`: read-only probe, typed findings | Golden findings for fixture hosts (supported, old OS, no swap, port busy, foreign workloads, existing OAP); the probe makes no writes (checked by a read-only filesystem in the test) |
| R3.12 | Host fixes as approved steps, with a manifest and undo | Each fix's apply then undo returns the fixture host to its starting state; an unapproved fix never runs; destructive fixes can't be grouped |
| R3.13 | `oap host install`: `oapd` and Caddy as containers, idempotent, upgrade by digest | Running install twice changes nothing the second time; Caddy is observed as an OAP system component; a fresh 1 GB host stays inside the footprint budget |
| R3.14 | Hand-off and uninstall | After hand-off the bootstrap key no longer authenticates; uninstall on a fresh host leaves only what was there before, compared by a host snapshot |
| R3.15 | Owner secrets: `oapctl owner secret set`, `list` and `delete`; the value is read from the terminal (or stdin), never echoed, and stored in oapd's encrypted secret store; a secret ask is answered by setting the secret it names | The value never appears in any response, CLI output, log, journal entry or desired-state commit (R3.9's scan); an agent can't set, list or read secrets; an app referencing the secret starts with it |
| R3.16 | App-authoring skill: guidance that teaches any agent to turn a repository, whatever its stack, into an image and an `oap.yaml` (find the port, health endpoint, env, data and workers; write a Dockerfile if missing; declare secret references and ask for their values; validate by proposing). Served by `oapd` over MCP (prompt and resource) with the application schema, so it matches the server's version; a Claude Code skill package points to it. Pulls the authoring part of R5.4 forward | Reference repositories of different stacks (at least a Go API with Postgres and a Node service with a worker) become specs that OAP accepts, with no secret value in them, following only the skill; the skill and schema served by `oapd` match the ones in the repository |

**Phase exit:** the local journey (create, deploy with a migration, scale,
injected failure, explain, fix, roll back, verify) passes driven only through
MCP, and again only through the CLI.

## Phase 4: Extensions

Depends on phase 3.

| ID | Task | Acceptance |
|---|---|---|
| R4.1 | Plugin host (go-plugin): versioned extension protos, capability grants, timeouts, memory caps | A crashing or hanging extension doesn't affect the core; the extension protos have no field that can carry a policy verdict; installing an extension is a `sensitive` proposal |
| R4.2 | Builders: on-host build queue (one build at a time by default, with memory admission) and registry-only | Two concurrent build requests on a 1 GB-class memory limit run one after another; the host never runs out of memory in the test |
| R4.3 | GitHub change facts (read-only GitHub App) | Facts for a recorded fixture repository map to the envelope's risk tiers; CI-not-green is denied |
| R4.4 | Notifications: webhook and email | Ask and digest delivery with retries; failures surface in `observe` |
| R4.5 | Compose import | The examples from [Compose import](https://github.com/mohsalsaleem/OpenAppPlatform/blob/4020bf4912936b1ef119d0b315d7b78abca3c606/docs/compose-import.md) convert to valid applications with the documented warnings |

## Phase 5: Cutover

Depends on phase 4. R5.3 runs on the owner's server.

| ID | Task | Acceptance |
|---|---|---|
| R5.1 | ~~`oap migrate`~~ **Dropped** (owner, 2026-10-19): no install runs today's code, so nothing is migrated; apps are deployed fresh from their specs | — |
| R5.2 | Port the acceptance journeys (`tests/env`) and the VPS release journey to the new interfaces | All ported journeys pass locally |
| R5.3 | Deploy to the owner's server and run the release journey over CLI, Connect and MCP | The journey passes on a 1 GB host with the evidence pack (receipts, monitor summary, findings) committed under `docs/evidence/` |
| R5.4 | Agent store (Turso/libSQL) and harness packages | A fresh machine syncs the store, logs in and resumes managing a server with no credentials synced. **Decisions needed first:** sync default; which harnesses first |
| R5.5 | Docs, roadmap, release notes; retire the old tree | Old tree removed from `rewrite`; docs describe only the new system; the owner approves the merge to `main` |
| R5.6 | Release artifacts: `make release`, `oapctl` binaries, the oapd image on GHCR pinned by digest in `oapctl`, `SHA256SUMS` ([release plan](release-plan.md)) | A clean machine installs the published oapd with the published `oapctl`; every file matches `SHA256SUMS`; versions name the tag |
| R5.7 | Install path: `install.sh` that verifies checksums | It installs a local copy of the release and refuses a tampered one |
| R5.8 | Pre-release security review of the new tree | Findings fixed with tests, or recorded as known limitations |

Decided (owner, 2026-10-21; [release plan](release-plan.md)): v0.2.0 is
public, for early users, and ships once R5.2, R5.3, R5.5, R5.6, R5.7 and R5.8
pass. R4.4, R4.5 and R5.4 move to v0.3. R5.2 is narrowed to the release
journey (the old `tests/env` journeys are replaced, not ported one by one).
R5.3 runs on `vultr_oap` from the root password; Nisha Shipping is down
during the run and redeployed after it. Artifacts are built locally with
checksums; GitHub Actions stays off.

## Decisions by task

Decided: Docker is the only runtime operator for the rewrite. The Coolify
adapter is not carried over; other operators can return later as extensions.

Decided for [onboarding](onboarding.md) (owner, 2026-10-19), needed before
R3.11–R3.13: supported systems are Ubuntu 22.04, 24.04 and 26.04 and Debian 12 and 13 (26.04 and Debian 13 added the same day, after the first real run stopped on 26.04); OAP
installs Docker itself, as a sensitive fix the owner approves, upgrades a
packaged Docker only through its own package with the restarting containers
listed, works with an unmanaged Docker 24 or later with a warning, and stops on
snap, Podman, rootless or unreachable Docker ([onboarding](onboarding.md#docker-on-servers-that-already-have-it)); the automatic
hostname is sslip.io unless the owner gives a domain.

Decided (owner, 2026-10-19): R3.0, R3.10 and R3.11 run ahead of R3.1, because
they need only SSH, not `oapd`. OAP uses the system OpenSSH client (`ssh`,
`ssh-keygen`, `ssh-keyscan`), so no SSH library is added and `ssh` itself reads
the password from the terminal.

Decided (owner, 2026-10-20): add R3.15 (owner secrets) and R3.16 (the app-authoring
skill), found while planning to host an owner's backend service: OAP stays
independent of any stack, and agents learn to author apps for it from a skill
OAP serves. Testing with the owner's real application is parked.

| Decision (rewrite doc §17) | Needed before | Recommendation |
|---|---|---|
| Default session approval mode | R3.6 | **Decided** (owner, 2026-10-20): trusted agent identity. The owner approves an agent's identity once (bound to a key the agent holds); its later sessions sign in automatically and the owner is notified. Approving every session stays available as a setting |
| Default autonomy for a fresh install | R3.9 | **Decided** (owner, 2026-10-20): routine changes allowed. On a fresh install an agent may make routine changes (scaling, deploying within its scope) without the owner; anything sensitive or irreversible asks the owner first |
| Turso Cloud or self-hosted libSQL | R5.4 | Self-hosted option first, Cloud as opt-in |
| Harness packages first | R5.4 | Claude Code skills and generic MCP |
| Patch VPS-H-001 on today's code | — | Not needed: no install runs today's code (owner, 2026-10-19) |

## Proposals awaiting the owner

Nothing here is approved, and no code relies on it.

**A `notice` ask kind** (proposed 2026-10-21, from R3.16's fresh-agent runs).
Agents sometimes need to tell the owner something they can't fix and that
doesn't block them: the notes agent found a committed `.env` and had to file
a `blocker` to say "remove it and rotate its values". A blocker waits for an
answer and reads as "I'm stuck", so the owner gets a false alarm and the
agent a pending ask it doesn't need.

- Add `ASK_KIND_NOTICE` to `AskKind`. Like the other kinds it is journalled,
  notifies the owner, has an expiry and appears in the owner's briefing (as
  a notice, apart from open asks), and the asking agent sees what happens to
  it through `watch`.
- It needs no answer and never blocks: `ask` returns at once and
  `ask --wait` doesn't wait for a notice. The owner may acknowledge it
  (`oapctl owner answer ID ack`, the only accepted answer), which closes it;
  otherwise it closes quietly at its expiry.
- It carries only the question text, like a blocker: no options, proposal or
  secret reference (refused with `ask.bad_request`). It grants nothing and changes no
  verdict.
- Rejected alternative: a separate `note` primitive. The rewrite doc fixes
  seven primitives, so an eighth is an architecture change needing an ADR,
  for behaviour `ask` already has apart from the waiting.
- If approved, it lands as one task: proto, service validation and answer
  rules, briefing, CLI, notifier text, tests for each, and the skill
  (`author-app.md`, which would point repository warnings such as committed
  secrets at a notice).

**From R5.3** (proposed 2026-10-28; the [evidence pack](../evidence/vultr-oap-2026-10-26/README.md)
lists the findings). Owner decision, 2026-10-28: v0.2.0 ships without
them. Removing an application and scaling to 0 come in v0.2.1, which also
clears vultr_oap's journey applications; the rest are v0.3 candidates.
Each still needs the owner's approval of its scope below before code
relies on it.

- **Remove an application, and scale to 0** (findings 5 and 6). Today an
  application can't be taken away: the journey's 18 containers stay on
  vultr_oap after hand-off, and a failed create leaves its application in
  desired state. Add a `remove` change to `propose` (a spec patch that
  deletes the application: stop and remove its instances, routes, jobs and
  network; volumes only when the proposal says so, which makes it
  sensitive), allow `replicas: 0`, and have a failed create offer its own
  removal in the outcome. Tests: removal under traffic to another app drops
  none of its requests; data volumes survive a removal that doesn't name
  them; `explain` shows no gaps afterwards. Then clear vultr_oap's journey
  applications through OAP and record it.
- **Honour `ingress.https`** (finding 4). The spec has the field, but oapd
  with TLS serves every routed host over HTTPS and redirects HTTP. Either
  route `https: false` hosts over plain HTTP, or remove the field from the
  schema and the skill. The recommendation is to remove it: every public
  host should be HTTPS, and a test can prove the schema refuses it.
- **Onboarding logs and `oapctl logs`** (finding 7). Each `oapctl` onboarding
  command (connect, check, fix, install, hand-off, uninstall) saves its
  transcript, scripts it ran and their output under the config folder,
  with credentials never written; `oapctl logs [STEP]` lists and shows
  them. For the server, `oapctl logs --oapd` reads oapd's log through the
  owner API, so it works after hand-off. Tests: each step leaves a log; a
  typed password never appears in one; `--oapd` works with only the owner
  key.
- **Two wording fixes** (findings 3 and 8). The uninstall confirmation
  shows the exact text to type on its own line; the check's advice on SSH
  password login says what hand-off actually does (advise, not offer).
  Tests on the rendered text.

## Status

| ID | Status | Commit | Evidence |
|---|---|---|---|
| R0.1 | done | `a0cf59e` | `go build ./...` and `go test ./...` pass, unchanged from `main` |
| R0.2 | done | `6bb1d38` | `tests/arch` passes; self-tests reject 15 kinds of violation; a trial `import "os"` in `internal/core` failed the check (not committed) |
| R0.3 | done | `5faf302` | 8 unit tests pass; a trial edit to `internal/controller` failed the check (not committed) |
| R0.4 | done | `3eb8037` | `make rewrite-check` passes. No CI workflow: GitHub CI is disabled (owner decision) |
| R0.5 | done | | `CLAUDE.md` points to this plan and names `make rewrite-check` |
| R1.1 | done | `00e35fe` | `buf lint` clean; `make gen` reproducible (checked by `make rewrite-check`); `internal/surface/wire` golden tests pin field names, enum strings, presence, unknown-field errors and the 7 documented 64-bit revision fields |
| R1.2 | done | `5eaefb0`, `849a37c` | Round-trip property test over 3,000 seeded random trees passes; 27 strict-subset rejections with why codes and line numbers; 30 schema and semantic rejection cases. 14 of 17 example specs convert and validate (goldens in `internal/adapters/desiredyaml/testdata/examples`); the other 3 depend on Coolify-only resource adoption and fail with `convert.unsupported`, as the amended acceptance requires. `849a37c` keeps `dependsOn` as ordering (`after`), covering the pre-deploy job pattern |
| R1.3 | done | `6fac44b` | Golden graphs in `internal/core/model/testdata/worlds` for `two-components` (converged, healthy), `topology` (release and instance mismatch; stale; never observed) and the pre-deploy job app (failed job for the desired release, old release still serving, leftover component). Tests also cover job states, release stability under scaling and determinism under observation order. Affordance slots are present and filled by R1.6 |
| R1.4 | done | `94bafc7` | 8 table tests across all seven intents (changes checked path by path; the input tree is never modified) and 22 error cases, each with a typed `why` and a remedy: corrected intent, refresh, or an `InputFix` naming the fields. Proto additions: `AddComponent.name`, `Remedy.fix` |
| R1.5 | done | `71ca78f` | Property test over 3,000 random topologies (5,160 plans, 286 irreversible) runs each plan against a simulated runtime and edge and checks rules (a)–(d) and that the end state matches the new desired state. Each rule was shown to catch a deliberately broken planner (skipped drain, stop before route removal, wrong undo, unmarked destructive job; not committed). Golden plans in `internal/core/plan/testdata`: scale 1→3 and 3→1, deploy with a pre-deploy job, rollback, worker removal, data removal with volume deletion. Adds a `destructive` flag for jobs, needed to meet rule (d) |
| R1.6 | done | `248762e` | 16 table cases covering every rule (scope by application and component, allowed actions, four invariants including minimum healthy during the plan, category tiers, change facts, pause, breaker); irreversible plans need the owner even under an envelope that makes everything routine; a proposal claiming routine/allowed is re-judged; 200 shuffled evaluations give identical decisions; 7 envelope rejections; affordances by scope and action. Mutation checks: ignoring irreversibility and leaving reasons unsorted are both caught (the determinism test was strengthened to catch the second) |
| R1.7 | done | `20ff6a8` | Changing any of 9 inputs (base revision, base desired state, agent, intent fields, change facts, their absence, envelope version, policy context) changes the id; outputs don't. Stored proposals round-tripped through JSON replay to the identical proposal for allowed, needs-approval and denied verdicts, a rollback and a spec patch. Replay reports edited steps or context as tampering, edited verdicts or reasons as not reproduced, and missing base or envelope. Proposal gains `agent`, `base_spec_hash`, `context` and `irreversible` |
| Phase 1 exit | passed | `20ff6a8` | `make rewrite-check` green; `tests/arch` confirms `internal/core` imports no I/O, clock or randomness (imports: standard library minus I/O, `gen`, protobuf); 203 tests pass across the new tree; core is 3,409 lines with 1,887 lines of tests. Merged to `rewrite` through PR #1 (`7ee94d7`) |
| R2.1 | done | `rewrite-phase-2` | Ports are interfaces only, checked by a new architecture rule; data types live in `internal/core/ops`. Queue split from OpStore; later tasks refined Queue (caller's time, Heartbeat), Edge (`Config`) and snapshots (idempotency key) |
| R2.2 | done | `rewrite-phase-2` | 20 SIGKILL crash runs: integrity check passes, acknowledged batches survive, batches whole, revisions gapless and forward-only; injected pre-commit failure leaves nothing; a non-atomic Append is caught |
| R2.3 | done | `rewrite-phase-2` | 10,000 seeded runs under `-race` passed (2,362 s, one-off evidence run); every check runs 10,000 without race and 500 with race. A fencing mutation is caught on seed 10 |
| R2.4 | done | `rewrite-phase-2` | 183 crash points across six scenarios resume to the right end state with nothing done twice; with a non-idempotent runtime the executor's observation alone prevents duplicate starts |
| R2.5 | done | `rewrite-phase-2` | 300 random schedules under `-race`: no overlap, submission order per application; parallel applications proven; a non-serializing version fails |
| R2.6 | done | `rewrite-phase-2` | Same seed, identical trace (20 seeds); each fault produces its failure; fakes pass the per-port contract suites; SQLite passes the journal suite |
| R2.7 | done | `80d18c9` | With half of all events dropped, the observer's picture is never wrong for longer than one 5 s interval (20 seeds; 4 under `-race`); without drops it is exact after every poll; freshness on every node |
| R2.8 | done | `3396c09`, `8b28c4d` | The real executor scales 3→1 under traffic on two routes; each seed draws a reload lag (0–1 s) and a request rate (one every 20–500 ms). `3396c09`: with the naive order (route-remove then stop), all 1,000 seeds lose requests (11,107 of 109,962: 8,532 refused, 2,575 reset), matching the VPS run's URLError and RemoteDisconnected. `8b28c4d`: with the planner's plan, 0 of 144,683 requests fail over 1,000 seeds (50 under `-race`; `make rewrite-check` runs the full count). No production change was needed: the rule (R1.5) and the executor's confirm and drain (R2.4) already existed. Failing first: the same test run against the naive order fails on seed 1. Each safety step matters (not committed): without the confirmed reload 669 of 1,000 seeds lose requests, without the drain 970. Not covered, because no ordering can prevent them: reloads that reset in-flight connections, instances exiting during drain, and requests longer than the drain timeout. Whether real Caddy resets connections on reload is for R2.9's contract suite |
| R2.9 | done | `1828e0d` | The same Runtime and Edge contract suites pass against the fakes, local Docker 29.2.1 with Caddy 2.10.2 in a container, and `vultr_oap` (Docker 27.5.1, Ubuntu 18.04, 1 GB; cross-compiled test binaries, nothing built there). Labels only: unlabelled, partly labelled and other-installation containers are never observed, adopted or stopped, and a squatted name is refused. Every test resource was removed afterwards, locally and on the server. Real traffic: routes and prefix stripping work, `/apiary` doesn't match `/api`, and a 3 s request survives a reload that removes its instance (graceful). Finding: Caddy's upstream count for a removed instance dropped to 0 at the reload while the request was still running, so drain would have stopped it mid-request. Fixed by parking removed upstreams until idle; with that, 1 in flight before and after the reload. Gated skips (`OAP_TEST_DOCKER=1`): `TestRuntimeContract`, `TestAdoptsOnlyByLabels`, `TestEdgeContract`, `TestTrafficAcrossReload` |
| R2.10 | done | `48fd514` | DesiredRepo contract suite passes on the system-git repository; commits carry author, message and `Application`/`Spec-Hash` trailers, none for an unchanged tree; history newest first, diff by hash, revert as a new commit (refusing another application's version); export overwrites a bare mirror and never brings its commits back; reopening keeps state. In use on `vultr_oap` |
| R2.11 | done | `3efb82d` | Secrets contract suite; round trip through a reopened store, 0600 files, no plaintext on disk; wrong key, a value moved to another name, a damaged file and a key overwrite all fail closed; no error carries a value; a deploy using a secret leaves no value in any journal entry |
| Phase 2 exit | passed | `cf84b00` | On `vultr_oap` (real Docker 29.9.1 and Caddy 2.10.2, 1 GB) with the DNS edge (ADR 0001): scaling 1→3→1 under continuous HTTPS traffic lost 0 of 503 requests, and ten cycles lost 0 of 3,282 with no Caddy reload; the gated real-Caddy test on the owner's machine lost 0 of 348. Before the fix (`7444992`) the same scaling lost 8 of 428, every loss at a Caddy reload: VPS-H-001's root cause, now resolved. Footprint: `oapd` 3.3 MiB, Caddy 11.3 MiB ([evidence](../evidence/vultr-oap-2026-10-20/README.md)). Merged to `rewrite` through PR #2 (`c6b003e`, 2026-10-20), with the phase 3 tasks done by then |
| R3.0 | done | `fb49c6e` | Golden output for all 9 journey states as JSON and on a terminal (`cmd/oapctl/testdata`); every owner step says why and what to ask; exit codes 0, 3, 4 and 5 checked per state; an interrupted check resumes at the check without reconnecting; journey file is 0600 and replaced atomically. Run for real against `vultr_oap`: no server → exit 3, connect → exit 0, check → exit 4 (stopped on Ubuntu 18.04) |
| R3.10 | done | `fb49c6e` | Acceptance met: without a terminal a password connect is refused before any ssh runs; on `vultr_oap` the key landed in `authorized_keys` tagged `oap-bootstrap` (then removed by hand; it no longer logs in); every connection uses `-F /dev/null` and OAP's own known_hosts, and a test with a temporary HOME shows `~/.ssh` untouched. Break-glass (`host connect --break-glass --hours N`, 1–24 h, `cc43c90`): OAP's key is installed with an `expiry-time` sshd enforces and recorded in the host manifest with its end; on `vultr_oap` (OpenSSH 10.2) the key logged in until its expiry, the manifest recorded `until`, and a key past its expiry was refused. Test keys removed afterwards |
| R3.11 | done | `fb49c6e` | Golden findings for 9 probe outputs: `vultr_oap` (old OS, no swap, unmanaged Docker, other software, SSH passwords), read-only Ubuntu 24.04 and Debian 12 containers, and synthetic hosts (port busy, Docker missing, earlier OAP install, not root, old Docker, low disk). Gated `OAP_TEST_DOCKER=1` (skipped otherwise): the probe runs on read-only Ubuntu 22.04/24.04 and Debian 12 and `docker diff` shows no change; a planted hidden write is caught (not committed). Later (`048282c`, `80c86a0`): a firewall check (ufw blocking 80/443, found on `vultr_oap`) and recognizing the server's own installation; golden findings for 14 probe outputs |
| R3.12 | done | `a00dd41` | With `OAP_TEST_DOCKER=1` (skipped otherwise), each undoable fix (swap-file, time-sync) applied then undone on Ubuntu 26.04 and Debian 13 containers leaves every file, symlink and directory as it was, with the stubbed `swapon`/`swapoff`/`timedatectl` calls matched; an undo that forgets its fstab line is caught (not committed). An unapproved fix never runs and writes no manifest; without a terminal only the proposal is shown; only routine fixes share an approval (destructive and sensitive never grouped). Real run on `vultr_oap` (Ubuntu 26.04): the owner approved `install-docker` on the terminal; Docker Engine 29.9.1 from Docker's apt repository is running and enabled, the manifest records the fix, and the re-check moved the journey to `ready` (next: install, R3.13). Docker policy for existing installs is golden-tested on snap, Podman, rootless, unreachable and old packaged Docker. Later (`048282c`): `open-web-ports` (ufw) applied then undone in the same containers restores the host; applied on `vultr_oap` |
| R3.1 | done | `200f5c8`, `ba2a40f` | Observe, propose, apply, await and explain have service tests on the simulation adapters (under `-race`; await 20 runs in a row): create and await, verdict-based apply, owner approval, stale revision and base refused, failed steps, explain, resume after a crash exactly once. Login (R3.6) and ask (R3.7) have their own service tests on the simulation adapters; all seven primitives are in place |
| R3.2 | done | `acfd75a`, `7444992` | Observe returns the identical world over gRPC, gRPC-Web, Connect and plain JSON (curl), errors carry the same code and `oap.v1.ErrorDetail` over each; JSON uses proto field names. `oapd` serves on a 0600 unix socket only (until R3.6) |
| R3.13 | done | `bbbf084`, `048282c`, `7444992` | On `vultr_oap`: the second install printed `caddy unchanged` / `oapd unchanged`; upgrading to a newer `oapd` image recreated only `oapd` and kept its data; Caddy runs as an OAP system component (labelled with the installation id, which the check recognizes); footprint `oapd` 3.3 MiB, Caddy 11.3 MiB on a 1 GB host. Images go over SSH, checked by content on arrival |
| R3.3 | done | `cdefca5` | Cut after every possible event and resumed from the last revision seen, Watch and Await yield exactly the uninterrupted stream (same events, in order, none twice; under `-race`); a live watch sees the next operation from its first entry; the application filter keeps other applications' events out; over gRPC, gRPC-Web and Connect a stream dropped by the client and resumed continues where it stopped. Adds `AwaitRequest.since_revision` (documented 64-bit field) |
| R3.4 | done | `9e25ea4` | Parity: one scenario (create from `oap.yaml`, scale to 2) through Connect JSON, gRPC, MCP and the CLI leaves byte-identical journals (5 runs under `-race`); a one-word difference in the CLI's intent is caught. Tools and input schemas are generated from `AgentService`'s descriptors (descriptions are one line per tool: the Go descriptors drop proto comments). Transports: stdio (`oapctl mcp`, relaying over SSH) and HTTP at `/mcp` on oapd's socket; HTTPS comes with the public API (R3.6) |
| R3.5 | done | `3604165` | Golden output (`cmd/oapctl/testdata/cli`, 12 files, stable over 3 runs under `-race`) for `observe`, `explain`, `watch`, `app apply` and `app scale` in human, `--json` and `--events` modes, including an unchanged re-apply and an unapproved change (exit 3 with the proposal as JSON); every command uses the generated Connect client |
| R3.6 | done | `483b07d`, `4902b30` | Expired, revoked, unknown and out-of-scope tokens are refused, and pause (one agent or all) takes effect on the next request, under `-race` in the service and end to end through `oapctl` against a token-only listener. Device login: the owner sees the code, approves with a scope written into the envelope, and the agent's token (1 h, stored only as a hash) works inside it. Trusted identity (owner decision): a login proving the trusted Ed25519 key is approved at once, and replayed, stale or forged proofs and other keys are refused; every-session mode and forgetting the identity bring the owner back. State survives restarts. On `vultr_oap` over HTTPS (`oap.139-180-132-23.sslip.io`): the public API refused a request without a token; an agent's login appeared to the owner, was approved for `nisha-shipping` with `scale`, observed over HTTPS and was refused on another application; pause and resume took effect on the next request; a revoked token was refused and the trusted key signed in again without the owner; the test agent was then revoked and forgotten. Trusted logins now notify the owner (R3.7). Owner login (owner decision: an owner key): `TestOwnerKey` (service, `-race`): a key enrolled by the owner on the local socket signs in as the owner and every sign-in is reported; unenrolled keys, forged proofs and agents calling themselves owner are refused; agents and network owner sessions can't enroll or remove keys; pausing all agents doesn't pause the owner; removing a key ends its sessions; state survives a restart. `TestOwnerEnroll` (CLI): at the installed stage the front door enrolls the key and owner commands then work over the public listener only (no SSH in the test), approving an agent there; after the key is removed they fail. R3.9's surface test covers the three new owner methods (agents get `permission_denied` on both listeners). On `vultr_oap` (oapd `4902b30`): `oapctl owner enroll` enrolled this Mac's key, and `owner keys`, `owner agents` and `observe` then ran over `https://oap.139-180-132-23.sslip.io`; oapd reported the enrolment and one owner sign-in |
| R3.7 | done | `5a543d7` | Ask, answer and expiry reach the asking agent through watch (under `-race`); an expired or already answered ask can't be answered; answers are checked by kind (approve/deny, one of the options, provided/declined for secrets so no value passes through); only the owner answers; other agents don't see the asks; asks and approvals survive a restart. The needs-approval loop: an agent's refused apply carries an approval ask as its remedy, and once the owner answers approve the agent's apply runs. Notifier port: log and webhook (JSON, HMAC-SHA256 signature, retries with backoff, no retry on 4xx); asks and trusted logins (R3.6) notify the owner. End to end through `oapctl`: `ask --wait`, `owner asks`, `owner answer`. On `vultr_oap` over HTTPS: a signed-in test agent asked a decision and waited, the owner saw it with its options and answered, the agent received the answer, and oapd logged the notification; the test agent was then revoked |
| R3.8 | done | `ee1db23` | Golden briefings (`internal/core/briefing/testdata`) for the reference worlds (two components; topology mismatched, stale and never observed; failed pre-deploy job) and for the owner, generated from the world model by a pure renderer (deterministic over 20 renders). The owner's `briefing.md` (server and per application, in the desired-state repository) appears under "House rules … (written by the owner)", quoted and marked as not generated or enforced, so a heading inside it can't pass for OAP's own. Each caller's briefing covers only its applications, grants and own asks. Served by `observe` (`briefing: true`), at `/.well-known/oap/briefing.md` (token required on the public listener) and as MCP resources `oap://briefing[/APP]`; `oapctl briefing` and `oapctl owner briefing`. On `vultr_oap`: the owner's briefing for nisha-shipping showed it converged, the recent scaling and a temporary house rule under the owner-written heading (removed afterwards); the public briefing URL answered 401 without a token |
| R3.9 | done | `55a199a` | `TestAgentStaysInScopeOnEverySurface` (`cmd/oapctl`), on a fresh install with the real SQLite journal, Git desired state and secret store, and oapd's own listeners (now `internal/surface/listeners`): an agent scoped to one application is refused on another (observe, briefing, explain, propose, apply, await, watch, ask) over Connect, Connect JSON, gRPC, gRPC-Web and plain JSON, on the public listener and on the socket with its token; can't apply the owner's proposal on its own application; can't call any of the owner API's 11 methods (absent from the public listener, `permission_denied` on the socket); is refused through MCP over HTTP and stdio, the briefing endpoint and the CLI; and sees nothing of the other application or another agent's ask in observe, watch, briefings or MCP listings. Missing and unknown tokens are refused everywhere (an unknown token on the socket is not taken for the owner); paused, revoked and expired tokens from the next request. Every response, CLI output, oapd's log, the data directory and the desired-state history were scanned for the secret value (plain, base64, hex) and for tokens: none found; a planted leak fails the scan, and the secret is checked to have reached the instance. Found and fixed: `oapctl mcp` ignored `OAP_AGENT` and reached oapd as the owner. Owner decision pinned by `TestFreshInstallAutonomy`: on a fresh install an approved agent's scaling runs without the owner, and a domain change needs approval. On `vultr_oap` over HTTPS: an agent scoped elsewhere was refused on `nisha-shipping` on every surface and protocol, and after revocation its token appeared in no log, data file or desired-state commit (`docs/evidence/vultr-oap-2026-10-20`). The secret-value scan ran in the test only: there is no way to store a secret on the server yet |
| R3.14 | done | `0290593` | Hand-off: `TestHandoffAgainstSSHD` (`OAP_TEST_DOCKER=1`) ran `oap host handoff` with the system ssh against a real sshd in an Ubuntu 26.04 container: OAP's key worked before and was refused (`Permission denied`) after, the owner's own `authorized_keys` line was untouched, and the manifest recorded the removal. `TestHandoff`: refused without a working owner key, touching nothing; when the key still works after removal it says so and stays at `owned`. Uninstall: `TestInstallUninstallRestoresHost` (`OAP_TEST_DOCKER=1`): a Docker-in-Docker host got OAP's key, the real oapd and Caddy, and an application with a volume deployed through oapd's API; after uninstall (manifest steps newest first, OAP's directory, then the key) its filesystem and Docker's containers, images, volumes and networks matched the snapshot taken before; skipping the last cleanup step fails it. `TestUninstall`: without a terminal only the plan is shown for the owner; a mistyped server address removes nothing; fixes without an undo (Docker) are kept and reported with the manifest. Not run on `vultr_oap`: handing it off ends OAP's SSH access there (later oapd upgrades would need break-glass with the root password), and uninstalling it would take Nisha Shipping down; both are the owner's call |
| R3.15 | done | `4fb00fe` | `TestOwnerSecrets` (service, `-race`): only the owner sets, lists and deletes secrets; bad names, empty and over-64 KiB values are refused; a secret ask can't be answered `provided` before its secret exists, and a value set for it must match its reference (a refused request stores nothing); setting it answers the ask; the journal records the set and the delete by name and never the value. R3.9's `TestAgentStaysInScopeOnEverySurface` now covers the flow end to end: an agent asks for an API key, the owner pipes it into `oapctl owner secret set shop-api-key --ask ID`, the ask closes, the shop's new instances start with both secrets, agents get `permission_denied` from all three methods on both listeners, and the scan finds neither value in any response, CLI output, log, data file or desired-state commit. On `vultr_oap` (oapd `4fb00fe`): a test secret was set through the owner's HTTPS session, listed, found in none of oapd's log, Caddy's log, the 95 data files or the desired-state history (the journal recorded only its name), and deleted |
| R3.16 | done | `4bf724d`, `252429d` | The skill (`internal/core/skills/author-app.md`) is served by oapd as the MCP prompt `author-app` and resource `oap://skills/author-app`, with the schema at `oap://schema/application`, over HTTPS (token required) and through `oapctl skill`; `harness/claude-code/skills/oap-author-app` points to it. `TestSkillServedMatchesRepository`: MCP, HTTPS and the CLI serve exactly the repository's guide and schema. `TestGuideExampleIsValid`: the guide's example is a spec OAP accepts. On `vultr_oap` (oapd `252429d`): `oapctl skill` and `oapctl skill schema` over HTTPS matched the repository; without a token, 401. Found and fixed while writing it: components had no service name on the network, so `uses` URLs resolved to nothing (`TestServiceNamesResolve`, failing first). Reference repositories for the evaluation are in `internal/core/skills/testdata/repos` (Go API with Postgres; Node web and worker with Redis). Fresh-agent acceptance (owner approved the run, 2026-10-21): two fresh agents, each given only the `oap-author-app` package and one reference repository, loaded the guide from `vultr_oap` and proposed `notes` (web, migration job, Postgres data component) and `shop` (web, worker, Redis data component) without applying; OAP accepted both (needs approval: new domains). `TestSkillEvalSpecsAccepted` checks the committed outputs (`internal/core/skills/testdata/agent-runs`) against expectations fixed before the run: valid and accepted, the right components, all four secret variables as references and never in `env`, and no `.env` value in any file (a planted value fails it). Their asks were answered and the test agent revoked. They surfaced three gaps, recorded there: available memory unobserved on `vultr_oap`, workers' image health checks with images that have none, and no ask kind for a notice. Follow-up (2026-10-21): `TestObserveAvailableMemory` (available memory from the host's meminfo, `optional` in `world.proto`, "available memory unknown" in the briefing when absent) and `TestImageWithoutHealthCheck` (an instance whose image has no `HEALTHCHECK` is healthy once running, in observe and in its start event; before, the start event said starting until the next resync, up to 30 s) both failed before the fix; gated `TestStartEventWithoutHealthCheck` passes against local Docker 29.2.1 and failed with the old start event. The skill documents both. A `notice` ask kind is proposed below, not built. Not run on `vultr_oap` |
| Phase 3 exit | passed | `b2ee4b8` | `TestPhase3Journey` (`OAP_TEST_DOCKER=1`) on a Docker-in-Docker host with the real oapd and Caddy 2.10.2, reaching oapd's socket as an SSH forward would: one application driven only through MCP (118 s) and another only through the CLI (99 s), each created (migration job, then web, served through Caddy), deployed with a new migration (a job step ran), scaled to 3, broken with an image that never serves (not achieved), explained (gap named), fixed forward, rolled back with the failed deploy's undo, and verified (converged, 3 instances on the earlier image, no gaps, serving). Found and fixed on the way: observe lagged an achieved outcome (`ab05b69`, failing test first); the CLI had no deploy or rollback (`93c74a9`). Known limit: HTTP health checks run `wget` inside the container, so images without it can't use them (noted in the skill). Every phase 3 task is done (R3.16 closed 2026-10-21) |
| R4.1 | done | `894d6e0`, `4a4d60d` | Host (`internal/adapters/extension`, go-plugin gRPC; contract `proto/oap/extension/v1`): `TestFailuresStayInTheExtension` (`-race`): a crashing, a hanging and a memory-hungry extension each fail their own calls within bounds (hang: the 2 s timeout; memory: killed over its 64 MiB cap) and are stopped and restarted on next use, while a healthy extension keeps working; `TestGrantsOnly`: an extension receives exactly its granted settings and secrets and none of oapd's environment (this caught go-plugin passing the host's environment on; now off); `TestRefusesMismatchedExtensions`: changed binary (sha256), another name, an ungranted point or a grant it never asked for are refused. No verdict: `TestContractCarriesNoVerdict` walks every message of the contract (no OAP import, no policy-named field or enum value, no free-form field); adding `bool allowed` fails it. Sensitive install: `TestInstallingAnExtensionIsSensitive`, `TestOwnerMayRelaxExtensions`, `TestBrokenExtensionIsRolledBack`, and `TestExtensionInstallThroughTheCLI` with the real host (the owner's install is planned as sensitive and verified; an agent with the action can only propose; with a hanging notifier installed an ask still returns at once). On `vultr_oap`, oapd `4a4d60d` with the host runs at 8.5 MiB after start (13.4 MiB before, long-running); Nisha Shipping unaffected. Not yet: network capabilities are recorded, not enforced (extensions don't run in their own network namespace); first-party extensions arrive with R4.2–R4.5 |
| R4.2 | done | `dd7aa10` | `TestBuildsOneAtATimeOn1GB` (`OAP_TEST_DOCKER=1`): on a Docker-in-Docker host with 1 GiB of memory and swap, running the real oapd and the on-host builder extension, two builds asked for at once, each holding 600 MiB in a step, ran one after another (journal revisions 26–28, then 29–31), both succeeded, and the host's `oom_kill` count stayed 0; the same two builds run at once outside the queue raised it to 1, so the check can fail. Registry-only: the `registry` builder returned the image a local registry had for the commit; with two builders installed, a build naming neither is refused. `engine` queue tests (`-race`, 20 runs): one at a time in order, concurrency 2, waiting for memory then running, giving up with the reason, cancellation; they found an ordering bug (tickets compared by pointer to a zero-size value), fixed. `TestBuildIsARoutineProposal`: a build is routine within scope, names its image in the outcome, changes no desired state; out of scope, without the action, or with bad input it doesn't build. On `vultr_oap` (Docker 29.9.1, oapd `dd7aa10`): the builder extension was installed through the CLI and built `oap-build/nisha-shipping-website:5d3534c64e96` from a test repository, labelled with the installation and commit, which ran; the test image was then removed and the site was unaffected. The builder stays installed there. Uses Docker's classic builder (needed for the per-build memory limit), which Docker has deprecated |
| R4.3 | done | `2a1550d` | `TestChangeFactsFromRecordedRepository`: seven commits recorded from real public GitHub repositories (`internal/adapters/extension/testdata/github`, recorder and `sources.tsv`: cli/cli, mastodon/mastodon, microsoft/vscode, this repository) served as one fixture repository behind GitHub App authentication (JWT verified, installation token issued). Through the real `oap-ext-github` extension and an agent's deploy proposals, under an envelope with `ci-not-green: deny`, `migrations-changed` and `lockfile-changed: sensitive`, `docs-only: routine`: plain and docs-only commits with green CI allowed; a lockfile change and a migration needing the owner; failing, still-running and absent CI denied. A built image's deploy gets its build's commit facts; an unreachable GitHub gives CI unknown and a denial; with no fact source installed, no facts. Unit tests: file classification, CI folding (skipped or neutral checks verify nothing; found while writing them), review approval, repository URLs. On `vultr_oap` (oapd `2a1550d`): the GitHub extension installed anonymously and, for a proposed (not applied) deploy sourced from a public cli/cli commit, reported CI green, a lockfile change, reviewed and signed. Not yet: the default envelope has no `change_facts` rules, so facts change verdicts only once the owner adds them |
| R4.4–R4.5 | moved to v0.3 | | Owner decision, 2026-10-21 ([release plan](release-plan.md)) |
| R5.1 | dropped | | Owner decision, 2026-10-19: no install runs today's code |
| R5.2 | done | `6b9041c` | `TestReleaseJourney` (`OAP_TEST_DOCKER=1`, 365 s) on a Docker-in-Docker host with the real oapd and Caddy, three agents signed in by device login and approved by the owner, each with its own application and hostname. CLI, full: a fixture app (web, api, worker, Redis-style data component with a volume, migration job) created; a deploy that needs approval proposed, approved through an ask and applied with `oapctl apply`; replaying it returns the same operation; deployed with its migration first; a failing migration blocks the rollout; a release whose api never becomes ready is not achieved, drops no api requests, leaves no stray instances and restores the previous desired state; explained; corrected forward to v2; scaled to 3 and back with 0 failed requests; rolled back (the migration skipped); volume data retained. Connect JSON and MCP: smoke runs (create, deploy, serve). Then: the three apps serve side by side behind the shared edge; with oapd stopped for 30 s, 549, 550 and 551 probe requests to them all succeeded; every interface answers after the restart; peak memory 530 MiB, no OOM kills. Receipt: [`docs/evidence/release-journey/2026-10-21-local`](../evidence/release-journey/2026-10-21-local/receipt.json). Found and fixed, each with a failing test first: a deploy that never became ready left its new instances running beside the old ones and its images in desired state (`TestFailedDeployLeavesNoStrays`); with oapd stopped Caddy failed every request, because its A-record upstreams had no grace period: the DNS edge now serves SRV records naming each member's host and port, and Caddy's `srv` source keeps the last members while oapd is down (`TestDNSEdgeKeepsServingWithoutOapd`, and the real-Caddy `TestDNSEdgeScalesWithoutLoss` still passes); `oapctl ask` refused flags before the question (`TestAskEndToEnd`). Noted, not fixed: `explain` for a failed migration job says "0 of 1 instances running". Under a full parallel `go test ./...`, `TestFailuresStayInTheExtension` can exceed its bounds (it did before this task too); alone and in `rewrite-check` it passes |
| R5.3 | done | `ed12efc` | On vultr_oap (1 vCPU, 950 MiB), from the root password with released v0.2.0-rc.2: uninstall, connect, fix (`open-web-ports`), install pulling the pinned digest from GHCR, owner enroll. `TestReleaseJourney` with `OAP_JOURNEY_SERVER` failed its first run (the fixture image was built on this Mac's arm64 base; the harness's names didn't resolve) and, after the harness fix, passed its second (587 s): CLI full, Connect JSON and MCP smoke runs by three device-login agents the owner approved; X1 each app on its own HTTPS host name; X2 oapd stopped under traffic with 659 requests and 0 failed; X3 memory peak 452 MiB, at least 498 MiB available, `oom_kill 0`. Nisha Shipping redeployed through OAP from its saved image and serves at www.nishashipping.com. Hand-off (2026-10-28): OAP's key removed and refused (`Permission denied`), owner commands work over HTTPS. Not done as planned: the journey's apps weren't scaled to 0, as OAP can't (owner decision, 2026-10-28: hand off anyway). Evidence pack and eight findings: [docs/evidence/vultr-oap-2026-10-26](../evidence/vultr-oap-2026-10-26/README.md); fixes for findings 1 and 2 are `c18a6ac` and `9385169`, the rest are proposed above |
| R5.5 | not started | | |
| R5.4 | moved to v0.3 | | Owner decision, 2026-10-21 |
| R5.6 | done | `79942bc` | `make release` (`scripts/release.sh`) builds `oapctl` for macOS and Linux on amd64 and arm64 and oapd's image for linux/amd64 and linux/arm64, pins the image by digest in `oapctl`, writes `SHA256SUMS` and drafts a GitHub release; `DRY_RUN=1` publishes nothing. `TestReleaseInstallsPinnedImage` (`OAP_TEST_DOCKER=1`): with a local registry standing in for GHCR, the released `oapctl` pins the pushed digest, and a fresh Docker-in-Docker host given only that reference pulled exactly it and ran it (recorded for uninstall; `oapd -version` names the release); every file matched `SHA256SUMS`. A dry run of v0.2.0-rc.1 built everything in 35 s. Published (owner approved, 2026-10-21): [v0.2.0-rc.1](https://github.com/mohsalsaleem/OpenAppPlatform/releases/tag/v0.2.0-rc.1), a GitHub pre-release of `3193fc0`, with `ghcr.io/mohsalsaleem/oapd:v0.2.0-rc.1@sha256:3f8b3b2d0ce7…` (linux/amd64 and linux/arm64). Checked from a clean directory: `install.sh` (from `rewrite-phase-4`, `OAP_VERSION=v0.2.0-rc.1`) installed `oapctl`, which names v0.2.0-rc.1 and pins that digest; every downloaded file matched `SHA256SUMS`; GHCR's index for the tag has that digest, and a pull by digest ran `oapd -version` → v0.2.0-rc.1. Correction (R5.3, 2026-10-23): that pull and inspection weren't anonymous, though they ran with an empty Docker config (Docker Desktop still had credentials); the package is private, so a server's anonymous pull is refused (`unauthorized`; the registry answers 403 without credentials). An anonymous check is now a plain registry request without credentials |
| R5.7 | done | `fa6ee7b` | `install.sh` installs `oapctl` for the machine from a release only when it matches `SHA256SUMS`, never with sudo. `TestInstallScript`: a matching release installs a runnable `oapctl`; a tampered binary or one `SHA256SUMS` doesn't list is refused and nothing is installed. Against the dry-run v0.2.0-rc.1 served locally it installed `oapctl`, which named its version and pinned oapd |
| R5.8 | done | see below | Review of identity and tokens, the public listener, secrets, the extension host, the root-run onboarding scripts and `oapctl`'s credential files. Fixed, each with a test: logins anyone can start without a token are bounded (16 waiting, then refused at once; proof nonces capped), `TestLoginFloodIsBounded`; request bodies are capped at 4 MiB on both APIs, `TestRequestsAreBounded` (it showed 6 MB bodies read in full before); extensions run as `nobody` when oapd is root, with Docker's socket only when granted, and oapd's data directory is 0700 (the journal was readable by any user in the container), shown in `TestBuildsOneAtATimeOn1GB`; builders clone only https or ssh repositories unless the owner allows local ones (a `file://` build could copy oapd's desired state into an image an agent reads), `TestRepositoriesAreRemote`; oapd's HTTP servers gain an idle timeout and a header limit. Checked and found sound: shell input to root scripts (validated, never interpolated unchecked), token storage and comparison, owner-key enrolment, credential file modes. Known limitations recorded in the [release plan](release-plan.md) |

Statuses: `not started`, `in progress`, `blocked (reason)`, `done`. Rows are split
per task once work on a phase begins.
