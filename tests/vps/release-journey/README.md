# v0.1 release journey on a real VPS: CLI, REST and MCP

**Purpose:** prove that an agent can manage an application end to end, reliably,
through **each** of OAP's agent interfaces. The same journey runs three times on
one server, once per interface:

| Run | Interface | Agent identity | Hostname |
|---|---|---|---|
| `cli` | `oap` JSON CLI | Journey agent (CLI) | `cli.$BASE` |
| `rest` | REST API (`curl`) | Journey agent (REST) | `rest.$BASE` |
| `mcp` | stdio MCP (`oap-mcp -operate`) | Journey agent (MCP) | `mcp.$BASE` |

Each run uses its own named agent, its own application and its own hostname.
Afterwards, cross-interface checks confirm the three apps coexist behind the
shared proxy and that no agent can touch another's app. This covers roadmap v0.1
closeout tasks 2 and 6. The results are one receipt per run plus a parity report.

**Local first.** Run this journey in the local test environment first, with
`--ingress-tls internal`, `.test` hostnames in `/etc/hosts` and `--cafile` on the
probe. Run it on a VPS once per release candidate, for what only real
infrastructure proves. See the [QA strategy](../../../docs/qa-strategy.md).

**Who runs it:** a testing agent on a disposable VPS. Owner steps (marked
**Owner**) use the owner credential and must never be mixed into agent steps. A
human can do them, or the testing agent acting as owner in those steps only.

## Ground rules

1. **One interface per run.** Every agent step in a run uses only that run's
   interface. `docker`, `curl` against the app's public URL, and `probe.py` are
   used only to build images and observe application traffic. Owner steps always
   use the CLI.
2. **Identities stay separate.** Never pass credentials in arguments, prompts or
   logs. Credential files are mode 0600 inside a 0700 directory.
3. **No workarounds.** Never edit the control database, call undocumented
   endpoints, hand-edit Caddy, or restart containers to make a step pass. A
   missing capability in one interface is a **finding**, not a reason to switch
   interfaces mid-run.
4. **Stop on surprises.** If a result differs from **Expect**, stop that run,
   capture evidence (operation, incident, instances), and record a finding. After
   an uncertain result, re-execute the **same** plan or reuse the **same**
   idempotency key; never create a new plan to "try again".
5. **Retain everything** in `$OUT/<run>/`: request bodies, responses, plan IDs and
   hashes, operation IDs and probe summaries, numbered in order.

## What you need

**Pending test revision:** the owner requires this journey to run on a **1 GB
VPS**. The procedure below still assumes the original 2 GB host and on-host
builds; it has not proven the 1 GB target. Constrain fixture overhead and add a
reproducible off-host build lane before repeating it on a 1 GB server. See
[VPS journey feedback](../../../docs/vps-journey-feedback.md) for observations
and acceptance requirements.

- A fresh Linux VPS (2 vCPU / 2 GB RAM or more) with Docker Engine (API ≥ 1.45),
  Compose v2, Python ≥ 3.8, Go (to build the CLI and MCP server), Git and `curl`.
  Public TCP 80/443 open.
- Three DNS A records pointing at the VPS: `cli.$BASE`, `rest.$BASE`, `mcp.$BASE`.
  Without DNS, use `--ingress-tls internal`, `.test` names in `/etc/hosts` and
  `--cafile` on the probe; record that public ACME was **not** tested.
- The OAP repository at the **release candidate commit**. Record `git rev-parse HEAD`.

## Phase A: build and install (once, host administration)

