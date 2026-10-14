# Grow and retire components in one application

`initial.json` is the initial one-service app and is also the desired definition
for removing the added worker. `with-worker.json` adds a worker to that same app.
These are templates: replace the target/environment and image placeholders with
your configured standalone target and immutable image references before planning.
Build the [worker example](../worker/README.md) through your normal image lane.

1. Create and deploy `initial.json` using the existing owner-issued agent grant.
2. Configure with `with-worker.json`, then deploy the `worker` selection. Saving
   alone does not deploy it; the web service's unchanged runtime stays running.
3. Create a `remove-component` plan with `initial.json` as the complete desired
   spec and the current version. The agent grant must include `remove-component`.
4. The human owner reviews/approves the exact plan; the original agent executes
   and waits. The worker's stopped container/volumes remain retained.
5. Configure/deploy `with-worker.json` again to explicitly re-add it.

Use complete action-request bodies containing `action`, `applicationId`,
`expectedVersion` and the specification object. [Lifecycle details and CLI
commands](../../docs/topology-lifecycle.md) describe the separate save/release
steps, required approval, reference cleanup and retirement boundaries.
