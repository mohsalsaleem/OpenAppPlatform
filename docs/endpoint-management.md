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
TLS readiness probing, production publication or bare Docker proxy transport.

## Reproducible acceptance

`make test-local` includes filesystem drift/symlink/size/idempotency tests, real
PostgreSQL system tests for stored reviews and publication fences, controller
recreation after both sides of a simulated publication interruption, safe cancellation,
credential/ownership drift, human-session API permission and mutation audit, and a
browser journey for review, uncertain outcome and resume. These simulated crash
boundaries do not replace live proxy traffic verification on staging.
