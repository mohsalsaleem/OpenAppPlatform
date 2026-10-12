# Frontend, API, Worker and Existing PostgreSQL

Build this directory's Dockerfile once. Set `MODE=frontend`, `MODE=api` or
`MODE=worker` on the corresponding OAP-owned image components. The worker has no
HTTP listener. API and worker run only `SELECT current_timestamp`; the example
never creates schemas, migrates tables or writes application data.

1. Create frontend/API/worker image components in an application.
2. Select **Link Database**, choose an existing resource in that target, and enter
   a descriptive endpoint such as `postgresql://database:5432/application`.
   Keep credentials out of this field. Review application-member health/log access.
3. Configure `DATABASE_URL` secret references for API and worker. An administrator
   grants each exact application/component/variable/reference tuple as documented
   in [runtime secret references](../../docs/runtime-secret-references.md).
   Use a dedicated role with CONNECT and the application's required read access.
   The acceptance fixture grants no writes to existing data.
4. Set API and worker startup dependencies to the database. Configure frontend's
   `UPSTREAM_URL` service connection to API. Coolify requires an explicit existing
   HTTP endpoint; Docker also supports application DNS for the managed API.
5. Deploy the image components. The database stays observe-only and outside the
   release. Open frontend and check the API database connection; inspect worker logs.

The connection endpoint is display metadata. It does not inject runtime variables,
join a database to a Docker network, open firewall ports or run a SQL connection
check in OAP. The operator/owner must supply a reachable endpoint and credentials.
Use an existing private network or an appropriately protected existing endpoint.

`make test-local` creates a PostgreSQL fixture **before** OAP links it, seeds owner
data and a restricted reader role, and runs the complete application against that
resource. It verifies credentials are reference-only, both consumers connect,
native database configuration/data are unchanged, dependency failure is visible,
and a held API release recovers. All fixture resources belong to the isolated
reproducible test environment; this is not an OAP database-provisioning feature.
