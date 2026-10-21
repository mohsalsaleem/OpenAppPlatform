# Release plan: v0.2.0, the agent-native rewrite

Status: proposed (2026-10-21). The owner's decisions are recorded below;
the tasks it adds are in the [rewrite plan](rewrite-plan.md), phase 5.

## What v0.2.0 is

The first public release of the rewrite, for **early users**. Someone with
a server's IP address, username and password can put OAP on it, sign in
their agent, and have the agent deploy and operate applications through
MCP, the CLI or the API, inside an envelope they control.

It ships what phases 1 to 4.3 built:

- **Onboarding:** connect with the password once, a read-only check,
  approved fixes, install (oapd and Caddy in Docker), an owner key, and a
  hand-off that removes OAP's SSH access. Uninstall puts the server back as
  it was.
- **The agent API:** observe, propose, apply, await, watch, explain and
  ask, the same over Connect, gRPC, gRPC-Web, JSON, MCP and the CLI, with a
  briefing and the app-authoring skill served by the server.
- **Policy:** scoped agents, device login with trusted identities, an
  envelope with risk tiers and change facts, approvals through asks, and
  pause and revoke.
- **Operations:** deploys without dropped requests (ADR 0001), scaling,
  rollback, migrations as jobs, secrets the owner sets and agents never
  see.
- **Extensions:** out of process, with grants and memory caps, and
  installed as sensitive proposals. It includes builders (on the server,
  one at a time within memory, or registry-only) and GitHub change facts.

## Owner decisions (2026-10-21)

| Question | Decision |
|---|---|
| Scope | **Core now, rest later.** v0.2.0 ships once R5.2, R5.3, R5.5 and the release tasks below pass. R4.4 (email notifications and digests), R4.5 (Compose import) and R5.4 (agent store and harness packages) move to v0.3. |
| Audience | **Public, early users.** Anyone can install it: `oapctl` binaries on GitHub Releases, the oapd image on GHCR pinned by digest, checksums, a getting-started guide. |
| Release journey host | **vultr_oap.** OAP is uninstalled there and the whole flow runs from the root password. Nisha Shipping is down during the run and redeployed through OAP after it. |
| Build and publish | **Locally, with checksums.** `make release` on the owner's Mac builds every artifact for amd64 and arm64, writes `SHA256SUMS` and publishes. GitHub Actions stays off. |

## Tasks

| ID | Task | Acceptance |
|---|---|---|
| R5.6 | **Release artifacts.** `make release VERSION=v0.2.0`: `oapctl` for macOS and Linux (amd64, arm64), the oapd image (with its first-party extensions) for amd64 and arm64 on `ghcr.io/mohsalsaleem/oapd`, `SHA256SUMS`, and a draft GitHub release. `oapctl` pins the oapd image by digest, as it pins Caddy's, so `host install` pulls the released image and needs nothing built locally | On a clean machine, the published `oapctl` installs the published oapd on a fresh host; every downloaded file matches `SHA256SUMS`; the image digest `oapctl` pins is the one on GHCR; `oapctl --version` and oapd's version name the tag |
| R5.7 | **Install path.** `install.sh` downloads `oapctl` for the platform from the release and refuses it unless it matches `SHA256SUMS`; the README gives it and the manual download | A test runs the script against a local copy of the release and a tampered one: it installs the first and refuses the second |
| R5.8 | **Pre-release security review** of the new tree: identity and tokens, the public listener, secrets, the extension host, onboarding scripts run as root | Findings fixed or recorded as known limitations, with tests for what was fixed |
| R5.2 | **Release journey** (narrowed): the v0.1 VPS journey, ported to the new interfaces as a Go test that runs against a local Docker-in-Docker host or a real server. A multi-component app (web, worker, data, migration job), driven fully through the CLI and as smoke runs through Connect JSON and MCP, each by its own agent, app and hostname. It also checks that the apps coexist behind the shared edge and survive an oapd restart, and that the host stays healthy. The old `tests/env` journeys are replaced by it and the existing gated suites, not ported one by one | It passes locally against the release candidate |
| R5.3 | **Release journey on vultr_oap**, from the root password, with the release candidate's published artifacts. See [the run](#r53-on-vultr_oap) | It passes on the 1 GB host, and the evidence pack (receipts, monitor summary, findings) is committed under `docs/evidence/`; Nisha Shipping serves again afterwards |
| R5.5 | **Docs and cutover:** README and getting started for the new system, the agents guide (MCP setup, the skill, the briefing), the security model, known limitations, release notes, roadmap (v0.3: R4.4, R4.5, R5.4); retire the old tree; merge `rewrite` to `main` | Docs describe only the new system; the old tree is removed from `rewrite`; the owner approves the merge to `main` |

