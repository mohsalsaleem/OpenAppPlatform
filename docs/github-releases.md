# GitHub release intake and operator integration

Start the controller in owner mode with `-github-hooks /path/to/config.json`.
Mappings bind a repository ID/name and branch to one OAP application and selected
components. Secrets are environment references, never values in configuration.
The receiver at `/api/v1/hooks/github/{id}` verifies HMAC SHA-256 over the exact
body, bounds payload size, and deduplicates delivery IDs with payload hashes.
Repository/branch mismatches and deletions are ignored. Signed ping is supported.
No GitHub Actions workflow is required or enabled.

## Existing Coolify GitHub App: preferred native lane

Use `examples/github-sources/coolify-app.json` with an already grouped observe-only
source application. Coolify keeps its existing GitHub App and auto-deployment
ownership. OAP receives a separately configured signed repository notification and
follows the matching provider deployment, without building, deploying, changing
source configuration, or transferring management. Its credential is a named,
app-scoped **read** token. Do not replace the existing GitHub App webhook URL as
part of this setup; the additional repository hook reports events to OAP.

The adapter requires the configured repository/branch, a GitHub App-backed source,
an exact commit in resource-scoped deployment history, and an unambiguous recent
match. The provider can queue slightly before OAP receives the notification;
matching allows a two-minute window. An old/superseded deployment does not prove
the pushed commit is live. Provider completion and current health remain separate
fields. Missing/ambiguous/failed observations move to owner-reviewed attention.
This lane reports existing operator activity; it does not claim OAP owns native
release sequencing or reuses native build artifacts across replicas.

