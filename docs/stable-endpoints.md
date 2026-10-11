# Stable endpoints and routing: M0 batch 5

OAP reuses existing native routes wherever the operator can manage them. Grouping
and inspection never rewrite Coolify domains or create another proxy. The current
runtime API exposes a primary instance URL; it does not yet model a shared route
over all component replicas.

The Coolify adapter projects the first valid HTTP(S) URL from its comma-separated
domain field for Open Service. Surrounding whitespace is removed. Invalid URLs,
embedded whitespace, unsupported schemes and credential-bearing URLs are skipped.
All native configured domains remain unchanged; secondary domains are not yet
listed by the OAP UI. No endpoint reachability or TLS readiness is inferred from
this projection. Bare Docker retains its configured host-port endpoint behavior.

## Ordered acceptance work

1. Verify native endpoint identity and domain preservation through replacement,
   restart and image rollback, including continued requests with OAP stopped.
2. Define shared component route authority and capabilities. Reuse Coolify's
   native proxy where supported; specify the minimal routing adapter for bare
   Docker. Inspect existing ownership and configuration before any route mutation.
3. Exercise supported replica changes, healthy backend selection and stopped
   backend removal. Keep traffic and operation identities independent of the
   controller process and avoid a controller request in the application path.
4. Verify custom domains/TLS and connection draining. Standard rollout remains
   the default; optional rollout strategies need separate acceptance. Do not
   promise zero downtime until the request/connection failure cases are tested.

The first slice fixes endpoint projection only. Shared routing, TLS provisioning
and draining remain incomplete. Unit tests cover multiple domains, malformed or
unsafe candidates, native ports/paths and unchanged native domain configuration.

The [connected preview](../examples/connected-preview/README.md) is a verified,
retained hands-on checkpoint: frontend plus two managed API instances and a reviewed native
Traefik route. `scripts/render-coolify-route.py` renders bounded provider config;
it does not publish it or imply an automatic routing adapter. Verify real TLS,
replacement/rollback, backend outage and controller independence as recorded in
the [staging acceptance](connected-preview-acceptance.md). Automatic membership and draining remain
separate follow-up work.

The [reviewed membership pilot](route-membership.md) adds owner plan/apply tooling
over live OAP bindings and the existing native route file. It removes manual URL
copying, rejects observed drift and preserves custom native configuration. It is
staging-only CLI tooling; automatic/background or dashboard route publication
and draining remain incomplete.
