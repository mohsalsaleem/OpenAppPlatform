# v0.1 release journey on a real VPS

**Purpose:** prove the v0.1 promise as **one continuous journey** on a fresh
server: a multi-component application, operated end to end by an
owner-authorized agent through the `oap` CLI. This is roadmap v0.1 closeout
tasks 2 and 6. The result is one receipt, `release-journey.json`.

**Who runs it:** a testing agent on a disposable VPS, plus a human owner for the
trust steps (or the testing agent acting as owner **only** in the clearly marked
owner steps, using the owner credential file, never in agent steps).

## Ground rules

1. **One management interface.** Every OAP operation uses the `oap` CLI. Docker,
   `curl` and `probe.py` are used only to build images and to observe
   application traffic, never to change OAP state.
2. **Identities stay separate.** Owner steps use `owner.json`. Agent steps use
   `agent.json` (onboarding) or `agent-app.json` (app-scoped). Never pass
   credentials in arguments or logs.
3. **No workarounds.** Never edit the control database, call private endpoints,
   hand-edit Caddy, or restart containers with Docker to make a step pass.
4. **Stop on surprises.** If a step's result differs from **Expect**, stop,
   capture evidence (command output, `operation`, `incident`, `instances`), and
   record the step as failed in the receipt. Do not retry with a new plan after an
   uncertain result; re-run `execute-action` with the **same** plan ID instead.
5. **Retain everything:** plan IDs, plan hashes, operation IDs and every probe
   summary, in the order they happened.

## What you need

- A fresh Linux VPS (2 vCPU / 2 GB RAM is enough) with Docker Engine (API ≥ 1.45),
  Compose v2, Python ≥ 3.8 and Git. Public TCP 80/443 open.
- A DNS name pointing at the VPS, e.g. `journey.example.com` (A record only).
  Without DNS, use `--ingress-tls internal`, a `.test` name in `/etc/hosts`, and
  `--cafile` on the probe; record that public ACME was **not** tested.
- The OAP repository at the **release candidate commit**. Record `git rev-parse HEAD`.

## Variables

```sh
export REPO=$PWD                         # repository root at the RC commit
export J=$REPO/tests/vps/release-journey
export DIR=/opt/oap-journey              # installation directory (new, empty)
export DOMAIN=journey.example.com        # your DNS name
export URL=http://127.0.0.1:8878         # OAP API, loopback only
export OUT=/opt/oap-journey-evidence     # evidence directory
mkdir -p "$OUT"
oapo() { ./bin/oap --url "$URL" --credential-file "$DIR/owner.json" "$@"; }      # owner
oapa() { ./bin/oap --url "$URL" --credential-file "$DIR/agent.json" "$@"; }      # agent (onboarding)
oapx() { ./bin/oap --url "$URL" --credential-file "$DIR/agent-app.json" "$@"; }  # agent (app-scoped)
```

Every `oap` command prints one JSON envelope. Plan IDs are at `.data.id`, plan
hashes at `.data.planHash`, execution results at `.data.result` (with
`applicationId`, `definitionVersion` and `operationId` where relevant). Save
each envelope to `$OUT/NN-step.json`.

## Phase A: build and install (host administration)

**A1. Build OAP and the CLI.**

```sh
cd "$REPO"
docker build --label org.opencontainers.image.revision="$(git rev-parse HEAD)" -t openappplatform:rc .
go build -o bin/oap ./cmd/oap      # or extract the shipping oap binary from the image
OAP_IMAGE=$(docker image inspect --format '{{.Id}}' openappplatform:rc)
```

**A2. Build the application images** and record their content IDs.

```sh
docker build -t oap-journey-api:base   examples/hello-api
docker build -t oap-journey-notes:v1   examples/persistent-web
docker build -t oap-journey-worker:v1  examples/worker
docker build -t oap-journey-job:v1     -f $J/job.Dockerfile $J
for v in web-v1 v1 v2; do
  docker build -t oap-journey-api:$v -f $J/api-variant.Dockerfile \
    --build-arg BASE=oap-journey-api:base --build-arg VERSION=$v $J
done
docker build -t oap-journey-api:broken -f $J/api-broken.Dockerfile --build-arg BASE=oap-journey-api:base $J
id() { docker image inspect --format '{{.Id}}' "$1"; }
export JOB=$(id oap-journey-job:v1) WEB=$(id oap-journey-api:web-v1) API1=$(id oap-journey-api:v1) \
       API2=$(id oap-journey-api:v2) APIBAD=$(id oap-journey-api:broken) \
       NOTES=$(id oap-journey-notes:v1) WORKER=$(id oap-journey-worker:v1)
```

