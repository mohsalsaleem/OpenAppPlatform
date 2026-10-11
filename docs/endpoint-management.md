# Reviewed endpoints in OAP: Coolify staging

M0 batch 5 now includes an Endpoints tab and authenticated API for reviewing and
publishing membership to an existing native Traefik file. The current implementation
is deliberately bounded to explicitly configured Coolify staging routes over
OAP-owned managed image web components. It does not create domains, change native
GitHub workflows or take ownership of arbitrary proxy configuration.

## Host configuration

Set `OAP_NATIVE_ROUTES` to a JSON array of trusted, non-secret bindings:

```json
[
  {
    "applicationId": "7cc13fa9d4bb51314240aabdcc7b851d",
    "component": "api",
    "targetId": "your-staging-target",
    "name": "oap-preview-7cc13fa9d4bb",
    "domain": "oap-api-preview.mohsal.dev",
    "healthPath": "/health/ready",
    "directory": "/native-routes"
  }
]
```

The deployment operator must bind the native file-provider directory into OAP
and grant its non-root UID the permissions necessary for atomic file replacement.
Existing native proxy configuration remains the traffic path. The directory must
be persistent; mounting a single file prevents atomic replacement. A writable
proxy directory is sensitive host authority: do not expose it to browser input,
agents, untrusted plugins or application containers. Configure only the named
staging route; no Docker socket, SSH key, native API execution endpoint, extra proxy
or additional runtime service is needed by this transport.

OAP accepts only names, components and domains from these trusted bindings. A
browser cannot supply paths, upstream URLs, TLS resolver names or proxy rules.
The server-host association is explicit administrator configuration, not a
certified discovery result. The persistent directory identity is frozen in each
review. Replacement of that authority blocks applying or cancelling uncertain
work until it is inspected; do not change a mount while a publication is unresolved.

## User flow and API

Open an application's **Endpoints** tab. Inspect the shared URL and current versus
desired backend count. **Review Routing** saves a plan and shows exact additions
and removals. **Apply Routing** rechecks the application definition, target including
its credential reference, binding ownership, native directory and file hashes,
and the healthy desired members. A changed precondition rejects the plan.

- `GET /api/v1/applications/{id}/routes`: bounded endpoint state and the latest
  twenty saved reviews. Existing app-scoped read access applies.
- `POST /api/v1/applications/{id}/route-reviews`: `{ "name": "oap-route",
  "expectedVersion": 4 }`. Creates an immutable review.
- `POST /api/v1/applications/{id}/route-publications/{plan}`:
  `{ "action": "apply" }` or `{ "action": "cancel" }`.

Mutations require the human workspace owner's session, CSRF protection and normal
mutation audit. The same owner must apply their saved review. Owner-issued agent
credentials cannot publish. The plan ID is the retry identity; callers cannot
alter its stored membership or native bytes. Retrying an applied plan returns its
recorded outcome without writing again; it does not claim a new live verification.

## Crash recovery and fencing

The application admission lock serializes review/application with configuration
and release admission. After preflight, OAP commits `publishing` intent **before**
writing the native file. PostgreSQL enforces a persistent fence against deployment
insertion and definition changes while that intent is unresolved. A process crash
cannot erase the fence. Other applications remain independent.

The file update uses a pinned directory descriptor, rejects symlinks and nonregular
or oversized route files, writes and syncs a bounded temporary file, rechecks the
old bytes, atomically renames, syncs the directory and verifies the result. Matching
retries do not replace the file. Native traffic continues without OAP.

An unresolved intent appears as **Review Recovery**. **Resume Update** verifies the
same reviewed authority and can confirm already-written desired bytes after a
crash. **Cancel If Unchanged** releases the fence only when the original native
bytes remain. It never overwrites or silently accepts a third native configuration.
A prepared, never-dispatched review can be discarded without compensation.

Database publication state and native publication are not a distributed transaction.
Native UI editors and the older CLI publisher do not share the API admission lock
and can race the final native check. Use one publishing workflow at a time. A
native edit or changed authority may require administrator inspection while the
fence remains; there is no bypass button or automatic guess about who owns drift.
This workflow has no automatic reconciliation, draining, route creation/deletion,
production publication or bare Docker proxy transport. The subsequent
[Domain & TLS readiness](endpoint-verification.md) workflow adds bounded verification
of the existing configured endpoint; it does not create or issue a new domain.

## Reproducible acceptance

`make test-local` includes filesystem drift/symlink/size/idempotency tests, real
PostgreSQL system tests for stored reviews and publication fences, controller
recreation after both sides of a simulated publication interruption, safe cancellation,
credential/ownership drift, human-session API permission and mutation audit, and a
browser journey for review, uncertain outcome and resume. These simulated crash
boundaries do not replace live proxy traffic verification on staging.

## Live staging acceptance: 11 October 2026

Feature commit `e34b14fa7139cec459be5f6672e40929eaad67a0` passed the complete
local gate `20261011-145614-437abd03` before push and deployment. The existing
staging application received an explicitly approved native-directory mount;
publication remained restricted to the named connected-preview route.

A baseline version-4 review was rejected after scale-up, preserving native bytes.
Release `39cc2955133d0acbf2c708ece8399f82` verified three API replicas. The owner
used Endpoints to review and apply publication
`144c36e74a33e71e2de816c7c32b23f9`, adding only the third backend. Twenty-four
real frontend requests reached all three instances with the original frontend
boot identity unchanged.

The owner used the UI to retire the third instance. Release
`9b6dfe31d07f8bde710d1772e17af34c` confirmed retirement before committing version
6 with two desired API replicas. Endpoints publication
`cfe10f8f4cd7ef804302302761448397` removed only the retired backend. Twenty-four
requests reached both survivors, the frontend process stayed unchanged, and both
publication retries returned recorded applied receipts without changing native
bytes. The final file hash was
`c5203ceaa026d4a3c8c5a245b86c51c48482e66cf76c85defaffcc8787fa76fe`.
The hostname/TLS/routing policy stayed the same; formatting is now the Go
renderer's canonical JSON, so this is not a claim of original byte identity.

With OAP staging's container stopped, eight frontend requests succeeded through
the native route. Restart restored controller access. This drill exposed an
HTTP shutdown deadline while a browser's live event stream remained connected.
The subsequent server change propagates process cancellation to every request,
including streams, and is covered by a real HTTP regression test. This is
controller shutdown behavior, not traffic draining for application instances.

A fresh PostgreSQL metadata backup restored into a new inactive database with
all twelve migrations, the owner, previous release/rollback provenance, both new
release outcomes, applied route receipts and the retired binding verified.
These bounded probes are functional acceptance, not a zero-downtime guarantee.
The private receipts and screenshots are retained under `.local/endpoint-ui-*`.
