# Image-defined PostgreSQL migration job

Build this directory's image with your existing build tools, publish an immutable
image digest, and use it for a `kind: "job"` component named `migrate`. The sample
entrypoint creates one example table transactionally and can be rerun safely.
It changes your application database; use a disposable database when trying it.
OAP never connects to the application database itself.

Use the existing owner-controlled secret-store workflow to bind `database-url` to
your application's PostgreSQL URL. Keep credentials out of the specification.
The database must be reachable from the app network; this example neither creates
nor adopts a database. The PostgreSQL image runs only the supplied migration
entrypoint, not a database server.

```json
{
  "name": "migrate",
  "kind": "job",
  "image": "registry.example/migrations@sha256:REPLACE_WITH_BUILT_DIGEST",
  "instances": 1,
  "secretRefs": { "DATABASE_URL": "database-url" },
  "readiness": { "timeoutSeconds": 120 }
}
```

Add `"dependsOn": ["migrate"]` to your API component. Replace the image placeholder
before validating. Save through a configure plan, then review a deployment plan;
selecting the API includes the job automatically. Execute and wait. Use operation
exit evidence and `incident --component migrate` to diagnose failure. A failed
migration blocks remaining release steps and preserves already-running containers.
A new release runs a new migration process. Image rollback skips this job and
requires explicit data compatibility acknowledgement.

The reproducible test suite builds an offline job fixture from the pinned local
images and exercises the complete CLI workflow; it needs no registry credentials
or external application database.
