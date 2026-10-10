# Build on the existing server and deploy through Coolify

The authorized SSH alias `hz_primary` is the Coolify host: its Docker daemon
contains the retained staging fixtures. The existing registry is bound to
127.0.0.1:5001; shared PostgreSQL is container c0kss8s04coc8804ggskko04 on the
`coolify` network. This avoids another registry or GitHub CI.

`scripts/build-on-server.py` implements the trusted image-builder stdin/stdout
contract. It archives the exact local Git object (no working-tree files or secrets),
checks the configured GitHub origin, sends that archive over SSH, and builds/pushes
on the server. OCI revision/source labels and the registry digest are inspected.
A stable build ID owns a locked intent/receipt journal on the server. Completed
requests reuse their receipt. Interrupted builds hold for owner review; the helper
does not blindly rebuild them. Build output goes to stderr, while stdout contains
only the commit/digest result. Use trusted repositories and a configured SSH alias;
this is a single-owner server builder, not a multi-tenant build sandbox.

The platform supports `OAP_TARGETS_FILE` and `OAP_GITHUB_HOOKS_FILE` for read-only
mounted configuration files in a Coolify image application. Command-line flags
remain compatible. Operator secrets stay in environment references.

Deploy the image by immutable registry digest, with a dedicated staging database
and least-privilege role in shared PostgreSQL. Owner mode and secure cookies stay
enabled. First-run signup requires the private setup secret stored in Coolify.
Source configuration and image receipts are non-secret; passwords and setup tokens
must never enter Git or command arguments.

## Verified product staging

On 10 October 2026, commit `426a5200b301c87bd34093e099092add569fc974`
was built on `hz_primary` and pushed to the existing loopback registry. The running
image is `127.0.0.1:5001/openappplatform/staging@sha256:208144df82ba9f41d879247b0bd419799928fe8b0b8973b60d17c9fe60682940`.
Repeated builder invocation returned the same saved receipt without rebuilding.

The product application is `2bs31xhjzmfi3w0aulusi7zy`, named
`open-app-platform-staging`, at https://oap-staging.mohsal.dev. Deployment
`luz2foosxmamekw47dihqsqt` finished healthy. HTTPS certificate validation, health,
protected API rejection, invalid bootstrap rejection and the rendered setup page
passed. The real owner account remains unclaimed; obtain `OAP_SETUP_TOKEN` from
this application's Coolify environment variables and complete owner signup.

The first deployment failed because custom Docker run options did not produce a
file mount in Coolify's generated Compose file. Configure the targets file using
Coolify's file-storage API (`POST /applications/{uuid}/storages`) instead. Storage
`a2q7lc8ehu6twekwj1bcng8m` maps
`/home/saleem/.config/openappplatform/staging/targets.json` to `/app/targets.json`.
The Docker mount is read/write, but the host file is mode 0444 and the non-root
runtime cannot write it. No operator secrets are stored in that file.

### Persistence and recovery

Shared PostgreSQL holds database and role `openappplatform_staging`; the role has
no superuser, role-creation or database-creation privileges. All nine application
migrations were applied. A custom-format `pg_dump` was restored into the retained
`openappplatform_staging_restore_check` database and checked for the migration
ledger and empty owner state. The private mode-0600 backup is
`/home/saleem/.local/share/openappplatform/backups/staging-20261010T125941Z.dump`.
This is a tested manual backup, not scheduled backup coverage. Before upgrades,
repeat the dump and restore check; back up to a separate host before production.

Restarting only this staging container recovered health using the same digest;
the database and pending setup state persisted. Coolify can redeploy this digest
for runtime recovery. This first release has no earlier product image for rollback;
a code rollback requires a known-good image plus migration compatibility review.
GitHub CI stays disabled. Existing production applications were not redeployed.

## Optional private host builder

The image includes `/app/oap-build-client`, an optional command adapter using
`OAP_BUILD_URL` and `OAP_BUILD_TOKEN` as explicit builder environment references.
It refuses redirects, bounds responses and requires the exact commit/repository
digest contract. Controller and operator credentials are never passed to it.

`scripts/build-host.py --policy /private/policy.json` is the optional trusted host
endpoint. The policy must be mode 0600 and contains a random token, private bind
address/port and exact `bindings` (repository, repository ID, and component name,
context and image destination). It accepts only those bindings and bounded JSON
POSTs at `/build`. It fetches public repositories by the exact requested commit,
archives the configured context and uses the same locked build-intent/receipt
journal as the SSH helper. Busy/interrupted/failed requests hold for inspection.
No automatic rebuild occurs after a durable intent.

Run this optional service on a trusted Docker build host under its existing Docker
operator account. Bind only a private network interface; do not expose its port
through the public proxy. The staging host uses the private `coolify` bridge
gateway (172.18.0.1:8790), with a dedicated token. This is a single-owner public
repository builder, not a sandbox for untrusted repository authors. Repository
builds have Docker build capabilities on that host. Private repository checkout,
autoscaling and multi-host build queues are outside this adapter. The default
platform still needs only the Go service and PostgreSQL.

Example hook command configuration:

```json
{
  "command": ["/app/oap-build-client"],
  "builderEnv": ["OAP_BUILD_URL", "OAP_BUILD_TOKEN"],
  "hooks": []
}
```

Add an explicit owner-scoped hook mapping and app-scoped operate credential to
`hooks`. Configure a separate repository hook for the OAP receiver; retain the
Coolify GitHub App's existing webhook. A dedicated staging source branch avoids
turning every main-branch documentation push into a fixture release.
