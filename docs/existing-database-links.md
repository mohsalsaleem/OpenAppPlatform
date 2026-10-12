# Existing Database Links

P0.3 groups an existing PostgreSQL resource with managed frontend/API/worker
components. Coolify supports standalone PostgreSQL resources in the configured
project/environment. Direct Docker supports explicitly scoped existing containers;
the human owner identifies the PostgreSQL resource, rather than automatic engine
inference. Compose databases and other engines remain unsupported in this slice.

A human workspace owner links from the application overview. Review the existing
resource, component name, credential-free PostgreSQL endpoint and application-member
health/log access. Linking changes OAP metadata and reserves the resource; provider
requests are GET-only. An active/uncertain release or route publication, stale
application version, duplicate name or reserved resource prevents linking.

Databases have `kind: database`, `management: observe`, one immutable resource
binding and no runtime overrides. The endpoint is descriptive metadata; credentials
belong in scoped secret references on API/worker, not the database definition.
Deployments skip databases. Explicit database deploy, restart, image replacement,
management handoff and runtime reconfiguration are rejected. Configuration editing
cannot change link identity or endpoint. No SQL execution, provisioning, migrations,
network mutation, backup scheduling or restore API is introduced.

Startup dependencies require the linked database's native healthy result. This
proves operator-reported availability, not that a consumer's credential or query
works. The example's API/worker native probes check actual authenticated read access.
A database failure is visible in the overview and holds dependent preparation;
recovery uses the existing versioned recovery workflow. Existing operator updates,
credentials, storage and backups remain authoritative.

The controller does not import database passwords or native connection strings.
App health/metadata and permitted bounded logs use existing app access rules. Linking
is owner-session-only; app-scoped agents cannot add database access or take lifecycle
authority. Native log support depends on operator version/token permissions. OAP's
runtime-reference log masking is bounded to configured values, not arbitrary SQL
content; do not log sensitive queries or credentials in application code.

The API is `POST /api/v1/applications/{id}/databases` with `name`, `resourceId`,
`endpoint`, `expectedVersion` and `acknowledge: true`. After a lost response, reload
the application to inspect the atomic link; retrying a stale version returns 409.
Credentials and endpoints are never copied automatically from another environment.

See the [complete example](../examples/application-breadth/README.md). Local and isolated staging acceptance passed on 12 October 2026.

## Accepted Four-Component Journey

Implementation commit `2e69c36` passed the full reproducible local gate (16 browser
checks, native Docker/PostgreSQL integration and race checks). SQL credentials stayed
reference-only. API and worker authenticated with a restricted role; a prohibited
write failed. A database outage held API preparation and recovery restored consumer
connectivity without database configuration/data changes.

[The staging application](https://oap-staging.mohsal.dev/applications/bb2f8cb890eacc584ead8db1507567d3)
contains frontend, API, worker and database, all healthy. Release
`770180d2f27fc6590acc08df31bd7bd7` updated only API/worker. The frontend reached an authenticated
API SQL read; worker SQL reads and native database logs worked. Definition/release
responses omitted credentials. Provider configuration, native container configuration,
storage bindings and seeded data matched their pre-link baseline. The database
fixture was created and seeded through the operator before OAP linked it; native
backups remain operator-owned. No production data or native source triggers changed.
