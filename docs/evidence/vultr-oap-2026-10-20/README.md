# vultr_oap, 2026-10-19/20: onboarding, install, first deploy, VPS-H-001

The first end-to-end run of the rewrite on a real server: onboarding from
an IP address and a password, install, a deploy, and scaling under
traffic. Times are UTC.

## Server

`vultr_oap`: 1 vCPU, 950 MiB memory, reinstalled by the owner with
Ubuntu 26.04.1 LTS (kernel 7.0). The v0.1 install that ran there was
backed up to the owner's machine and torn down first.

## Onboarding (R3.0, R3.10–R3.13)

| Step | What happened |
|---|---|
| connect | The owner ran `oapctl`, confirmed the host key fingerprint and typed the root password once into `ssh`; OAP's bootstrap key was installed |
| check | Stopped on `host.os.unsupported`: the supported list omitted Ubuntu 26.04. By owner decision the list became Ubuntu 22.04/24.04/26.04 and Debian 12/13 |
| check | `host.docker.missing` (sensitive fix) |
| fix | The owner approved `install-docker` on the terminal: Docker Engine 29.9.1 from Docker's apt repository |
| install | `oapd` and Caddy started as containers; a second run printed `caddy unchanged` / `oapd unchanged`; an upgrade to a newer `oapd` image recreated only `oapd` and kept its data (journal revision 13, the application, its routes) |
| check | `host.firewall.blocks`: Vultr's image enables ufw allowing only SSH, which also blocks Docker's published ports. New check and fix (`open-web-ports`), approved by Claude on the owner's behalf under the owner's instruction to complete the deployment overnight |

Footprint after install: `oapd` 3.3 MiB, Caddy 11.3 MiB (budget: OAP
and SQLite under 60 MiB).

## First deploy

The Nisha Shipping static site (`~/saleem/nisha-shipping`, branch `oap`)
was deployed with `oapctl app apply oap.yaml`: its image was sent over
SSH (no registry), the proposal (5 steps: write desired, start, await
ready, route add, confirmed reload) needed approval because it adds a
domain, and the owner role applied it. About 20 s end to end.

`https://nisha.139-180-132-23.sslip.io/` serves every page with a Let's
Encrypt certificate; HTTP redirects with 308; the theme plugins left out
of the image return 404. Observe reports the component converged and
healthy, with its one instance routed.

## Moving to www.nishashipping.com (2026-10-20)

The site's real domain, `www.nishashipping.com`, already points at the
server. Changing `oap.yaml`'s domain first produced a one-step plan
(write desired) that left Caddy on the old host: the planner emitted no
route steps for a route-only change. Fixed in `a321ba6` (failing test
first); the domain then moved through OAP, and `www.nishashipping.com`
serves every page with its own Let's Encrypt certificate. Still open: the
world model doesn't compare the edge's actual hosts with desired routes,
so Explain didn't report the gap.

`oapd:a321ba6` was built on the server (owner decision, 2026-10-20):
Docker Desktop's amd64 emulation on the owner's machine hung. The image
build no longer emulates a CPU anywhere: apk installs the target
architecture's packages from a native stage.

## Scaling 1 → 3 → 1 under traffic: 8 of 428 requests failed

`traffic.py` sent requests from the owner's machine (four workers, a new
TLS connection per request) while `oapctl app scale` ran 1 → 3 and
3 → 1 (`scale-1-3-1.jsonl`). The plans used the planner's safe order
(route-remove, confirmed reload, drain, stop). Failures:
`Connection reset by peer` (5) and `RemoteDisconnected` (3): the same
two errors as VPS-H-001 on 2026-10-15. Every failure falls within 0.5 s
of a Caddy `POST /load`.

## Root cause of VPS-H-001: Caddy reloads drop connections

Reloading an **unchanged** configuration (forced with
`Cache-Control: must-revalidate`), with no instance added or removed,
failed 9 of 308 requests over five reloads (`noop-reloads.jsonl`); with
HTTP/3 turned off, 13 of 293 (`noop-reloads-no-h3.jsonl`). Each reload
logs `servers shutting down; grace period initiated` followed about
20 ms later by `server shutdown … context canceled` for `:443`, the line
noted in the October evidence.

So the loss is in Caddy 2.10.2's reload itself, not in the order of
OAP's steps. R2.8's ordering rule is still needed (in simulation, without
it every seed loses requests), but it can't prevent this. A likely mechanism, not yet
proven, is the listener handoff on reload (`SO_REUSEPORT`), which resets
connections still in the old socket's accept queue. R2.8's simulation
models this as the `ReloadDropsInFlight` fault.

Proposed fix: change route membership without reloading Caddy
([ADR 0001](../../architecture/adr/0001-edge-membership-without-reload.md),
awaiting the owner).

## The fix: route membership through DNS (ADR 0001, 2026-10-20)

`oapd:cf84b00` runs the DNS edge: Caddy's routes take their upstreams
from names oapd resolves, so scaling changes DNS answers, not Caddy's
configuration. One operation converted Caddy to this form (its only
reload). Then, against `https://www.nishashipping.com`, with the same
traffic as before:

| Run | Requests | Failed | Caddy reloads |
|---|---|---|---|
| Scale 1 → 3 → 1 once (`dns-edge-scale-1-3-1.jsonl`) | 503 | 0 | 0 |
| Scale 1 → 3 → 1 ten times, 20 of 20 operations succeeded (`dns-edge-10-cycles.jsonl`) | 3,282 | 0 | 0 |

The same test against real Caddy on the owner's machine (gated
`TestDNSEdgeScalesWithoutLoss`) lost 0 of 348. Before the fix, the same
scaling lost 8 of 428. VPS-H-001 is resolved.

An earlier ten-cycle attempt didn't count: every scale-up in it failed
(its output wasn't kept), so no instance moved. The run above logged
every command; all twenty succeeded.

## A guardrail slip

For the two no-op reload experiments Claude edited Caddy's configuration
by hand through its admin socket (forcing reloads, and turning HTTP/3
off for the second). The plan says not to hand-edit Caddy on a server.
The configuration was restored afterwards and checked identical to the
one OAP wrote (`oap-gen-5`).
