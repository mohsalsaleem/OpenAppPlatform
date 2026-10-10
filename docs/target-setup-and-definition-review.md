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
Full secret entry/vault, full configuration templates/promotion, native delegated
source commands and richer per-operation capability presentation remain follow-up
work. Batch 3 remains active until its remaining contracts are delivered.