```sh
export REPO=$PWD J=$PWD/tests/vps/release-journey
export DIR=/opt/oap-journey OUT=/opt/oap-journey-evidence
export BASE=journey.example.com URL=http://127.0.0.1:8878
mkdir -p "$OUT" && install -d -m 700 "$DIR/creds"

docker build --label org.opencontainers.image.revision="$(git rev-parse HEAD)" -t openappplatform:rc .
go build -o bin/oap ./cmd/oap && go build -o bin/oap-mcp ./cmd/oap-mcp
OAP_IMAGE=$(docker image inspect --format '{{.Id}}' openappplatform:rc)

docker build -t oap-journey-api:base  examples/hello-api
docker build -t oap-journey-notes:v1  examples/persistent-web
docker build -t oap-journey-worker:v1 examples/worker
docker build -t oap-journey-job:v1 -f $J/job.Dockerfile $J
for v in web-v1 v1 v2; do
  docker build -t oap-journey-api:$v -f $J/api-variant.Dockerfile \
    --build-arg BASE=oap-journey-api:base --build-arg VERSION=$v $J
done
docker build -t oap-journey-api:broken -f $J/api-broken.Dockerfile --build-arg BASE=oap-journey-api:base $J
id() { docker image inspect --format '{{.Id}}' "$1"; }
export JOB=$(id oap-journey-job:v1) WEB=$(id oap-journey-api:web-v1) API1=$(id oap-journey-api:v1) \
       API2=$(id oap-journey-api:v2) APIBAD=$(id oap-journey-api:broken) \
       NOTES=$(id oap-journey-notes:v1) WORKER=$(id oap-journey-worker:v1)

python3 scripts/install.py init --json --directory "$DIR" --image "$OAP_IMAGE" \
  --environment production --ingress-tls letsencrypt --ingress-bind 0.0.0.0 \
  --ingress-domain "cli.$BASE" --ingress-domain "rest.$BASE" --ingress-domain "mcp.$BASE"
python3 scripts/install.py start --json --directory "$DIR"
python3 scripts/install.py check --json --directory "$DIR" | tee "$OUT/A-check.json"
```

**Expect:** `check` reports healthy OAP, PostgreSQL and Caddy. The OAP API answers
only on loopback. No certificate is requested until an app with HTTPS routes deploys.

## Phase B: owner trust (once, **Owner**)

Write `private-owner.json` with `{"email","name","password"}` (mode 0600, outside
the repository), then:

```sh
python3 scripts/install.py owner-setup --json --directory "$DIR" \
  --cli-bin ./bin/oap --out "$DIR/creds/owner.json" < private-owner.json
oapo() { ./bin/oap --url "$URL" --credential-file "$DIR/creds/owner.json" "$@"; }
for RUN in cli rest mcp; do
  printf '{"name":"Journey agent (%s)","targetId":"docker-local","environment":"production","maxApplications":1,"actions":["configure","deploy","recover","rollback"]}\n' "$RUN" > grant-$RUN.json
  oapo --out "$DIR/creds/onboard-$RUN.json" --file grant-$RUN.json grant | tee "$OUT/B-grant-$RUN.json"
done
```

**Expect:** three agent credential files. The owner credential is never given to
an agent step.

## Interface reference

Request bodies are **identical across interfaces**: the CLI's `--file` body, the
REST request body and the MCP tool arguments carry the same JSON. Responses carry
the same data (CLI `.data`, REST response body, MCP `structuredContent.data`):
plan ID at `id`, plan hash at `planHash`, execution result at `result` with
`applicationId`, `definitionVersion` and `operationId`. An operation ID is also the
release ID used by rollback.

| Action | CLI (`oap … <command>`) | REST | MCP tool |
|---|---|---|---|
| Validate spec | `--file B validate-spec` | `POST /api/v1/specifications/validate` | `validate_specification` |
| Diff spec | `--app A --file B diff-spec` | `POST /api/v1/applications/A/specification/diff` | `diff_specification` |
| Export spec | `--app A export-spec` | `GET /api/v1/applications/A/specification` | `export_specification` |
| Capabilities | `--app A capabilities` | `GET /api/v1/applications/A/capabilities` | `inspect_capabilities` |
| Create a plan | `--file B action-plan` | `POST /api/v1/action-plans` | `create_action_plan` with `{"request": B}` |
| Read a plan | `--plan P get-action-plan` | `GET /api/v1/action-plans/P` | `get_action_plan` |
| Execute a plan | `--plan P --file empty.json execute-action` | `POST /api/v1/action-plans/P/execute` with `{}` | `execute_action_plan` |
| Wait | `--operation O --timeout 10m wait` | Poll `GET /api/v1/deployments/O` until `state` is terminal | `wait_operation` (`timeoutSeconds` ≤ 1800) |
| Instance health | `--app A verify` | `GET /api/v1/applications/A/instances` | `inspect_instances` |
| Incident | `--app A --component C --ordinal N incident` | `GET /api/v1/applications/A/incident?component=C&ordinal=N` | `get_incident_context` |
| Rollback plan | `--app A --file B rollback-plan` | `GET /api/v1/applications/A/rollback-plan?releaseId=R&expectedVersion=V` | `plan_rollback` |
| Rollback | `--app A --key K --file B rollback` | `POST /api/v1/applications/A/rollbacks` with `Idempotency-Key: K` | `execute_rollback` (with `idempotencyKey`) |
| Approve a plan (**Owner**) | `--plan P --file approve.json approve-action` | — (owner uses CLI) | — (owner uses CLI) |

