# Existing operator setup, reusable layouts and configuration review

M0 batch 3 adds a guided setup path without replacing the native operator workflow.

## First connection

Deployment targets has a Coolify setup guide. It prepares a JSON entry with an
operator URL, project/server IDs, environment and credential environment-variable
reference. It does not accept/store token values, perform a browser-originated
connection test against arbitrary URLs, or apply server configuration automatically.
Install the operator token on the OAP controller and merge the entry into its target
file; preserve existing entries and use a stable unique target ID per scope.

After the controller loads configuration, Verify connection makes a bounded,
read-only check against the configured project/environment. An empty valid scope
is different from unreachable/unauthorized scope. Errors omit credentials and
provider bodies. No build, deploy, restart, webhook, domain or storage mutation occurs.
App-scoped credentials cannot enumerate or check other targets. Known controller
secret references are rejected by parsing and the adapter factory.

## Additional environments on a trusted connection

A human owner can select an already configured Coolify connection, name another
existing operator environment and register its OAP target scope. The server derives
URL, project ID, server ID and credential reference from the trusted existing target;
the browser cannot supply alternate authority or token values. Verification is GET
only. Persistence is create-only, and duplicate target IDs or operator/project/env
scopes conflict under a transaction lock. Operator environment creation is separate:
the existing scope must be accessible before OAP saves the connection.

The new target is stored as OAP metadata and survives controller restart. Existing
file-backed target IDs remain authoritative; do not reuse/repoint their IDs. First
connection and a different project/server still use the server-assisted guide.
This deliberately avoids introducing the full M1 encrypted credential store here.

## Reusable component layouts

Group services can reuse an existing environment's component names. Select its
layout, select the new environment's existing resources, then review every mapping.
The source endpoint exports only application name, definition version and component
name/kind. It excludes images, variables, credential values/references, routes,
resource IDs and native source settings. No automatic build workflow or permission
handoff is copied. Suggestions follow selection order and remain editable in review.
This is naming/layout reuse, not a clone of running resources or a full configuration
promotion. Linking the resulting environment into the logical application remains
an explicit owner action.

## Readable configuration diff

Settings shows current/proposed image, ports and instance count, plus changed
variable/service/endpoint keys before saving. Values and addresses in variable and
endpoint summaries are hidden; app-local component references are visible. Saving
still requires the current definition version and changes future releases only.
Observed runtime fields remain locked and no save triggers a deploy or restart.

API additions:

- `GET /api/v1/targets/{id}/connection`: configured scope check and capabilities.
- `POST /api/v1/targets/scopes`: human owner submits `id`, `name`, `sourceTargetId`
  and another existing `environment`; no URL/credential fields accepted.
- `GET /api/v1/applications/{id}/layout`: sanitized current naming layout, available
  within existing application read scope.

System/browser acceptance includes immutable target authority, no operator writes,
create-only/duplicate rejection, forbidden credential references, scoped-token/viewer
boundaries, sanitized layout export/reuse, masked diff values, guided recipe output
and owner environment registration through a local read-only Coolify fixture.
Full secret entry/vault, full configuration templates/promotion and native delegated
source commands remain follow-up work. The baseline M0 batch 3 contract is complete
with guided trusted setup, layout reuse, diff review and ownership presentation.

The slice is deployed in Coolify staging. Live scope/layout verification and
failed-scope persistence checks passed; successful owner registration and browser
reuse/diff behavior were exercised against isolated local fixtures.
See [verification](verification.md) for the exact build and evidence limits.

## Workflow ownership and adapter support

Application overviews describe build, deployment, restart, configuration and
management handoff for each component. Observed components retain the existing
workflow; OAP does not start their builds or deployments. Managed components use
image releases coordinated by OAP and executed by the target. An optional builder
requires separate configuration. Saving configuration remains separate from deployment.

After a read-only connection check, target cards list adapter support for standard
releases, restart, retirement, variables, service DNS/endpoints, image handoff, native
source observation, rolling and blue-green. These are adapter declarations, not a
claim that every resource is eligible or that the current user can mutate it.
Native source commands and source handoff remain unavailable. Deploy is disabled
until standard release support is loaded; existing API authorization stays authoritative.

Verified staging release: `a98365a403156705f6a0e3057ef7b0cc0ce34697`, deployment
`vtojokj23sjb5koi34ssunes`, healthy at https://oap-staging.mohsal.dev. Local exact-tree
run `20261010-210926-dae2f5ed` passed unit, system, isolated Docker and browser checks.
A live browser verified owner sign-in, managed component ownership and the read-only
Coolify capability check. The retained source release and both replica digests stayed
unchanged and healthy. Observed-component ownership was tested in the local fixture.
Schema remains at migration 010; the previous staging image and tested database
backup remain available. This completes the presentation slice of M0 batch 3, not
native lifecycle handoff or configuration promotion.

## Reproducible operator-first acceptance

The opt-in acceptance run requires explicit, distinct staging image/source resource
IDs. It uses the exact-tree local test image and a disposable PostgreSQL database,
without mounting a Docker socket or connecting to the platform's durable database:

```sh
make test-local
python3 scripts/run-local.py python3 scripts/test-operator-acceptance.py \
  --image-resource <existing-staging-image-id> \
  --source-resource <existing-staging-source-id>
```

The adapter passes through a GET-only forwarding boundary. Non-GET requests fail
locally and are never forwarded to Coolify. It selects the named resources from the
configured project/environment, groups them only in the disposable OAP database,
checks health/log access, verifies environment identity, and rejects OAP deploy,
restart and native source handoff. Configuration fingerprints compare native source,
trigger settings, domains, ports, image, full environment records and storage records.
Provider bodies, secrets and logs are never printed. Independent native deployments
can change fingerprints during this check; a failure is investigated, never overwritten.
No production scope, provider creation, trigger changes or metadata imports into the
hosted platform are performed. Multi-environment linking remains covered by local
system/browser tests; this staging test verifies a mixed native application in its
existing environment.

Staging acceptance passed on 10 October 2026 using image resource
`ibgspvrw2hvvm6aa2a9sblmv` and source resource `lf1ggsrabuf3qifyj305kreo`, both
in the existing dedicated staging environment. Test
`TestLiveCoolifyObserveOnlyAssembly` passed in 15.24 seconds with zero provider
writes and zero OAP releases. Full local run `20261010-212147-667b34a3` also passed.
No builder or registry was configured in the disposable controller. The test database
and network were removed afterward; the hosted platform and provider fixtures were
not redeployed. This is existing-resource acceptance, not a native lifecycle handoff
or proof of cross-environment production behavior.
