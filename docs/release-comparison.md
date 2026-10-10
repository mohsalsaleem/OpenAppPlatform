# Frozen release comparison

M0 batch 4 adds a read-only comparison of two releases belonging to the same
application/environment record:

```http
GET /api/v1/applications/{id}/release-comparison?from={releaseId}&to={releaseId}
```

The response contains release IDs, definition versions, operation/state, recorded
source commit, requested selected image references with digest-reference flags, and
sorted component configuration/selection changes. It compares immutable stored
manifests, not the editable current definition. A source release's unaffected
components remain unselected; comparing never expands selection or dispatches work.

Environment, service-variable and endpoint values are hidden. Only their keys and
added/changed/removed indicators are returned. Images and dependency component names
are visible within existing application read scope. Default readiness values normalize
to avoid false diffs; dependency collection order does not imply a graph change.
Cross-application release IDs return not found. App-scoped credentials can compare
only their authorized environment; logical grouping grants no sibling access.
The route performs database reads only and needs no reachable operator or builder.

Activity offers two release selectors and a compact comparison with artifact references
and a configuration table. Mutable tags are labeled as lacking reproducibility proof.
A recorded digest reference is not proof that a native build artifact was extracted or
that it remains pullable. Release state is metadata, not a fresh runtime health check.

This is not a rollback compatibility decision or execution API. Restart and retirement
records can be inspected, but their definitions do not establish a deployable known-good
snapshot. Target authority, bindings, routes, storage/data, private image availability and
schema compatibility still need explicit verification before future rollback. Comparing
does not restore data or mutate desired state. Rollback and cancellation remain pending.

Unit/system tests verify deterministic diffs, masking of all runtime map values,
immutable-vs-mutable reference flags, same-ID rejection, scoped/cross-application
boundaries, unchanged release state and zero adapter calls. Browser tests compare two
actual isolated Docker releases with different images, ports and environment values.