Terminal operation states: `succeeded`, `failed`, `attention`, `cancelled`, `abandoned`.

### Running an agent step through each interface

```sh
# CLI
oapx() { ./bin/oap --url "$URL" --credential-file "$CRED" "$@"; }
oapx --file body.json action-plan

# REST: build the auth header file once per credential (never put the token in arguments)
python3 -c 'import json,sys;print("Authorization: Bearer "+json.load(open(sys.argv[1]))["token"])' "$CRED" > "$DIR/creds/$RUN.hdr"
chmod 600 "$DIR/creds/$RUN.hdr"
rest() { curl -sS -H @"$DIR/creds/$RUN.hdr" -H 'Content-Type: application/json' "$@"; }
rest -X POST "$URL/api/v1/action-plans" --data @body.json

# MCP: preferably configure oap-mcp as an MCP server in your own agent client:
#   command: ./bin/oap-mcp   args: -url $URL -operate -credential-file $CRED
# or use the helper, which performs the same stdio handshake:
mcpx() { python3 -I $J/mcp_call.py --bin ./bin/oap-mcp --url "$URL" --credential-file "$CRED" "$@"; }
mcpx list                                             # expect 19 tools incl. lifecycle tools
echo '{"request": '"$(cat body.json)"'}' | mcpx call create_action_plan --args -
```

Notes per interface:
- **REST:** HTTP `202` means admitted, not succeeded. Record status codes. Waiting
  is client-side polling (every 1–2 s). If a bearer-authenticated mutation is
  refused for origin or CSRF reasons, record a finding.
- **MCP:** there is no `verify` tool; use `inspect_instances`. Owner approval is
  deliberately not an MCP tool. Note any other step the tools can't express as a
  finding with the closest available tool.

## The journey (repeat for `RUN=cli`, then `rest`, then `mcp`)

```sh
export RUN=cli  DOMAIN=$RUN.$BASE  CRED=$DIR/creds/onboard-$RUN.json
mkdir -p "$OUT/$RUN"
echo '{}' > empty.json
```

Each step names the action from the reference table and the JSON body to send.
`V` is always the current definition version (from the last `configure` or
`create` result, or from reading the application).

### C: create and first deploy

**C1. Fill and validate the spec.**

```sh
sed -e "s|__JOB_IMAGE__|$JOB|" -e "s|__API_V1_IMAGE__|$API1|" -e "s|__WEB_IMAGE__|$WEB|" \
    -e "s|__NOTES_IMAGE__|$NOTES|" -e "s|__WORKER_IMAGE__|$WORKER|" -e "s|__DOMAIN__|$DOMAIN|" \
    -e "s|\"release-journey\"|\"release-journey-$RUN\"|" $J/application.template.json > spec-$RUN.json
```

*Validate spec* with `{"specification": <spec>}`. **Expect:** valid.

**C2. Create (save only).** *Create a plan* with `{"action":"create","specification":<spec>}`,
then *execute* it. Record `APP` and `V` from the result.
**Expect:** no containers started (*instance health* shows none running).

**C3. Owner: app-scoped credential.** Scale-down is granted per application, so:

