# Logical applications and existing environments

M0 batch 3 now adds an organization layer over the existing environment-specific
application records. Existing resources are grouped through Deployment targets;
the application list leads with Group existing services. Native operator build,
trigger, domain and storage behavior is preserved.

Migration 010 assigns each existing application its own logical application group,
using its existing ID. Similar names are never automatically merged. Existing
application/environment IDs, manifest versions, resource bindings, releases,
GitHub mappings and app-scoped credentials keep their identities. A database
trigger supports older controllers inserting environment records without the new
field, so compatible rollback does not require dropping the new schema.

The application list shows one logical application with links to its environments.
Staging is the default entry when present; the owner can explicitly select another
environment.
Each environment retains its own target, components, configuration, release history
and lifecycle actions. Environment navigation loads the selected record; deployment
and restart continue to operate only on that record. Component copy distinguishes
existing operator workflow from OAP-coordinated image releases.

An owner can link an existing standalone environment into the current logical
application, provided its environment name is not already present. Both group
versions are checked, source/destination locks have consistent order, and the
move is atomic. No operator request, deployment or configuration rewrite occurs.
An owner can also make an environment standalone again. Neither operation grants
an app-scoped credential access to siblings; credentials remain bound to the
original environment ID. Membership edits require a human owner session and are
audited. Viewers/operators can read their existing workspace organization but
cannot edit it. App-scoped agents cannot enumerate global groups.

API:

- `GET /api/v1/application-groups` returns IDs, names, organization versions and
  existing environment records. The existing flat `/applications` API remains.
- `POST /api/v1/application-groups/{id}/environments` accepts `applicationId`,
  `expectedGroupVersion` and `expectedSourceVersion`.
- `POST /api/v1/application-groups/{id}/environments/{environment}/unlink` accepts
  `expectedVersion`; `environment` is the existing environment-record ID.
- Stale versions or duplicate environment names return 409. Refresh before retrying.

Organization versions are separate from runtime definition versions. Linking does
not invalidate a queued release, change a source cursor or duplicate a workload.
Inactive group metadata is retained; no resource or history is deleted.

System coverage includes migration from the prior nine schemas, preserved IDs and
bindings, no automatic merge, old-style inserts after migration, no provider calls,
existing release identity, stale/duplicate links, unlink, concurrent version winners
and denied scoped-agent/viewer edits. Owner browser coverage links staging and
production definitions on isolated local Docker targets, switches environment URLs
and checks the combined list. The production-named local fixture is metadata in the
isolated test environment, not a production deployment.

Batch 3 remains active. Safe guided target connection, reusable definitions/readable
diffs and more detailed native capability/authority presentation remain follow-up
items. Target setup still uses server-side configuration files and secret references.
No production environment is provisioned by the new UI or by this migration.