## Order

1. Approve this plan (it changes the rewrite plan's phase 5).
2. **R5.6, R5.7, R5.8** on the branch: artifacts, install path, security
   review. They can run side by side.
3. **R5.2** locally, then publish **v0.2.0-rc.1** as a GitHub pre-release
   with its image on GHCR.
4. **R5.3** on vultr_oap with rc.1, in a window the owner picks.
5. Fix what R5.3 finds; publish further release candidates and re-run R5.3
   if anything in the install path or the journey changed.
6. **R5.5**: docs and release notes, retire the old tree, merge to `main`.
7. Tag **v0.2.0** on `main`, `make release`, publish the draft release,
   make the GHCR image public.

## R5.3 on vultr_oap

The site is down from step 2 until step 9. Steps marked **Owner** need
the owner at a terminal.

1. Save Nisha Shipping's image outside OAP (`docker save` to `/root`) and
   record its current spec, so it can come back unchanged.
2. `oapctl host uninstall`, approving each step, including removing OAP's
   key. Docker stays (OAP installed it and it has no undo); the host
   manifest says so.
3. **Owner:** install the release candidate's `oapctl` with `install.sh`,
   run `oapctl`, and type the root password at `host connect`.
4. Check, fix if anything is proposed, and install. `host install` pulls the
   released oapd by digest.
5. **Owner:** `oapctl owner enroll`.
6. Hand-off: OAP's SSH key is removed and refused.
7. The release journey: three agents sign in by device login and the owner
   approves them. The CLI run is full; the Connect JSON and MCP runs are
   smoke runs. The checks for coexistence, oapd restart and host health
   follow. A monitor records traffic and memory throughout.
8. Evidence pack committed.
9. Nisha Shipping redeployed through OAP as the owner (from the saved
   image, sent with break-glass access, or built from its repository if
   it's pushed). The site serves at www.nishashipping.com.

After R5.3 the server runs released v0.2.0 the way a user's would: no
OAP SSH access, owner commands over HTTPS with the owner key, upgrades
through break-glass.

## Known limitations for the release notes

- HTTP health checks run `wget` inside the container. Images without it
  use image health checks.
- Builds use Docker's classic builder, needed for the per-build memory
  limit, which Docker has deprecated.
- Extensions' network grants are recorded, not enforced.
- Notifications are a log or a webhook; email and digests are v0.3.
- No Compose import, no agent store across machines (v0.3).
- One server per installation.
- The default envelope has no `change_facts` rules; the owner adds them.
- Proposed, awaiting the owner: an ask kind for notices.
- An extension granted the `docker_socket` setting (the builders) can use
  Docker, which is root on the host. Grant it only to extensions you trust;
  every other extension runs as `nobody` without it.
- `install.sh` checks `oapctl` against the release's `SHA256SUMS`, which
  catches a corrupted or swapped download but not a compromised release:
  releases aren't signed yet.
- An agent refused for an application outside its scope learns that the
  application exists (from the refusal).
- Only logins are rate-bounded on the public listener; signed-in agents'
  calls aren't rate-limited.