```sh
printf '{"name":"Journey app agent (%s)","scope":"operate","applicationId":"%s","actions":["configure","deploy","scale-down","recover","rollback"]}\n' "$RUN" "$APP" > app-grant-$RUN.json
oapo --out "$DIR/creds/app-$RUN.json" --file app-grant-$RUN.json app-grant
export CRED=$DIR/creds/app-$RUN.json      # REST: rebuild the header file for this credential
```

**C4. First deploy.** *Create a plan* with `{"action":"deploy","applicationId":APP,"expectedVersion":V}`,
*execute*, *wait*, *instance health*. Then *execute* the same plan again.

**Expect:**
- The operation succeeds, and its first step is `migrate` with `exitCode: 0`.
- All instances are healthy.
- Re-executing returns the **same** `operationId`; no second release is created.
- `https://$DOMAIN/` answers from `web`, `/api` returns version `v1`, `/notes`
  returns `{"boots":1}`, with a publicly trusted certificate.

```sh
curl -sS https://$DOMAIN/api   | tee "$OUT/$RUN/C4-api.json"
curl -sS https://$DOMAIN/notes | tee "$OUT/$RUN/C4-notes.json"
```

### D: scale up under traffic

**D1.** In the spec, change only `api.instances` from 1 to 3. *Create a plan*
`{"action":"configure","applicationId":APP,"expectedVersion":V,"specification":<spec>}`
and *execute*; then *create a plan* `deploy` at the new `V`, *execute*, *wait*.
Run the probe for the whole step:

```sh
python3 -I $J/probe.py --url https://$DOMAIN/api --duration 240 --out "$OUT/$RUN/D1-probe.json" &
# … configure, deploy, wait …
wait
python3 -I $J/probe.py --url https://$DOMAIN/api --duration 20 --expect-instances 3 --out "$OUT/$RUN/D1-after.json"
```

**Expect:** success. The migration runs again (new release, new job). `D1-after`
shows 3 distinct instances, all `v1`. Record D1's operation ID as `D1_RELEASE`.
Rollout failures are an observation (standard deploys may interrupt traffic).

### E: injected failures, diagnosis, correction

**E1. Failing migration.** Set `migrate.env.JOB_EXIT_CODE` to `"7"`, then configure
and deploy. **Expect:** the operation ends `failed`, `migrate` has `exitCode: 7`,
no `api` step was dispatched, and `/api` still serves `v1` from 3 instances.
*Incident* for component `migrate`: **Expect** `state: issue`, the job failure
identified, and `applicationBehaviorVerified: false`. Then restore `JOB_EXIT_CODE`
to `"0"` and configure (save only).

**E2. Broken API release.** With the probe running, *create a plan*
`{"action":"deploy","applicationId":APP,"expectedVersion":V,"components":["api"],"images":{"api":"<APIBAD>"}}`,
*execute*, *wait*. Then *incident* for `api`, ordinal 1.
**Expect:** readiness never passes and the operation does not succeed. The
incident reports `issue` for `api`, with the image change in its comparison.
Record `/api` availability as an observation. If the operation lands in
`attention` instead of `failed`, stop and capture it as a finding.

**E3. Correct.** The same deploy request with `"api":"<API2>"`, then *execute*,
*wait*, *instance health*. **Expect:** success, and the probe with
`--expect-instances 3` shows `v2` on all 3.

### F: app-wide rollback to D1

*Rollback plan* with `releaseId = D1_RELEASE`, `expectedVersion = V`. Then
*rollback* with `{"releaseId":D1_RELEASE,"expectedVersion":V,"planHash":<hash>,"acknowledgeDataCompatibility":true}`
and idempotency key `journey-$RUN-rollback-1`; *wait*; send the identical
rollback again; *instance health*.

**Expect:**
- The plan lists every owned web/worker instance and **skips `migrate`**.
- Success, and the repeated request returns the same operation.
- `/api` serves `v1` from 3 instances.
- No `migrate` step in the rollback operation.
- `/notes` `boots` is greater than in C4 (the volume survived).

### H: scale down under traffic

Change only `api.instances` back to 1. *Create a plan*
`{"action":"scale-down","applicationId":APP,"expectedVersion":V,"specification":<spec>}`,
*execute*, *wait*, with the probe running for 120 s.
**Expect:** success after the 31-second drain grace, **0 probe failures**, and
one `api` instance remains.

