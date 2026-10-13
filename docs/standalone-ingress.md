# Standalone application ingress — M3.1

Caddy is the selected standalone proxy. Existing Coolify routes continue to use
Coolify's native Traefik integration. M3.1 is in progress: the internal compiler
and conditional publisher exist, but application plan/apply and the durable scale
loop do not use them yet. No new public ingress field or agent command is enabled.

## Routing foundation

`internal/ingress` accepts a proposed domain/path definition, a manifest and exact
owned instance observations. It generates bounded native Caddy JSON, including
private Unix administration, explicit HTTPS policy and saved configuration.
No Caddy plugins, Docker discovery provider or Docker socket mount are required.

The proposed definition is:

```json
{
  "domain": "app.example.com",
  "https": true,
  "routes": [
    {"path": "/", "component": "frontend"},
    {"path": "/api", "component": "api", "stripPrefix": true}
  ]
}
```

This is an internal proposal, **not an accepted application specification field**.
The next controller work will embed it in the versioned specification with domain
ownership and target capabilities enforced at admission and execution.

Routes target owned web components with private ports. Workers, database links,
observed/adopted resources and fixed host ports cannot be route backends. Paths
are literal segment prefixes: `/api` matches `/api` and `/api/*`, while
`/apiculture` falls through to `/`. Longest prefixes win. Prefixes are preserved
unless stripping is explicitly enabled. Unknown hostnames return 404.

Only ready members are included. Every supplied member must have matching
application/target/component/ordinal, a unique private runtime IP at the declared
port, and observation age at most 15 seconds. No ready backend for a route blocks
publication. The future controller must derive these fields from exact owned
bindings; the compiler's checks do not authenticate evidence by themselves.

Configurations and hashes are deterministic regardless of input ordering.
The bounded reverse proxy uses round-robin selection and passive failure tracking.
Passive tracking does not prevent the first request failing when a backend dies.
Streaming close delay is not proof of drained HTTP requests.

## Publication and recovery

The client connects exclusively through an installer-authorized Unix socket.
The generated socket is owner-only (`0600`); production packaging must arrange
matching service identity and private directory/storage permissions. Application
containers must never mount this directory, Caddy configuration/certificate
storage or a Docker socket. Agents receive no direct Caddy administration access.

Publication reads the entire config and its ETag, then sends one conditional
`POST /config/` with `If-Match`. It never unconditionally replaces a changed config
and never automatically retries a mutation. The returned receipt is `applied`,
`unchanged`, `blocked` or `unknown`, with expected/candidate/observed hashes.
Native error bodies are withheld. A lost response stays uncertain; a later read
matching the candidate can confirm the existing configuration without another write.

The future controller must persist publication intent before dispatch, enforce
current authority, serialize publications and record the observed outcome.
This client alone is not a durable operation ledger. Caddy's ETag contract is
experimental; the pinned version's real-container test is part of the local gate.

Caddy persists API changes. Launch it with `caddy run --resume` and durable config
and certificate volumes so proxy restarts recover routes and certificates without
OAP. API reload behavior follows the [Caddy API contract](https://caddyserver.com/docs/api).
A running proxy continues independently of the controller; loss of proxy volumes
or the server is a different failure and requires recovery.

HTTPS selects the Let's Encrypt production ACME directory explicitly. An
installer-only internal-CA option supports offline fixtures. Public issuance,
renewal, DNS ownership, external reachability and enable/disable transitions still
need controller/installer acceptance; local CA success is not public ACME evidence.
See [automatic HTTPS](https://caddyserver.com/docs/automatic-https).

## Reproducible acceptance and next work

`python3 scripts/test-env.py run` loads the digest-pinned Caddy fixture into a
fresh disposable Engine. `TestCaddyStandaloneRoutingPublicationAndResume` checks
real requests reaching three API replicas, segment precedence, prefix stripping,
unknown-host rejection, conditional publication/replay, removal of two route
members, HTTP restart recovery, and verified HTTPS/internal-CA restart recovery.
Certificate verification uses the fixture root; insecure TLS verification is not
used. The test writes `ingress-foundation.json` with explicit scope and limits.
Unit tests cover stale/foreign/duplicate membership, unsafe paths/backends,
configuration drift, concurrent CAS conflict, rejected config and lost responses.

Next, staying within M3.1:

1. Add ingress to the specification and shared plan/diff/execute contracts, with
   exact domain/port ownership. Saving configuration must not publish traffic.
2. Package and claim a dedicated proxy, config/certificate volumes and private
   socket without granting workloads access. Decide server-wide aggregation and
   application-network attachment before supporting multiple routed apps on 80/443;
   the current compiler produces a complete single-application proxy config.
3. Connect frozen release membership to durable publications and verification.
   Add only ready instances; fence stale outcomes and preserve routes on outages.
4. Make spec-driven scale-down withdraw, verify and drain exact members before
   retirement. In-flight requests and streaming timeouts need explicit tests;
   route removal alone does not satisfy this requirement.
5. Prove the full named-agent 1 → 3 → 1 scale loop, controller-down continuity,
   optional public ACME and separate proxy footprint. M3.1 remains open until then.

Standard replacement can cause downtime. Neither blue-green nor universal
zero-downtime behavior is implied by these foundation tests.