Read-only preflight on 10 October 2026 confirmed Coolify 4.4.6, the configured
`saleem-personal` GitHub App (local source ID 2), and access to repository
`mohsalsaleem/OpenAppPlatform` (ID 1411193511). The official current
[GitHub App handler](https://github.com/coollabsio/coolify/blob/main/app/Http/Controllers/Webhook/Github.php)
queues the webhook `after` commit. A separate isolated native source build on Coolify 4.4.6 subsequently passed at
commit add41068b125a136d0e544b5c1533e411a99209d, with matching provider history
and healthy runtime observed. Existing operator triggers are unchanged.
See [GitHub App setup](https://coolify.io/docs/applications/sources/github/app).

## Optional explicit immutable-image builder

`examples/github-sources/image-builder.json` demonstrates the fallback build
contract for OAP-managed image components. A trusted, owner-configured absolute
executable receives one JSON request on stdin:

```json
{"buildId":"stable-build-id","repository":"owner/repo","repositoryId":123,"commit":"40-character-sha","component":{"name":"web","context":".","imageRepository":"ghcr.io/owner/web"}}
```

It must check out that exact commit, build once, publish the artifact and return
only `{"commit":"same-sha","image":"ghcr.io/owner/web@sha256:..."}` on stdout.
The builder must be an installed trusted helper, never a script supplied by the
repository. OAP invokes it without a shell. Output is bounded, the process times
out, and only PATH and explicit `builderEnv` references are inherited. Database,
Coolify, setup and shared API tokens cannot be whitelisted. Repository code/builds
must be isolated by the configured helper; OAP does not install or silently run a
host build toolchain. The helper must journal/idempotently reconcile the stable
build ID. This is an executable adapter contract, not a bundled image builder.

The pipeline records the builder's exact-commit/digest attestation; it does not
independently prove source-to-image content. Only configured trusted builders can
supply that attestation. Each returned image must match the configured repository
and an immutable SHA-256 digest. A successful component build is persisted and
reused for all its replicas; unrelated components are excluded from release steps.
The frozen application version is checked before enqueueing. Source provenance
is stored on the release; a lost release acknowledgement retries its idempotency
key without rebuilding or creating a second release.

Build intent is durable before process invocation. Interrupted/invalid/failed
builder results move to attention and never automatically rebuild. Owner recovery
requires the event timestamp/current definition version; uncertain builds require
explicit `retryBuild` after inspection. Built digests are retained. Branch cursors check push `before` against the last accepted commit. Out-of-order
pushes hold for review, newer accepted events supersede older in-flight builds, and
enqueue locks the cursor atomically. Owner recovery needs `approveSourceCommit`
to promote a superseded commit. A linked immutable release is restored before
rebasing, so a lost acknowledgement cannot create a second release. Named agent
expiry/revocation is checked before work and by the release worker. OAP's audit
records the initiating subject and credential. The Activity screen shows source
requests, commits, build results and reviewed recovery.

## Verification and remaining gate

Local tests cover HMAC rejection, changed duplicates, repository mismatch,
selected-component releases, one successful build reused for two replicas,
uncertain-build holds/recovery, revoked credentials, native read-only observation,
and source/repository scope. Existing Docker/owner/UI/MCP suites stay required.

M0 batch 2 remains active. Before a native management handoff or changing triggers,
retain the isolated native source evidence and verify signed push sequencing
under the proposed ownership contract. Exact-commit source build/runtime observation
is verified, while the live test uses an explicit staging deployment rather than
changing the GitHub App webhook or existing triggers. Before advertising immutable-image
build deployment, configure and exercise a real trusted builder/registry locally
and in staging. The signed server-build staging test below now supplies that evidence. Git credentials and registry credentials are separate. No native
source management or private-registry provisioning is claimed by this slice.

### Native staging fixture

An isolated GitHub App source fixture is retained as
`r9uzce0z9mnunfftgocit3sj`, with auto-deploy disabled. The first inline-Dockerfile
probe proved that Coolify uses an empty build context for that mode, so it cannot
verify source checkout. Native source verification therefore uses the committed
`examples/github-source/Dockerfile` with the repository root as context. The real
source test is separately opt-in and must succeed before claiming the native gate.

Native source-context verification passed on 10 October 2026 with fixture
`lf1ggsrabuf3qifyj305kreo`, provider deployment `69vq60zbyqtvtf6krvhnxryw`, and
commit `add41068b125a136d0e544b5c1533e411a99209d`. The existing `saleem-personal`
GitHub App cloned the repository, built its committed Dockerfile, and reached
healthy runtime. OAP registered an observe-only application and matched the exact
provider operation. No management handoff, trigger changes, or registry credentials
were required. The failed inline fixture is retained separately for diagnostics.

### Opt-in signed server-build verification

`TestLiveSignedServerBuildCoolifyReplicas` uses an isolated local PostgreSQL schema,
a local owner-mode controller, the trusted SSH builder and the existing Coolify
staging target. It retains two remote fixture resources and writes a non-secret
receipt to `.local/signed-build-live.json`. The committed fixture context is
`examples/server-build`; no real application database is needed by the fixture.

Run from the repository with the configured local secrets and PostgreSQL:

```sh
OAP_LIVE_SERVER_BUILD=1 OAP_TEST_SOURCE_COMMIT="$(git rev-parse HEAD)" \
  python3 scripts/run-local.py go test ./tests/system \
  -run '^TestLiveSignedServerBuildCoolifyReplicas$' -count=1 -v
```

This explicitly authorizes staging fixture creation/build/deployment for the test.
The source tree must be committed before execution because the builder archives
only the exact Git object. The test checks HMAC rejection, duplicate delivery,
commit provenance, saved build receipt reuse, two replicas sharing one registry
digest, healthy runtime and completed redelivery. It sends a signed test payload;
it does not register a GitHub repository hook or change existing App triggers.
Do not run it against production or the hosted controller's live database.

### Signed immutable-image staging evidence

On 10 October 2026, `TestLiveSignedServerBuildCoolifyReplicas` passed in 35.74
seconds using commit `e610d2f91139dfe8d85087b78b67d5be43d81d61`. Signed intake
created event `4517dd3f5fdee32c678a6d3799ff1ec3` and release
`62dd6adbf5390d8acea621662f9b4587`. The server build ID was
`e40fdb28ef755ab8395b01dc8f1edb1d`. Its immutable artifact was
`127.0.0.1:5001/openappplatform/signed-staging@sha256:731095ddc173afad23718e449b9b8fb4d5a9f28c7b1bd751ed40e262961385e4`.
Both retained Coolify fixtures (`gt9jqss5zwln3ysrwlo30wbx`,
`7pednplu78ftyjf02zioon3e`) reported that digest and healthy runtime. Signature
rejection, duplicate event/release identity and completed builder receipt reuse
passed. The controller and isolated test schema ran locally; the server build,
registry and workloads were real. Test-schema metadata is removed after the test,
and the non-secret receipt retains its provenance.

This closes the real builder/registry staging verification gate. It does not
claim a delivery originated at GitHub: the test signs a push-shaped payload.
Enabling a live repository webhook is an explicit configuration step for an
owner-selected application/branch. Existing GitHub App webhook ownership stays
unchanged. The hosted staging controller has no installed image-builder command;
this test does not grant it SSH keys or Docker socket access.
