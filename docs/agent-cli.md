# Agent CLI: M2.5

The `oap` binary is a thin, non-interactive client of the existing public API.
It does not run an agent or a model. Flags precede a single command; stdout is
one JSON envelope with `schemaVersion: 1`, `ok`, `data` or `error`. No dashboard
changes are part of this slice.

Build with `go build -o bin/oap ./cmd/oap`. The platform image also includes
`/app/oap`; its default entrypoint remains the server. `oap help` returns command
contracts and `oap schema` returns the bundled versioned OpenAPI document offline.
The bundled schema describes the client release, not live server introspection.

## Identity and context

Set `OAP_URL` to an HTTPS origin or loopback HTTP origin. Set `OAP_API_TOKEN` to a
privately supplied named agent token. Tokens are never command-line flags. Existing
owner-mode agents remain app-scoped: app creation/listing and target enumeration
are not granted by this CLI. Broader headless grants arrive in M2.6.

For current owner-authorized API operations, `--session-file` or `OAP_SESSION_FILE`
accepts an existing owner session in a private regular JSON file with a `session`
field. The file must deny group/other access; symlinks are rejected. Protect and
remove it when no longer needed. The CLI never prints or persists sessions and
will not combine an owner session with an agent token. This is not headless login,
bootstrap, agent impersonation or a new credential issuance mechanism.

Use explicit `--app`, `--operation`, and optional `--target` / `--environment`
assertions. Assertions check current definition or frozen operation context before
mutation/wait; they do not select a default target or replace server authorization.
HTTP redirects are not followed and remote plaintext HTTP is rejected.

## Supported journey

```sh
bin/oap help
bin/oap schema
bin/oap --app "$APP_ID" capabilities
bin/oap --app "$APP_ID" get
bin/oap --app "$APP_ID" --file release.json --key release-001 deploy
bin/oap --operation "$OPERATION_ID" --timeout 2m wait
bin/oap --app "$APP_ID" verify
bin/oap --app "$APP_ID" diagnostics
```

`release.json` contains an `expectedVersion` greater than zero, optional selected
`components`, and optional image overrides. Follow the API schema for those fields.
Use one stable key for the intended release, retain the returned `operationId` and
`idempotencyKey`, and reuse that key after uncertain requests. Mutations are never
automatically retried. Admission returns success with the operation ID; only waiting
reports its eventual outcome. A timeout or interrupted client does not cancel it.

`create --file application.json` saves a manifest; `update --app ID --file update.json`
saves a versioned `{manifest, expectedVersion}` request. Neither deploys. Create
requires existing workspace authority; the isolated preview test exercises it
without claiming an app-scoped agent can create applications today.

`GET /api/v1/applications/{id}/capabilities` projects credential permissions, runtime
support and component ownership for deploy/restart/configure. It exposes denial
reasons and missing admission evidence. This covers only those actions; eligibility
is not authorization for a particular operation, proof of readiness or permission
to mutate observed resources. Controller preconditions remain authoritative.

`verify` checks current, non-retired instance status. It accepts running without a
native probe, or healthy reported status; absent/starting/unhealthy/unavailable
instances are inconclusive. It explicitly reports `applicationBehaviorVerified:
false`. The E2E fixture separately checks an actual HTTP response. Arbitrary URL
probes, full incident bundles and MCP mutation tools remain later M2 slices.

## Errors and bounds

API JSON errors supply stable codes, `retryable` and `nextActions`. A conflict stays
`conflict` when the API cannot distinguish its cause; the CLI does not invent a
stale-definition diagnosis. The CLI normalizes denial/recovery hints and does not
print provider error messages, credentials or raw transport failures. `retryable`
is not permission to repeat a mutation with a new key or a claim of non-dispatch.

| Exit | Meaning |
| --- | --- |
| 0 | Command succeeded; deploy means admitted, verify means current health only |
| 2 | Invalid command/input/context or credential-file configuration |
| 3 | Authentication/scope denial or absent scoped resource |
| 4 | API rejection, conflict or inconclusive verification |
| 5 | Transport/server/rate-limit/response failure |
| 6 | Operation failed, needs attention, was cancelled or abandoned |
| 7 | Wait deadline or interruption; operation can continue |

Input is one JSON document up to 64 KiB, response up to 2 MiB, each request has a
20-second timeout, and total command deadline defaults to two minutes with a
30-minute maximum. Wait polls once per second. It performs read-only observation;
no cancellation, redispatch or hidden recovery. Unknown nonterminal states remain
observable until the bounded deadline rather than being guessed successful.

## Reproducible acceptance

`make test-local` runs `tests/env/cli.py` against an isolated Docker daemon and fresh
platform, without Coolify credentials or resources. It checks create without deploy,
capabilities, versioned save, context mismatch and stale denial, identical-key
admission, durable completion, current health, actual fixture HTTP behavior, a
replacement release, and a deliberately failed missing-image release. The runner
writes `cli-acceptance.json` beside its normal suite report and removes its isolated
environment afterward. The deliberate failure uses pullPolicy never: no remote
registry or image is contacted.

Focused tests cover redirects, oversized responses, private session files,
auth/conflict/rate-limit errors, held/terminal states and deadline behavior. The
owner-mode system regression checks app-scoped capability grants and sibling denial.
M2.5 is accepted only when the full local gate passes; M2.6 then becomes the next
implementation slice. End-to-end named-agent bootstrap remains unimplemented.
