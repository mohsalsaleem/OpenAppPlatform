# Runtime Secret References

M0 P0.1 adds runtime inputs for OAP-owned image workloads on Docker and Coolify.
Keep existing native secrets and variables in their operator. Grouping or observing
an existing resource does not transfer configuration authority.

Definitions contain reference names, never values:

```json
{"secretRefs":{"DATABASE_URL":"database-url-v1"}}
```

Secret References appears beside plain environment variables in application
configuration. It accepts reference names only. A key cannot be both a secret,
plain variable or service connection. Observed/adopted resources cannot use this
field. Saving a definition does not deploy it or prove the reference is available.

## Administrator Setup

First create the logical application and record its ID. Configure this non-secret
JSON in the OAP server's `OAP_RUNTIME_INPUTS` environment variable:

```json
[{
  "reference":"database-url-v1",
  "targetId":"coolify-staging",
  "applicationId":"REPLACE_WITH_APPLICATION_ID",
  "component":"api",
  "variable":"DATABASE_URL",
  "valueEnv":"OAP_SECRET_DATABASE_URL_V1"
}]
```

Supply `OAP_SECRET_DATABASE_URL_V1` through the existing operator's runtime secret
configuration, or the administrator's uncommitted local environment for bare
Docker. Restart OAP to load the grant. Values are loaded once per process, must
contain 8–4096 bytes without NUL, and the resolved component environment is limited
to 16 KiB. Use high-entropy credentials. References cannot select arbitrary OAP
credentials: only explicitly granted `OAP_SECRET_` variables can be read.

Grant the worker separately when it needs the same credential. Authorization binds
target, application ID, component name, destination variable and reference name.
App-scoped API access alone cannot create grants or read values. There is no secret
read/write endpoint, vault, external secret service or automatic provisioning.
Bindings require owner mode; preview mode cannot load them.

## Deployment and Recovery

Missing or unauthorized references block release admission before any provider
mutation. Admission stores only private, release-scoped fingerprints alongside
reference-only snapshots. Preparation resolves an ephemeral component for the
adapter; values never enter saved definitions or public release models. Existing
Coolify variable identity/journal checks still prevent takeover of unrelated native
variables. Runtime values reach native Docker container environment or Coolify
runtime variables; native operator/daemon administrators can inspect them.

References are immutable in this slice. Keep admitted values available across OAP
restarts. Changing a value or grant after admission blocks further preparation and
dispatch with an owner-visible attention state; restore the admitted binding before
using the existing recovery workflow. Image-only rollback verifies the resolved
native configuration and pins inputs again. Definition restore restores names,
not secret values. Metadata backups contain private fingerprints, not credentials;
restore the operator-held inputs separately before recovery.

REST and SSE log output masks exact configured values. Logs fail closed when any
historical reference for that application is missing or changed, so an old value
cannot escape after a server restart. Keep retired references available while their
application log history remains accessible. Redaction does not sanitize transformed,
encoded or partial secrets, and cannot control public responses produced by user
applications. Applications must also avoid logging credentials.

Rotation, historical secret retention policies, build-time inputs, registry
credentials and a general encrypted/BYOK store remain outside this slice.
