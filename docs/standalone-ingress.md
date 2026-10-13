# Standalone application ingress — M3.1

Caddy is the selected standalone proxy. Existing Coolify routes keep their native
Traefik integration. M3.1 now supports routed deployment and specification-driven
scale-down through an explicitly **owner-provisioned dedicated Caddy peer**.
Default Docker targets keep ingress disabled. Automatic proxy provisioning and
multi-application lifecycle aggregation remain open; public Let's Encrypt
acceptance is deferred until a public server/domain is available.

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

`spec.ingress` belongs to `oap.dev/v1alpha1 Application` and is discoverable through
`oap schema`. Structural validation, export and semantic diff preserve it.
Operational admission requires `applicationIngress` and the owner's exact domain
allowlist. Ordinary unconfigured targets reject ingress without changing metadata
or workloads. Configuration save still never publishes routes.

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

The controller now records the frozen candidate and native precondition in a private
PostgreSQL publication journal before dispatch. The target's publication lock,
current grant, exact peer identity, owned bindings and health are rechecked.
A synthetic `@ingress` step completes only after the native hash is observed.
Failed workloads preserve previous ingress. Attention after a lost response can
use the existing recovery plan with `component: "@ingress"`, `ordinal: 1` and the
exact operation timestamp; recovery observes the recorded candidate and never
sends another write. Pre-dispatch preparation retry remains separately fenced.
Caddy's ETag contract is experimental; the pinned-version native test stays in
the local gate.

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
The same real-container test also checks two applications sharing one proxy,
independent HTTP/HTTPS modes and recovery of both after restart. A system test
checks unsupported ingress create/configure returns rejection with unchanged
metadata and no runtime dispatch. Unit tests cover stale/foreign/duplicate membership, unsafe paths/backends,
configuration drift, concurrent CAS conflict, rejected config and lost responses.

## Owner-configured operational slice

The owner provisions one dedicated Caddy container with:

- The target label `io.openappplatform.ingress-target=<target-id>`.
- A private Unix admin socket, reachable by the OAP service identity, never mounted
  into application containers. No published administration port or Docker socket.
- Durable `/config` and `/data`, and `caddy run --resume`.
- Explicit traffic ports; initial local testing uses loopback port bindings.

Only a dedicated bootstrap peer is claimed. To obtain its current configuration
commitment without printing its body:

```sh
go run ./examples/ingress-authority --socket /private/socket/admin.sock
```

Add owner-managed `settings.ingress` to the Docker target file, then start/reload
OAP through its normal host procedure. Applications cannot set these fields:

```json
{
  "containerId": "<full-immutable-container-id>",
  "adminSocket": "/private/socket/admin.sock",
  "proxySocket": "/run/oap-ingress/admin.sock",
  "domain": "app.example.test",
  "bootstrapHash": "<hash-from-owner-helper>",
  "httpPort": 8080,
  "httpsPort": 8443,
  "internalCa": true
}
```

`adminSocket` is the OAP-visible path; `proxySocket` is the path inside Caddy.
Ports are Caddy listener ports; host mappings remain owner-managed. The configured
peer must be unprivileged, avoid host networking and isolate administration.
Owner-provisioned mode supports **one routed application/domain per configured
Docker target/peer**. The shared compiler can aggregate 32 applications, but that
is not yet enabled as a multi-application controller feature. Another application
cannot claim an existing domain or ingress authority. Domain changes, disabling
an established route and proxy identity handoffs need a later explicit workflow.

For HTTPS the target explicitly enables internal CA. Public ACME execution is
blocked in this operational slice. Trust only the intended local CA for local
verification; local CA acceptance is not public certificate acceptance.

The agent's loop is:

1. Validate/diff the application specification; use the existing `configure` plan
   to save changes. Save does not change traffic or start containers.
2. Execute a `deploy` plan and wait. New route membership appears only after owned
   routed instances report native healthy state. Images without a native health
   check need an explicit HTTP check before they can become route members.
3. Increase a component count with configure → deploy; selected-component releases
   also retain a publication step and read claims on routed siblings.
4. For a lower count, send a `scale-down` action plan with the **desired complete
   specification**, changing exactly one count and no other field. It withdraws
   route members, waits a persisted 31-second grace, stops exact excess instances,
   then commits the count. Named agent authority must include `scale-down`; exact
   owner approval is enforced when requested. Worker retirement needs no route.
5. Verify external behavior separately from operation success.

Example scale-down action body:

```json
{
  "action": "scale-down",
  "applicationId": "<app-id>",
  "expectedVersion": 3,
  "specification": "<complete Application object with api.instances=1>",
  "requireReview": true
}
```

Replace the example's specification placeholder with a JSON object, not a string.
Use `oap --file change.json action-plan`, inspect the returned plan, then
`oap --plan <id> --file empty.json execute-action` (`empty.json` contains `{}`).
The grace period is conservative and bounded, not proof that arbitrary long-lived
streams drain forever. Its dispatch timestamp survives controller replacement.

**Acceptance:** the named-agent API/CLI test releases frontend/API, scales API
1 → 3 → 1 under continuous public-path requests, verifies all three boot IDs,
keeps traffic flowing with the controller stopped/recreated, replays retirement
without duplication, and injects a lost Caddy response followed by observation-only
recovery without another native write. It writes `ingress-lifecycle.json`.
Native fixture tests separately verify local-CA HTTPS and shared-proxy restart.

**Remaining M3.1:** packaged automatic proxy provisioning, safe multi-application
publication/claims, domain/port handoff and removal, extended drain boundaries,
HTTPS in the integrated agent journey, and separate proxy footprint accounting.
Public ACME issuance/renewal remains deferred and does not block local work.

Standard replacement can cause downtime. Neither blue-green nor universal
zero-downtime behavior is implied by these foundation tests.