**A3. Install with packaged Caddy and public HTTPS.**

```sh
python3 scripts/install.py init --json --directory "$DIR" --image "$OAP_IMAGE" \
  --environment production --ingress-domain "$DOMAIN" --ingress-tls letsencrypt --ingress-bind 0.0.0.0
python3 scripts/install.py start --json --directory "$DIR"
python3 scripts/install.py check --json --directory "$DIR" | tee "$OUT/A3-check.json"
```

**Expect:** `check` reports healthy OAP, PostgreSQL and Caddy. The OAP API answers
only on loopback. Port 443 has no certificate yet (none is requested until an app
with HTTPS routes is deployed).

## Phase B: owner establishes trust (owner only)

**B1. Owner setup.** Write `private-owner.json` with `{"email","name","password"}`
(mode 0600, outside the repository).

```sh
python3 scripts/install.py owner-setup --json --directory "$DIR" \
  --cli-bin ./bin/oap --out "$DIR/owner.json" < private-owner.json
```

**B2. Onboarding grant for the agent.**

```sh
cat > grant.json <<'EOF'
{"name": "Release journey agent", "targetId": "docker-local", "environment": "production",
 "maxApplications": 1, "actions": ["configure", "deploy", "recover", "rollback"]}
EOF
oapo --out "$DIR/agent.json" --file grant.json grant | tee "$OUT/B2-grant.json"
```

**Expect:** `agent.json` is created with mode 0600. The owner credential is never
given to agent steps.

## Phase C: agent creates and deploys the application (agent)

**C1. Fill the spec.**

```sh
sed -e "s|__JOB_IMAGE__|$JOB|" -e "s|__API_V1_IMAGE__|$API1|" -e "s|__WEB_IMAGE__|$WEB|" \
    -e "s|__NOTES_IMAGE__|$NOTES|" -e "s|__WORKER_IMAGE__|$WORKER|" -e "s|__DOMAIN__|$DOMAIN|" \
    $J/application.template.json > spec.json
python3 -c 'import json;s=json.load(open("spec.json"));print(json.dumps({"specification":s}))' > validate.json
oapa --file validate.json validate-spec | tee "$OUT/C1-validate.json"
```

**Expect:** `ok: true`.

**C2. Create (save only).**

```sh
python3 -c 'import json;s=json.load(open("spec.json"));print(json.dumps({"action":"create","specification":s}))' > create.json
oapa --file create.json action-plan | tee "$OUT/C2-plan.json"          # keep .data.id
echo '{}' > empty.json
oapa --plan "$CREATE_PLAN" --file empty.json execute-action | tee "$OUT/C2-create.json"
export APP=<.data.result.applicationId>  V=<.data.result.definitionVersion>
```

**Expect:** application saved, **no containers started** (`oapa --app $APP instances`
shows none running).

**C3. Owner issues an app-scoped credential** (owner step; needed because
scale-down is granted per application).

```sh
cat > app-grant.json <<EOF
{"name": "Release journey app agent", "scope": "operate", "applicationId": "$APP",
 "actions": ["configure", "deploy", "scale-down", "recover", "rollback"]}
EOF
oapo --out "$DIR/agent-app.json" --file app-grant.json app-grant | tee "$OUT/C3-app-grant.json"
```

From here on, agent steps use `oapx`.

**C4. First deploy (runs the migration job).**

```sh
echo "{\"action\":\"deploy\",\"applicationId\":\"$APP\",\"expectedVersion\":$V}" > deploy.json
oapx --file deploy.json action-plan | tee "$OUT/C4-plan.json"
oapx --plan "$DEPLOY1_PLAN" --file empty.json execute-action | tee "$OUT/C4-exec.json"
oapx --operation "$OP" --timeout 10m wait | tee "$OUT/C4-wait.json"
oapx --app "$APP" verify | tee "$OUT/C4-verify.json"
oapx --plan "$DEPLOY1_PLAN" --file empty.json execute-action > "$OUT/C4-replay.json"
```

**Expect:**
- The operation succeeds, and its first step is `migrate` with `exitCode: 0`.
- `verify` reports every instance healthy.
- Replaying the same plan returns the **same** `operationId`.
- `https://$DOMAIN/` answers from `web`, `https://$DOMAIN/api` from `api` (version
  `v1`), and `https://$DOMAIN/notes` returns `{"boots":1}`. The certificate is
  publicly trusted (no `--cafile` needed).

