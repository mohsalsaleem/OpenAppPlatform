# Reviewed native route membership: staging pilot

`scripts/coolify-route-membership.py` discovers a managed image component's current
healthy bindings through OAP, reviews additions/removals, and updates an existing
isolated native route. It supports Coolify staging with the existing Traefik
file-provider layout. This is owner CLI tooling, not a dashboard route action or
continuous reconciliation service.

The pilot never creates a route or adopts arbitrary YAML. The existing file must
be JSON emitted by the OAP renderer, with the expected hostname, router/service
names, health policy and only backends belonging to the selected component.
Custom routes and foreign backends remain native-owned. Grouping does not grant
publication rights. This pilot requires a human owner session and managed image
bindings; agents, observed/adopted resources and production targets are rejected.

## Review and apply

First complete the supported replica lifecycle operation in OAP. Every desired
instance must be healthy, with exact ordinals and retained native ownership. Active
or unresolved application work blocks a plan. Retired instances remain available
as ownership evidence for removal, but do not become new backends.

The Coolify adapter now implements the existing recorded retirement contract for
managed image instances. It checks target scope and the OAP ownership marker,
requests a native stop with `docker_cleanup=false`, and observes the resource
stopped before OAP commits a lower desired count. The application resource is
retained. The stop API returns no job ID: the recorded resource UUID is an instance
confirmation reference, not a fabricated provider deployment identity. Uncertain
responses require normal retirement recovery; observation never repeats the stop.
Unknown state, source strategy or changed ownership retains the recovery fence.

For the retained demo:

```sh
python3 scripts/coolify-route-membership.py plan \
  --base-url https://oap-staging.mohsal.dev \
  --credentials-file .local/staging-workspace-credentials.json \
  --ssh-host hz_primary \
  --application 7cc13fa9d4bb51314240aabdcc7b851d --component api \
  --name oap-preview-7cc13fa9d4bb --domain oap-api-preview.mohsal.dev \
  --plan-file .local/route-membership-plan.json
```

Inspect the plan's added/removed URLs and native configuration. It contains hashes
of the application definition and target authority, exact member identities,
native proxy/engine identity, and the current native file hash. Credential values
are never included. Use the credentials file's exact trusted OAP origin and an
existing trusted SSH alias for the target server.

```sh
python3 scripts/coolify-route-membership.py apply \
  --base-url https://oap-staging.mohsal.dev \
  --credentials-file .local/staging-workspace-credentials.json \
  --ssh-host hz_primary --plan-file .local/route-membership-plan.json \
  --acknowledge-routing-change
```

Apply rechecks owner access, app version/definition, target, bindings, native proxy
and file contents. Stale or altered plans are rejected. Cooperating publishers
share a native-host lock; the existing file is checked again before an atomic
replacement. Symbolic links and oversized files are rejected. Identical retries
return `alreadyApplied` without replacing the file. After an uncertain response,
inspect the native file and retry the same reviewed plan rather than guessing.

## Limits and acceptance

The SSH/server association is explicit owner configuration, not inferred or
certified by OAP. Native UI editors do not participate in this publisher's lock;
external writes can race the final check/replacement. Lifecycle and native file
publication also do not form a distributed transaction. Recheck health and traffic
after applying; retain native access and the previous reviewed configuration.

The pilot preserves the existing route's hostname/TLS/health layout. Domain creation,
route deletion, credential-store integration, background membership updates,
draining, bare Docker proxy routing and agent publication are not delivered here.
Stopped backends are excluded by native health checking before reviewed membership
removal; this is not graceful draining or zero downtime.

`make test-local` covers permission, uncertain work, binding ownership, replica
counts, foreign/custom configuration and stale/tampered plan rejection. A separate
native Docker file-protocol drill verifies drift preservation, atomic replacement,
proxy identity, symlink rejection and idempotent retry without inode replacement.
Its fixture is not an HTTP proxy; the real HTTP scale/route journey is verified
against the retained Coolify staging demo.