### Run receipt: `$OUT/$RUN/release-journey-$RUN.json`

```json
{
  "schemaVersion": 1, "run": "cli | rest | mcp", "status": "passed | failed",
  "commit": "<sha>", "oapImage": "<id>", "tls": "letsencrypt | internal",
  "steps": {
    "validate": true, "createWithoutDeploy": true, "appGrant": true,
    "deployWithMigration": true, "planReplaySameOperation": true, "publicHttpsTrusted": true,
    "scaleUpTo3": true, "trafficOnAll3": true,
    "failingMigrationBlocksRollout": true, "incidentIdentifiesJob": true,
    "brokenReleaseNotCompleted": true, "incidentIdentifiesApi": true, "correctedToV2": true,
    "appWideRollback": true, "rollbackSkippedMigration": true, "rollbackReplay": true,
    "volumeDataRetained": true, "scaleDownTo1": true, "scaleDownFailures": 0
  },
  "observations": {"scaleUpFailures": 0, "brokenReleaseApiFailures": 0},
  "operations": ["<operation IDs in order>"],
  "findings": ["<anything that differed from Expect, or an action the interface could not express>"]
}
```

## Phase X: cross-interface checks (after all three runs)

**X1. Coexistence.** All three apps answer on their own hostname over trusted
HTTPS. A request with an unknown Host is rejected.

**X2. Isolation.** Each run's app credential is **denied** on the other two apps,
through its own interface: for example, the MCP agent calls `inspect_capabilities`
for the CLI app, and the REST agent calls `POST /api/v1/action-plans` with a
`deploy` for the MCP app. Expect a denial with a structured error, and no new
plan or operation.

**X3. Controller outage.** Stop only OAP and probe all three apps for 60 s:

```sh
docker ps --format '{{.ID}} {{.Names}}' > "$OUT/X3-before.txt"
(cd "$DIR" && docker compose stop platform)
for RUN in cli rest mcp; do
  python3 -I $J/probe.py --url https://$RUN.$BASE/api --duration 60 --out "$OUT/X3-$RUN.json" &
done; wait
(cd "$DIR" && docker compose up -d --wait platform)
python3 scripts/install.py check --json --directory "$DIR"
docker ps --format '{{.ID}} {{.Names}}' > "$OUT/X3-after.txt"
```

**Expect:** zero probe failures, workload and Caddy container IDs unchanged, and
each interface works again after restart (one read call per interface).

**X4. Pause and revoke (Owner).** Agent control is workspace-wide. Read it with
`oapo agent-control`, then pause all agents:

```sh
printf '{"enabled":false,"expectedVersion":%s,"reason":"journey pause check"}\n' "$CONTROL_VERSION" > pause.json
oapo --file pause.json set-agent-control
```

One read call per interface must now be denied. Re-enable with `"enabled":true`
at the new version, and confirm each interface works again. Then
`oapo --credential <credentialId> revoke` one run's onboarding credential: its
next call is denied, and the other runs are unaffected.

**X5. Footprint and disk.** Record `docker stats --no-stream` for OAP, PostgreSQL
and Caddy, `docker system df`, and the count of retained `migrate` containers.

## Parity report: `$OUT/parity.json`

```json
{
  "schemaVersion": 1, "commit": "<sha>",
  "runs": {"cli": "passed", "rest": "passed", "mcp": "passed"},
  "coexistence": true, "isolation": true, "controllerOutageFailures": 0, "pauseRevoke": true,
  "gaps": [
    {"interface": "mcp", "step": "verify", "used": "inspect_instances", "note": "no verify tool"}
  ],
  "differences": ["<any step whose result differed between interfaces>"],
  "footprint": "...", "disk": "..."
}
```

Hand back the three run receipts, `parity.json`, the `$OUT` directory and the
findings. The journey passes only if all three runs pass and the cross-interface
checks pass. Downtime during standard deploys (D1, E2) is an observation, not a
failure. Any action an interface cannot express is listed under `gaps`; whether
it blocks v0.1 is the owner's decision.