```sh
curl -sS https://$DOMAIN/api | tee "$OUT/C4-api.json"
curl -sS https://$DOMAIN/notes | tee "$OUT/C4-notes.json"
curl -sS -o /dev/null -w '%{http_code}\n' -H 'Host: unknown.invalid' https://$DOMAIN/ -k   # expect 404 or TLS refusal
```

## Phase D: scale up with live traffic (agent)

**D1.** Change `api.instances` from 1 to 3 in `spec.json` (nothing else), then
configure and deploy. Run the probe in parallel for the whole step.

```sh
python3 -I $J/probe.py --url https://$DOMAIN/api --duration 240 --out "$OUT/D1-probe.json" &
# configure: {"action":"configure","applicationId":APP,"expectedVersion":V,"specification":<spec>}
oapx --file configure.json action-plan    # → execute-action → note new definitionVersion
oapx --file deploy.json action-plan        # deploy at the new version → execute-action → wait → verify
wait
python3 -I $J/probe.py --url https://$DOMAIN/api --duration 20 --expect-instances 3 --out "$OUT/D1-after.json"
```

**Expect:**
- The deploy succeeds. The migration runs again (a new release runs a new job).
- `D1-after.json` shows **3 distinct instances**, all version `v1`.
- Failures during the rollout are recorded but don't fail the step (standard
  deploys may interrupt traffic; this is a known limit).

## Phase E: injected failure, diagnosis and correction (agent)

**E1. Failing migration blocks rollout.** Set `migrate.env.JOB_EXIT_CODE` to `"7"`,
then configure and deploy.

**Expect:** the operation ends `failed`, the `migrate` step has `exitCode: 7`, and
no `api` step was dispatched. `/api` still answers `v1` from 3 instances.

```sh
oapx --app "$APP" --component migrate incident | tee "$OUT/E1-incident.json"
```

**Expect:** `state` is `issue`, the job failure is identified, and
`applicationBehaviorVerified` is `false`.

Restore `JOB_EXIT_CODE` to `"0"` and configure (save) so the definition matches
the last good release again.

**E2. Broken API release.** Deploy with an image override, with the probe running:

```sh
python3 -I $J/probe.py --url https://$DOMAIN/api --duration 180 --out "$OUT/E2-probe.json" &
echo "{\"action\":\"deploy\",\"applicationId\":\"$APP\",\"expectedVersion\":$V,\"components\":[\"api\"],\"images\":{\"api\":\"$APIBAD\"}}" > deploy-bad.json
oapx --file deploy-bad.json action-plan   # → execute-action → wait (expect non-zero exit)
oapx --app "$APP" --component api --ordinal 1 incident | tee "$OUT/E2-incident.json"
wait
```

**Expect:** readiness never passes and the operation does not succeed. `incident`
reports `issue` for `api` and shows the image change in its comparison. Record
whether `/api` stayed available (`E2-probe.json`); standard deploys may cause
downtime here, so record it as a known limit rather than a failure. If the
operation lands in **attention** instead of `failed`, stop and capture
`operation` and `incident`: that's a finding.

**E3. Correct with a good release.** Deploy `api` with `$API2`, the same way as E2,
then `wait` and `verify`.

**Expect:** success. `probe.py --expect-instances 3` shows version `v2` from all
3 instances.

## Phase F: app-wide rollback (agent)

Roll back to the **D1 scale-up release** (v1 images, api at 3 instances). Its
release ID is in the D1 deploy result.

```sh
echo "{\"releaseId\":\"$D1_RELEASE\",\"expectedVersion\":$V}" > rb-read.json
oapx --app "$APP" --file rb-read.json rollback-plan | tee "$OUT/F1-plan.json"
echo "{\"releaseId\":\"$D1_RELEASE\",\"expectedVersion\":$V,\"planHash\":\"$HASH\",\"acknowledgeDataCompatibility\":true}" > rb.json
oapx --app "$APP" --key journey-rollback-1 --file rb.json rollback | tee "$OUT/F1-rollback.json"
oapx --operation "$OP" --timeout 10m wait
oapx --app "$APP" --key journey-rollback-1 --file rb.json rollback > "$OUT/F1-replay.json"
oapx --app "$APP" verify
```

