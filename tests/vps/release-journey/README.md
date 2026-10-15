# v0.1 release journey on a real VPS

**Purpose:** prove the v0.1 promise on a **1 GB VPS**: a multi-component
application operated end to end by an owner-authorized agent through the **CLI**,
plus a short smoke check that the **REST API** and **MCP** work end to end too.

| Run | Interface | Scope | Hostname |
|---|---|---|---|
| `cli` | `oap` JSON CLI | **Full journey** (C–H) | `cli.$BASE` |
| `rest` | REST API (`curl`) | **Smoke** (S1–S6) | `rest.$BASE` |
| `mcp` | stdio MCP (`oap-mcp -operate`) | **Smoke** (S1–S6) | `mcp.$BASE` |

Each run uses its own named agent, its own application and its own hostname.
Final checks confirm the apps coexist behind the shared proxy, survive an OAP
outage, and that the host stayed healthy. This covers roadmap v0.1 closeout
tasks 2 and 6. Results: one CLI receipt, two smoke receipts and a summary.

**Scope on purpose.** Full REST and MCP journeys, interface parity, the
cross-interface isolation matrix and pause/revoke checks belong to the QA track
(Q1 integrity checker, Q3 agent evaluations). Their steps are kept in
[Deferred to the QA track](#deferred-to-the-qa-track) so nothing is lost. The
local gate already covers isolation and pause/revoke through the CLI.

**Local first.** Run this journey in the local test environment first, with
`--ingress-tls internal`, `.test` hostnames in `/etc/hosts` and `--cafile` on the
probe. Run it on a VPS once per release candidate, for what only real
infrastructure proves. See the [QA strategy](../../../docs/qa-strategy.md).

**Who runs it:** a testing agent with SSH access to a disposable 1 GB VPS. A ready-to-use
prompt is in [HANDOFF.md](HANDOFF.md). Owner steps (marked
**Owner**) use the owner credential and must never be mixed into agent steps. A
human can do them, or the testing agent acting as owner in those steps only.

## Ground rules

1. **One interface per run.** Every agent step in a run uses only that run's
   interface. `docker`, `curl` against the app's public URL, `probe.py` and
   `monitor.py` are used only to prepare images and observe traffic and the host.
   Owner steps always use the CLI.
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

## Target: a 1 GB VPS

The journey must pass on a **1 GB RAM VPS without swap**; a bigger server is not
the fix ([feedback](../../../docs/vps-journey-feedback.md)). The first attempt
failed because it compiled the platform, frontend and fixtures **on** the VPS.
So nothing is compiled on the VPS: a build machine prepares a verified bundle,
and the VPS only loads images and runs OAP.

**Memory budget** (MiB; OAP, PostgreSQL and Caddy from the measured
[footprint reference](../../../docs/footprint.md); the rest are estimates the
monitor will confirm):

| Item | Expected | Notes |
|---|---|---|
| Kernel, systemd, sshd | ~150 | Varies by image |
| Docker Engine and containerd | ~100 | |
| OAP | ~25 | Measured idle peak 21 |
| PostgreSQL | ~140 | Measured idle peak 131–134 |
| Caddy | ~50 | Measured idle peak 45 |
| Fixtures at peak: 6 containers in the active run, 4 each in the two retained runs | ~100 | Small Go programs, ~5–10 each; the 64 MiB limits are caps, not usage |
| Agent tooling on the VPS (CLI, MCP server, Python helpers) | ~50 | Short-lived processes |
| **Total** | **~615** | Leaves ~300 of 985 for page cache and peaks |

**Stop rule:** if available memory falls below **100 MiB** for more than a
minute, or SSH stops answering, stop the run, record it, and collect evidence
after recovery. Do not add swap to pass.

## What you need

**Build machine** (a laptop, workstation or CI runner; not the VPS): Docker
(default `docker` builder), Go, Git, Python ≥ 3.8, and the repository at the
**release candidate commit** with a clean working tree.

**VPS:** fresh Linux, **1 vCPU / 1 GB RAM**, no swap required, ≥ 10 GB free disk,
Docker Engine (API ≥ 1.45), Compose v2, Python ≥ 3.8, Git and `curl`. **No Go or
Node.** Public TCP 80/443 open. A checkout of the same commit (for the installer
and these journey files; nothing is built from it).

**DNS:** three A records pointing at the VPS: `cli.$BASE`, `rest.$BASE`,
`mcp.$BASE`. Without DNS, use `--ingress-tls internal`, `.test` names and
`--cafile` on the probe, and record that public ACME was **not** tested.

**Where things run:** OAP's API is loopback-only, so `oap`, `oap-mcp`, `curl`
against the API and the MCP helper run **on the VPS** over SSH. `probe.py` runs
**on the build machine** (it only needs the public URL), which keeps traffic
generation off the 1 GB host and tests real external access.

## Phase A: prepare, load and install (once)

**A1. Build the bundle (build machine).** Use the VPS's platform
(`linux/amd64` or `linux/arm64`).

```sh
git checkout <RC_SHA> && git status --porcelain   # must be empty
python3 -I tests/vps/release-journey/bundle.py prepare --out ./journey-bundle --platform linux/amd64 \
  | tee prepare.json
scp -r ./journey-bundle vps:/opt/oap-journey-bundle
```

**Expect:** `ok: true`, `dirtyTree: false`. `manifest.json` records the commit,
platform, every image ID and every file checksum.

**A2. Preflight and monitor (VPS).**

```sh
export REPO=$PWD J=$PWD/tests/vps/release-journey BUNDLE=/opt/oap-journey-bundle BIN=/opt/oap-journey-bundle/bin
export DIR=/opt/oap-journey OUT=/opt/oap-journey-evidence
export BASE=journey.example.com URL=http://127.0.0.1:8878
mkdir -p "$OUT"
# Leave DIR absent or empty: install.py init refuses existing contents.
git rev-parse HEAD | tee "$OUT/A-checkout.txt"    # must equal the bundle commit
{ uname -a; nproc; free -m; swapon --show; df -h /var/lib/docker /opt; docker version --format '{{.Server.Version}}'; } \
  > "$OUT/A-preflight.txt" 2>&1
nohup python3 -I $J/monitor.py run --out "$OUT/monitor.jsonl" --duration 21600 > /dev/null 2>&1 &
echo $! > "$OUT/monitor.pid"
```

**Expect:** total memory ≤ 1 GiB is fine; record it. The monitor keeps running
until the final checks end.

**A3. Load the bundle (VPS).**

```sh
python3 -I $J/bundle.py load --bundle "$BUNDLE" | tee "$OUT/A-load.json"
. "$BUNDLE/journey.env"     # exports OAP_IMAGE, JOB, WEB, API1, API2, APIBAD, NOTES, NOTES2, WORKER
```

**Expect:** `ok: true`. Every checksum and image ID matches the manifest.

**A4. Install (VPS, host administration).**

```sh
python3 scripts/install.py init --json --directory "$DIR" --image "$OAP_IMAGE" \
  --environment production --ingress-tls letsencrypt --ingress-bind 0.0.0.0 \
  --ingress-domain "cli.$BASE" --ingress-domain "rest.$BASE" --ingress-domain "mcp.$BASE"
python3 scripts/install.py start --json --directory "$DIR"
python3 scripts/install.py check --json --directory "$DIR" | tee "$OUT/A-check.json"
install -d -m 700 "$DIR/creds"
```

**Expect:** `check` reports healthy OAP, PostgreSQL and Caddy. The OAP API answers
only on loopback. No certificate is requested until an app with HTTPS routes deploys.
Create the private credential directory only after initialization succeeds.

**After every phase,** copy evidence off the VPS from the build machine, so a lost
host loses nothing: `rsync -a vps:/opt/oap-journey-evidence/ ./evidence/`. Record
each phase's start and end time in `$OUT/timings.txt`.

## Phase B: owner trust (once, **Owner**)

Write `private-owner.json` with `{"email","name","password"}` (mode 0600, outside
the repository), then:

```sh
python3 scripts/install.py owner-setup --json --directory "$DIR" \
  --cli-bin "$BIN/oap" --out "$DIR/creds/owner.json" < private-owner.json
oapo() { "$BIN/oap" --url "$URL" --credential-file "$DIR/creds/owner.json" "$@"; }
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
oapx() { "$BIN/oap" --url "$URL" --credential-file "$CRED" "$@"; }
oapx --file body.json action-plan

# REST: build the auth header file once per credential (never put the token in arguments)
python3 -c 'import json,sys;print("Authorization: Bearer "+json.load(open(sys.argv[1]))["token"])' "$CRED" > "$DIR/creds/$RUN.hdr"
chmod 600 "$DIR/creds/$RUN.hdr"
rest() { curl -sS -H @"$DIR/creds/$RUN.hdr" -H 'Content-Type: application/json' "$@"; }
rest -X POST "$URL/api/v1/action-plans" --data @body.json

# MCP: preferably configure oap-mcp as an MCP server in your own agent client:
#   command: $BIN/oap-mcp   args: -url $URL -operate -credential-file $CRED
# or use the helper, which performs the same stdio handshake:
mcpx() { python3 -I $J/mcp_call.py --bin "$BIN/oap-mcp" --url "$URL" --credential-file "$CRED" "$@"; }
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

## The full journey (CLI run)

```sh
export RUN=cli
export DOMAIN=$RUN.$BASE CRED=$DIR/creds/onboard-$RUN.json
mkdir -p "$OUT/$RUN"
echo '{}' > empty.json
```

On the **build machine**, for the probes:

```sh
export J=$PWD/tests/vps/release-journey PROBE_OUT=$PWD/evidence/probes BASE=journey.example.com RUN=cli
mkdir -p "$PROBE_OUT"
```

`[build machine]` marks probe commands; everything else runs on the VPS. Start a
probe, run the VPS steps, then wait for the probe to finish.

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
Run the probe for the whole step. Durations are maximum windows; after the
operation finishes, send SIGTERM to the probe and wait for its JSON summary.
The probe must still be running when the action finishes; an expired window
does not prove coverage of the whole action. This avoids idle waits while
retaining observations throughout configuration and rollout.

```sh
# [build machine] start, then run configure → deploy → wait on the VPS
python3 -I $J/probe.py --url https://$RUN.$BASE/api --duration 240 --concurrency 2 --out "$PROBE_OUT/$RUN-D1.json" &
PROBE_PID=$!
# Run configure → deploy → wait on the VPS before the following two commands.
kill -TERM "$PROBE_PID"
wait "$PROBE_PID"
# [build machine] afterwards
python3 -I $J/probe.py --url https://$RUN.$BASE/api --duration 20 --expect-instances 3 --out "$PROBE_OUT/$RUN-D1-after.json"
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
Stop the traffic probe after the operation and incident observations complete;
retain its summary before starting E3.

**E3. Correct and exercise the volume.** Create a deploy plan with
`{"action":"deploy","applicationId":APP,"expectedVersion":V,"components":["api","notes"],"images":{"api":"<API2>","notes":"<NOTES2>"}}`,
then *execute*, *wait*, *instance health*. **Expect:** success, the probe with
`--expect-instances 3` shows `v2` on all 3, and `/notes` reports `boots: 2`.
The notes variant has the same program and volume contract but a distinct image
ID. This replaces the container through the product's deploy interface and
proves it read the existing volume. Record this counter for comparison in F.

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
- `/notes` `boots` is greater than in E3 (the volume survived another image
  replacement). An unchanged notes image need not restart during rollback;
  that is why E3 deploys a distinct notes image first.

### H: scale down under traffic

Change only `api.instances` back to 1. *Create a plan*
`{"action":"scale-down","applicationId":APP,"expectedVersion":V,"specification":<spec>}`,
*execute*, *wait*, with the probe running for 120 s.
Use 120 s as the maximum probe window and stop it after the operation finishes,
as in D1. Keep the probe running throughout the drain and successful completion.
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

## S: REST and MCP smoke checks

Run once with `RUN=rest` and once with `RUN=mcp`, after the CLI journey, using
only that run's interface (see the [interface reference](#interface-reference)).
Each takes about 10 minutes. Record every request and response.

```sh
export RUN=rest     # then mcp
export DOMAIN=$RUN.$BASE CRED=$DIR/creds/onboard-$RUN.json
mkdir -p "$OUT/$RUN"
```

- **S1. Discover.** MCP: `list` shows the lifecycle tools. REST: read the
  target capabilities. **Expect:** a structured answer, no error.
- **S2. Validate and create.** Fill the spec as in C1 (with this run's domain),
  *validate*, then *create a plan* `create` and *execute* it. Record `APP` and `V`.
  **Expect:** saved, no containers started.
- **S3. Owner app grant** exactly as C3, for this run's app; switch `CRED` to it.
- **S4. Deploy.** *Create a plan* `deploy`, *execute*, *wait*, *instance
  health*; then *execute* the same plan again. **Expect:** success with the
  `migrate` step at `exitCode: 0`; the replay returns the same `operationId`;
  `https://$DOMAIN/api` answers `v1` over trusted HTTPS.
- **S5. Incident read.** *Incident* for `api`. **Expect:** `observed_healthy`
  with freshness fields, and `applicationBehaviorVerified: false`.
- **S6. One denial.** Ask for *capabilities* of the CLI run's app. **Expect:** a
  structured denial, with no plan or operation created.

Smoke receipt `$OUT/$RUN/smoke-$RUN.json`:

```json
{"schemaVersion": 1, "run": "rest | mcp", "status": "passed | failed",
 "steps": {"discover": true, "validate": true, "createWithoutDeploy": true, "appGrant": true,
           "deployWithMigration": true, "planReplaySameOperation": true, "publicHttpsTrusted": true,
           "incidentRead": true, "crossAppDenied": true},
 "operations": ["..."], "findings": ["..."]}
```

## Final checks (after the CLI journey and both smoke checks)

**X1. Coexistence.** All three apps (CLI, REST and MCP runs) answer on their own hostname over trusted
HTTPS. A request with an unknown Host is rejected. Send it over HTTPS to a known
hostname (valid SNI and certificate) with `Host: unknown.invalid`; expect 404,
421 or 403. An HTTP-to-HTTPS redirect alone does not prove host rejection.

**X2. Controller outage.** Stop only OAP and probe all three apps for 60 s:

```sh
docker ps --format '{{.ID}} {{.Names}}' > "$OUT/X2-before.txt"     # VPS
(cd "$DIR" && docker compose stop platform)                        # VPS
for R in cli rest mcp; do                                          # [build machine]
  python3 -I $J/probe.py --url https://$R.$BASE/api --duration 60 --concurrency 2 --out "$PROBE_OUT/X2-$R.json" &
done; wait
(cd "$DIR" && docker compose up -d --wait platform)                # VPS
python3 scripts/install.py check --json --directory "$DIR"
docker ps --format '{{.ID}} {{.Names}}' > "$OUT/X2-after.txt"
```

**Expect:** zero probe failures, workload and Caddy container IDs unchanged, and
each interface works again after restart (one read call per interface).

**X3. Host health, footprint and disk.** Stop the monitor and summarize it:

```sh
kill "$(cat "$OUT/monitor.pid")"
sudo python3 -I $J/monitor.py summary --file "$OUT/monitor.jsonl" --kernel-log | tee "$OUT/X3-host.json"
docker stats --no-stream > "$OUT/X3-stats.txt"
docker system df > "$OUT/X3-disk.txt"
docker ps -a --filter name=migrate --format '{{.Names}}' | wc -l > "$OUT/X3-migrate-containers.txt"
```

**Expect:** `oomKills: 0`, `minMemAvailableMiB` above 100, no swap used, and SSH
responsive throughout. Compare container peaks with the memory budget above and
record any item that exceeded its estimate.
Kernel OOM observations cover the window from the monitor's first sample until
the summary runs, so earlier host incidents are not attributed to this journey,
and an OOM that killed the monitor itself is still counted. `kernelLogAvailable: false`
and `oomKills: null` mean evidence is unavailable; they do not prove zero OOMs.

## Summary receipt: `$OUT/v0.1-journey.json`

```json
{
  "schemaVersion": 1, "commit": "<sha>",
  "cli": "passed | failed", "smoke": {"rest": "passed | failed", "mcp": "passed | failed"},
  "coexistence": true, "controllerOutageFailures": 0,
  "host": {"memTotalMiB": 985, "swap": false, "minMemAvailableMiB": 0, "oomKills": 0,
           "kernelLogAvailable": true, "maxLoad1": 0, "sshLost": false, "containerPeakMiB": {}},
  "timings": {"prepareSeconds": 0, "loadSeconds": 0, "phaseA": "…", "cli": "…", "smoke": "…", "final": "…"},
  "bundle": {"commit": "<sha>", "platform": "linux/amd64", "dirtyTree": false},
  "findings": ["..."]
}
```

Hand back `release-journey-cli.json`, `smoke-rest.json`, `smoke-mcp.json`,
`v0.1-journey.json`, the VPS `$OUT` directory, the build machine's `prepare.json`
and `evidence/` (including probes), and the findings.

The journey passes only if the CLI journey and both smoke checks pass, the final
checks pass, and on the 1 GB host there were no OOM kills and the host stayed
responsive. If the host becomes unreachable, keep every partial receipt already
copied off and record where it stopped. Downtime during standard deploys (D1,
E2) is an observation, not a failure.

## Deferred to the QA track

Not part of v0.1. Kept here as the starting point for Q1 (integrity checker) and
Q3 (agent evaluations and interface parity).

- **Full REST and MCP journeys:** run phases C–H with `RUN=rest` and `RUN=mcp`,
  producing `release-journey-<run>.json`.
- **Parity report** `parity.json`: steps whose results differed between
  interfaces, and actions an interface could not express (known: MCP has no
  `verify` tool, use `inspect_instances`; owner approval is CLI-only by design).
- **Isolation matrix:** Each run's app credential is **denied** on the other two apps,
through its own interface: for example, the MCP agent calls `inspect_capabilities`
for the CLI app, and the REST agent calls `POST /api/v1/action-plans` with a
`deploy` for the MCP app. Expect a denial with a structured error, and no new
plan or operation.

- **Pause and revoke across interfaces:** Agent control is workspace-wide. Read it with
`oapo agent-control`, then pause all agents:

```sh
printf '{"enabled":false,"expectedVersion":%s,"reason":"journey pause check"}\n' "$CONTROL_VERSION" > pause.json
oapo --file pause.json set-agent-control
```

One read call per interface must now be denied. Re-enable with `"enabled":true`
at the new version, and confirm each interface works again. Then
`oapo --credential <credentialId> revoke` one run's onboarding credential: its
next call is denied, and the other runs are unaffected.

