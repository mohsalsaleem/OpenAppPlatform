# ADR 0001: Change route membership without reloading Caddy

Status: **accepted by Claude under the owner's standing instruction** to
complete the work with the best approach (2026-10-20); the owner should
confirm or reverse it. Implemented as `caddy.DNSEdge`, on by default in
oapd (`--edge dns`); `--edge static` keeps the reload-per-change edge.

## Context

Every route change OAP makes (scaling, deploying, removing an instance)
replaces Caddy's configuration with `POST /load`. On vultr_oap every such
reload, even of an unchanged configuration, reset 1 to 3 in-flight HTTPS
requests out of a steady 20 per second
([evidence](../../evidence/vultr-oap-2026-10-20/README.md)). This is
VPS-H-001's root cause. The planner's traffic-safety order prevents the
other losses but can't prevent this one, because the reload itself drops
connections. A scale from 1 to 3 to 1 makes six reloads.

## Options

1. **Membership through DNS that oapd serves.** Each routed component
   gets one static Caddy route whose upstreams come from
   `dynamic_upstreams a` (built into Caddy) for a name such as
   `website.nisha-shipping.oap`, refreshed every second, resolved by a
   small DNS responder in oapd on the installation's network. Route-add
   and route-remove change oapd's answer, with no Caddy reload.
   Edge-reload-confirmed becomes "Caddy has asked since the change",
   which oapd sees as a DNS query. Drain keeps using Caddy's per-upstream
   request counts. Caddy reloads only when hosts or paths change (a new
   application or domain). It needs no new dependency: the responder
   answers A queries only, in the standard library. The Edge port and its
   contract suite stay the same; the Caddy adapter changes.
2. **Batch route changes into one reload per operation.** Fewer reloads
   (two instead of six for 1 → 3 → 1), but every one still drops
   requests.
3. **Make oapd the edge.** A reverse proxy inside oapd changes membership
   in memory, but OAP would take over TLS and certificates
   (`certmagic`, a new dependency) and lose Caddy's maturity.
4. **Accept and retry at the client.** It doesn't help browsers or other
   clients OAP doesn't control.

## Proposal

Option 1. Acceptance: on a real server, scaling 1 → 3 → 1 under
continuous HTTPS traffic loses no request over at least 10 runs, and
membership changes cause no Caddy reload (checked in Caddy's admin log).
The simulation gains a mode where membership changes don't reload, and
R2.8's tests run against it too.

## Consequences

- Caddy reloads become rare: only route shape changes.
- oapd runs a DNS responder on the installation's network, reachable
  only from that network.
- Confirmation depends on Caddy's refresh interval (about 1 s).