**Expect:**
- The plan lists every owned web/worker instance and **skips the `migrate` job**.
- The rollback succeeds, and the replay returns the same operation.
- `/api` answers `v1` from 3 instances.
- The migration did **not** run again: there's no `migrate` step in the rollback operation.
- `/notes` shows `boots` greater than 1, meaning the volume data survived. Compare with `C4-notes.json`.

## Phase G: controller outage (host administration + traffic only)

Stop **only** the OAP service, run the probe for 60 seconds, then start it again
(commands from the [installation guide](../../../docs/installation.md)):

```sh
docker ps --format '{{.ID}} {{.Names}}' > "$OUT/G-before.txt"
(cd "$DIR" && docker compose stop platform)
python3 -I $J/probe.py --url https://$DOMAIN/api --duration 60 --out "$OUT/G-probe.json"
(cd "$DIR" && docker compose up -d --wait platform)
python3 scripts/install.py check --json --directory "$DIR"
docker ps --format '{{.ID}} {{.Names}}' > "$OUT/G-after.txt"
```

**Expect:** zero probe failures while OAP is down. Workload and Caddy container
IDs are unchanged before and after. `oapx --app $APP instances` works again
after the restart.

## Phase H: scale down with live traffic (agent)

Set `api.instances` back to 1 in `spec.json` (the only change) and send a
`scale-down` plan:

```sh
python3 -I $J/probe.py --url https://$DOMAIN/api --duration 120 --out "$OUT/H1-probe.json" &
# {"action":"scale-down","applicationId":APP,"expectedVersion":V,"specification":<spec with api.instances=1>}
oapx --file scale-down.json action-plan   # → execute-action → wait
wait
```

**Expect:** success after the 31-second drain grace, `H1-probe.json` has **0
failures**, and one `api` instance remains.

## Phase I: final checks

- `oapx --app $APP verify` passes. `/`, `/api` and `/notes` answer over trusted HTTPS.
- `python3 scripts/install.py check --json --directory "$DIR"` passes.
- **Disk:** record `docker system df` and the number of retained `migrate`
  containers (`docker ps -a --filter name=migrate`). This is evidence for roadmap
  closeout task 3, not a pass/fail check.
- **Footprint:** record `docker stats --no-stream` for OAP, PostgreSQL and Caddy.

## Optional phases

- **Owner review path:** repeat H1 with `"requireReview": true`. The agent's
  `execute-action` must be refused until the owner runs `oapo --plan ID --file
  approve.json approve-action` with `{"planHash": "..."}`. An agent attempting
  `approve-action` must be denied.
- **Component removal:** requires the owner to add `remove-component` to the
  app grant. Remove `worker` per [component lifecycle](../../../docs/topology-lifecycle.md),
  with owner approval.
- **Linked PostgreSQL:** not part of this journey; the existing local gate covers it.

## Receipt: `release-journey.json`

Write this at the end, in `$OUT`, with no secret values:

```json
{
  "schemaVersion": 1,
  "status": "passed | failed",
  "commit": "<git rev-parse HEAD>",
  "oapImage": "<OAP_IMAGE>",
  "host": {"provider": "...", "vcpu": 2, "memoryGiB": 2, "os": "...", "docker": "..."},
  "tls": "letsencrypt | internal",
  "interface": "oap CLI",
  "steps": {
    "install": true, "ownerSetup": true, "onboardingGrant": true, "appGrant": true,
    "createWithoutDeploy": true, "deployWithMigration": true, "planReplaySameOperation": true,
    "publicHttpsTrusted": true, "unknownHostRejected": true,
    "scaleUpTo3": true, "trafficOnAll3": true,
    "failingMigrationBlocksRollout": true, "incidentIdentifiesJob": true,
    "brokenReleaseNotCompleted": true, "incidentIdentifiesApi": true, "correctedToV2": true,
    "appWideRollback": true, "rollbackSkippedMigration": true, "rollbackReplay": true, "volumeDataRetained": true,
    "controllerDownTrafficOk": true,
    "scaleDownTo1": true, "scaleDownFailures": 0
  },
  "observations": {
    "scaleUpFailures": 0, "brokenReleaseApiFailures": 0, "migrateContainersRetained": 0,
    "diskUsage": "...", "idleFootprint": "..."
  },
  "operations": ["<operation IDs in order>"],
  "findings": ["<anything that differed from Expect>"]
}
```

Hand back `release-journey.json`, the `$OUT` directory and the list of findings.
A run passes only if every step matched **Expect**. Downtime during standard
deploys (D1, E2) is recorded as an observation, not a failure.
