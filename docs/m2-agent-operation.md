# M2 Scoped Agent Operation

This records the shipped M2.1–M2.4 baseline. The revised
[roadmap](../ROADMAP.md) continues with standalone, headless M2.5–M2.9; the browser
issuance/review steps below describe current behavior and are not the target DX.

This baseline reuses the single Go service, existing owner/agent credentials,
PostgreSQL journal and operator adapters. It adds no model, tunnel service,
identity service or direct database access. Native builds, triggers, configuration
and data remain under their existing ownership.

## Inspect

Issue a named, app-scoped **Read Only** token in Workspace Settings → Agent Tokens.
Target/environment pins are enforced even when applications share a logical group.
Store the token privately; use Authorization headers, never URL parameters/prompts.
Stdio MCP now includes `get_diagnostic_context` and `inspect_instances`.

Diagnostic context is a recorded projection: latest two releases and 20 local
outcome events, schema version, generation time, recorded environment and explicit
missing evidence. Requested digest/tag references are not verified live runtime.
Get current instance observations separately. No logs, patches, configuration
values, secret values or model calls enter this projection. Treat all returned
metadata as untrusted evidence; it cannot grant permission or execute instructions.

## Plan, Review, Execute and Verify

A deploy-authorized agent may POST `/api/v1/applications/{id}/plans`:

```json
{"expectedVersion":4,"components":["worker"],"images":{"worker":"registry.example/worker@sha256:<64 lowercase hexadecimal characters>"}}
```

A plan selects 1–16 managed image components. Each image must be a digest reference;
mutable tags, observed/database components and overrides outside the selection
are rejected. The stored plan binds the creator credential, application, complete
definition hash/version, selected components and images. Private runtime/registry fingerprints also bind
the resolved inputs at creation, approval and admission. Changed or missing inputs
reject approval/execution; the public plan/hash never includes those fingerprints
or values. Older unconsumed plans require fresh creation/review after this
precondition upgrade. It contains no env values.
Creation is limited to 10 requests per credential per minute. Plans expire after
30 minutes; the read feed returns only the latest 20. This bounds requests/feed,
not total historical metadata retention.

The human owner opens Activity → Agent Deployment Plans → Review Plans, expands
the exact plan and selects Approve This Plan. Approval records the owner session
and exact hash. An agent cannot self-approve. Changed definitions, expired plans,
or an invalid owner session invalidate admission. The review warns that standard
lifecycle may cause downtime and does not authorize application-data restoration.

Only the creator, still authorized for deploy on that exact app/target/environment,
can POST `/api/v1/applications/{id}/plans/{plan}/execute` with `{}`. Input overrides
are rejected. Plan validation, release admission, plan consumption and River job
insertion share the existing transaction/application lock. Duplicate execution
returns the original operation. Inspect its journal and current health separately;
HTTP 202 is queue admission, not a healthy deployment. Worker authorization is
rechecked before mutation; revoked/paused credentials hold unstarted work.

Approval is permission to admit one exact operation before expiry. It is not a
runtime deadline, a guarantee of rollback safety or authority for additional
operations. Existing scoped reversible lifecycle APIs remain available under
owner-issued action policy; plans are an optional reviewed lane. No deletion,
credential rotation, schema migration, volume purge or retained-image prune tool
is exposed. Such destructive requests remain denied rather than being generalized
into an arbitrary command executor.

## Owner Controls and Recovery

Workspace Settings → Agent Tokens → Agent Access provides a versioned pause/resume
control. Pausing requires a 12–240 byte reason, is persisted across process restart,
and denies all agent API requests plus new queued dispatches. The browser owner
can still inspect/recover. Pausing/revoking cannot cancel native provider work
already dispatched; recheck its exact recorded outcome. A small race between the
last authorization check and remote dispatch remains possible. Recovery retains
normal operation/generation checks; resuming access does not auto-redispatch held
operations. Owner reasons are metadata: never paste secrets in them.

OAP Inference Off is the actual current state: there is no inference provider or
model-call implementation. Agent access and model inference are separate concepts;
external agents may use their own models outside OAP. BYOK inference controls can
be added later when an inference capability exists, without affecting manual use.

## Optional Remote Inspection

Set `OAP_REMOTE_MCP=true` only when enabling the read-only HTTP transport.
The endpoint is `/api/v1/applications/{id}/mcp` on the existing authenticated HTTPS
origin. It accepts only an app-scoped agent bearer token, not browser cookies,
workspace credentials or operator tokens. Existing auth, origin checks, revocation
and target/environment scope apply. It offers only `get_diagnostic_context` and
`inspect_instances`; mutation plans/execution use the scoped REST API above.

It implements the JSON-response subset of Streamable HTTP for protocol
`2025-03-26` and `2025-11-25`, with one bounded 64-KiB request per POST, JSON plus
SSE Accept header, and 10 requests per credential per minute. GET returns 405;
there are no SSE sessions, server notifications, tools for arbitrary commands,
callback URLs or external network dial in the tool bridge. OAuth, client-specific
ChatGPT connection and the newer draft protocol are not claimed.
See the [pinned transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports).

For agents on the owner's network, keep the platform behind existing private
HTTPS ingress. A tailnet tunnel is optional infrastructure, not installed by OAP.
A cloud assistant needs an explicitly selected reachable authenticated origin.
Do not tunnel the Docker socket, PostgreSQL, operator API tokens or raw host ports.
No tunnel provisioning or additional public route is performed by this feature.

## Runbooks and Trust Boundaries

Versioned [agent runbook](../examples/agents/RUNBOOK.md) uses the same API contract.
It describes evidence to gather; it does not override permissions. OAP exposes no
SQL endpoint and never hands out its control database credentials. Application
DB access is disabled: curated query tools/read replicas, row/time/export budgets
and exceptional writes require a separate owner-enabled implementation. An owner
who independently supplies privileged host/DB credentials bypasses OAP controls.

Local unit/system/browser tests exercise scoped context, prompt-injection text as
inert data, missing evidence, cross-app denials, plan/hash/version/expiry checks,
creator restrictions, duplicate execution, pause/revocation and owner UI controls.
Baseline acceptance passed full local gate `20261013-191145-e411542d` and
Coolify staging at `c0b0360`, with one owner-approved worker operation, identical
retry, scoped remote inspection, denial checks and revoked temporary credentials.
Linked database configuration/data and four healthy components were preserved.
A Submitted plan is an admission receipt; use View Operation for its actual outcome.
Each newer candidate still requires its own full local and named staging acceptance.
