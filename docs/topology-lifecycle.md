# Standalone component topology — M3.2 first slice

M3.1 routing is functionally complete. This slice makes a standalone app's owned
web/worker components grow and retire through the existing specification and
agent interfaces. It does not add UI flows or return to operator integration.

## Add a component

Edit the complete versioned application specification in the repository. Add an
owned `web` or `worker` with its image, instances and optional connections/storage.
Validate/diff it, then create and execute a `configure` action plan with the current
`expectedVersion`. Saving changes the future definition only. It creates no
containers and does not restart existing components.

Create a `deploy` plan at the saved version. Select the new components if only they
need deployment, or release the app normally. Immutable digests or cached Docker
content IDs are required by deployment planning. Existing components retain their
identities when their runtime configuration is unchanged. Existing linked databases
and observed resources stay externally owned; adding/adopting those through this
path is outside this slice.

## Remove a component

First remove its dependency/connection references and ingress routes from the
remaining definition through `configure`. Those edits must produce a valid spec.
If they change a surviving component's runtime connections/environment, deploy
and verify that component before retirement; a saved definition does not prove
that live code has stopped using the old connection.
A save still leaves live routing unchanged; retirement publishes the saved route
membership before stopping the component. Existing ingress withdrawal and the
shipped 31-second grace are reused when the app has a publication to change.

Then remove exactly one owned web/worker from the complete desired specification
and create an action plan with:

```json
{
  "action": "remove-component",
  "applicationId": "<app-id>",
  "expectedVersion": 5,
  "specification": "<complete desired Application object>"
}
```

Replace the specification placeholder with an object, not a string. Remaining
component order and configuration must stay unchanged. The plan snapshots exact
bindings and always requires human owner approval, regardless of `requireReview`.
The named agent needs an owner-issued `remove-component` grant. It cannot approve
its own plan; a stale definition, binding or target invalidates approval/execution.

Use `oap --file remove.json action-plan`, inspect the plan and its masked diff,
then let the owner use `approve-action` with its exact `planHash`. The original
agent executes `oap --plan <id> --file empty.json execute-action` and waits for the
operation (`empty.json` contains `{}`). The same request and approval contracts
back execution-facing MCP; there is no host-shell or SQL escape hatch.

The operation stops each exact owned active instance, reserves retired bindings,
and retains stopped containers and named volumes. Only confirmed stops permit the
new desired specification to be committed. Attention work keeps its claims and
blocks conflicting edits; constrained recovery observes uncertain stops rather
than blindly repeating them. Re-executing the plan returns the same operation.
Unrelated components are read-claimed and are not deployed/restarted by removal.

Re-adding the same owned component through configure/deploy can reactivate its
retained binding and data. Existing storage safeguards still reject incompatible
mount changes or silently dropping persistence. This is not application-data
restore, image rollback or database/schema rollback. The final component cannot
be removed to create an empty application; whole-app deletion remains separate.

## Acceptance and boundary

`topology.json` records the named-agent CLI journey: web/worker addition,
save-without-deploy, exact owner review, self/stale approval denial, retirement
replay, unchanged sibling start time and retained volume data after re-add.
`ingress-lifecycle.json` also checks reviewed routed-component retirement using
the shipped routing/drain primitive. Fixtures use the normal owner grant and
public CLI/API paths; native reads are test assertions, not agent permissions.

See [topology templates](../examples/topology/README.md). The next M3.2 slice is
pre-deploy jobs, then app-wide rollback and definition history/diff/revert/export.
Installed proxy authority/port handoffs and extended drain testing stay deferred
hardening and do not reopen M3.1.

Schema migration 025 adds the removal action/operation values. Existing metadata
and volumes are retained; the installer's schema compatibility checks still
protect updates and reject incompatible older control-plane images.
