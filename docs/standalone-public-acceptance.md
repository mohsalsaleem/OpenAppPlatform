# Standalone public installation acceptance

This is the executable setup contract for M3.1. Follow the commands in order;
do not replace installer steps with database edits, private endpoints, manual
proxy containers or hand-written target authority. Implement and pass the local
installation end-to-end test before running the candidate on the public server.
Public issuance, local HTTPS, certificate persistence and renewal are separate
results: a restart check does not prove scheduled renewal.

## Prerequisites and retire the previous attempt

Use a Linux host with Docker API 1.45 or newer, Compose v2, Python 3.8 or newer,
and an immutable OAP image built from the tested source using the shipping
Dockerfile. Build on a development machine and load the image on a small host
if necessary. Docker/OS prerequisites are host administration, not OAP APIs.
The Vultr test host already has these prerequisites; its original Ubuntu 18.04
installation is retained and is not a supported fresh-OS reference.

`www.nishashipping.com` resolves to this host. Public inbound TCP 80/443 and
outbound HTTPS must work. Do not add an AAAA record unless IPv6 reaches the host.
No wildcard DNS, DNS API token or wildcard certificate is needed.

The owner explicitly authorized taking this low-priority site down. Preserve its
original files, private site/configuration archives, previous OAP metadata and
other service data. For this host only, retire the earlier attempt:

```sh
sudo systemctl disable --now nginx
(cd /opt/openappplatform && docker compose stop)
docker stop oap-8555a20de347-website-1
```

This leaves the site offline and frees the public ports. Do not delete volumes,
replace the OS, prune unrelated resources or reuse the earlier metadata database.

## Build and install

Check out the tested OAP revision and build the documented shipping image:

```sh
docker build --label org.opencontainers.image.revision="$(git rev-parse HEAD)" \
  -t openappplatform:local .
OAP_IMAGE=$(docker image inspect --format '{{.Id}}' openappplatform:local)
python3 scripts/install.py init --json --directory /opt/oap-standalone \
  --image "$OAP_IMAGE" --environment production \
  --ingress-domain www.nishashipping.com --ingress-tls letsencrypt \
  --ingress-bind 0.0.0.0
python3 scripts/install.py start --json --directory /opt/oap-standalone
python3 scripts/install.py check --json --directory /opt/oap-standalone
```

The new installer prepares a pinned, dedicated, non-root Caddy service with
private administration and durable configuration/certificate volumes. Start
records its immutable identity and initial configuration commitment before
starting OAP. Existing publications must not silently acquire a replacement
proxy identity. The API stays loopback-only. No certificate is requested until
an authorized application with HTTPS routes is deployed.

For local acceptance use `--ingress-tls internal`, a `.test` hostname and loopback
port mappings. `letsencrypt-staging` is an explicit optional test mode; its
certificates are not normally trusted. Staging-to-production authority handoff
is not implicit: use separate disposable installations until a reviewed handoff
workflow exists. Default installations without ingress retain their old behavior.

## Human owner establishes trust and grants agent access

Extract/use the matching shipping `oap` binary as described in the installation
guide. Create a private owner input file containing `{email,name,password}`;
never put those values in command arguments or logs. Then:

```sh
python3 scripts/install.py owner-setup --json --directory /opt/oap-standalone \
  --cli-bin ./bin/oap --out /opt/oap-standalone/owner.json < private-owner.json
./bin/oap --url http://127.0.0.1:8878 \
  --credential-file /opt/oap-standalone/owner.json \
  --out /opt/oap-standalone/agent.json --file grant.json grant
```

`grant.json` selects one named agent, `docker-local`, `production`, an application
cap of one and `configure/deploy/restart/recover`. Keep owner and agent identities
separate. The installation helper never auto-creates an owner or an agent.

The human owner performs this step. An operating agent receives only the scoped
agent credential, never the owner password/session or installer setup token. The
acceptance runner does not set up owners or issue grants and refuses owner sessions.

## Agent deploys through the CLI

Build the site's static image from the retained source using its Dockerfile.
Declare its immutable content ID in an `oap.dev/v1alpha1` Application. The web
component listens on 8080, has one instance, image readiness, a 64 MiB limit and
no host port. Declare ingress domain `www.nishashipping.com`, `https: true`, and
route `/` to that component. Validate and plan through the scoped CLI:

```sh
./bin/oap --url http://127.0.0.1:8878 --credential-file /opt/oap-standalone/agent.json \
  --file validate.json validate-spec
./bin/oap --url http://127.0.0.1:8878 --credential-file /opt/oap-standalone/agent.json \
  --file create.json action-plan
# Retain the returned plan ID. Execute that exact plan with an empty JSON object.
./bin/oap --url http://127.0.0.1:8878 --credential-file /opt/oap-standalone/agent.json \
  --plan "$CREATE_PLAN" --file empty.json execute-action
# Plan deploy with the returned application ID and current definition version.
./bin/oap --url http://127.0.0.1:8878 --credential-file /opt/oap-standalone/agent.json \
  --file deploy.json action-plan
./bin/oap --url http://127.0.0.1:8878 --credential-file /opt/oap-standalone/agent.json \
  --plan "$DEPLOY_PLAN" --file empty.json execute-action
./bin/oap --url http://127.0.0.1:8878 --credential-file /opt/oap-standalone/agent.json \
  --operation "$OPERATION" --timeout 120s wait
```

Request bodies follow [portable lifecycle](portable-lifecycle.md). Saving a spec
does not deploy. Retain plan IDs/receipts before retrying; do not mint new plans
after an uncertain dispatch. No shell, SQL or mutable route configuration enters
an agent's application request.

The checked-in `scripts/standalone-acceptance.py` runs this exact owner/scoped-agent
CLI sequence and retains its plan IDs before execution. The local E2E test uses
the same script. For the Vultr site, build the checked-in static-site example:

```sh
mkdir /opt/nisha-site-build
cp examples/static-site/Dockerfile examples/static-site/Caddyfile /opt/nisha-site-build/
cp -a /usr/share/nginx/html/nishashipping /opt/nisha-site-build/site
docker build -t nishashipping:oap-static-v1 /opt/nisha-site-build
cp examples/static-site/application.json /opt/nisha-site-build/application.json
# Fill the example through normal JSON configuration, not control database edits.
SITE_IMAGE=$(docker image inspect --format '{{.Id}}' nishashipping:oap-static-v1)
export SITE_IMAGE
python3 - <<'PY'
import json, os
from pathlib import Path
p = Path('/opt/nisha-site-build/application.json')
x = json.loads(p.read_text())
x['spec']['name'] = 'nisha-shipping'
x['spec']['components'][0]['image'] = os.environ['SITE_IMAGE']
x['spec']['ingress']['domain'] = 'www.nishashipping.com'
p.write_text(json.dumps(x, indent=2) + '\n')
PY
python3 scripts/standalone-acceptance.py deploy --directory /opt/oap-standalone \
  --spec /opt/nisha-site-build/application.json --cli-bin ./bin/oap \
  --credential-file /opt/oap-standalone/agent.json
python3 scripts/standalone-acceptance.py verify --directory /opt/oap-standalone \
  --spec /opt/nisha-site-build/application.json --origin https://www.nishashipping.com \
  --source-root /usr/share/nginx/html/nishashipping
```

On this previously prepared VPS, use the supported interpreter/Docker prerequisites
without replacing system Python: `export PATH=/opt/oap-prerequisites/python/bin:/opt/docker-27.5.1/docker:$PATH`.
Use a fresh private owner input. Keep previous credentials with the previous
installation; do not copy its sessions into the new one. The acceptance script
refuses a changed specification fingerprint and retries the same recorded plans.
For normal app changes later, use the ordinary configure/deploy CLI flow instead.

## Acceptance and evidence

1. Run the reproducible local gate, including fresh installer → local trusted
   HTTPS → scoped agent deployment → controller recovery → Caddy resume.
2. On the public host verify HTTP redirects, normal HTTPS hostname/chain trust,
   all 426 retained files, and rejection of an unrelated Host.
3. Verify proxy administration and OAP/metadata are not public. App containers
   have no published host ports and share only their owned application network.
4. Stop/recover only OAP with the documented Compose/installer commands while
   making HTTPS requests. Workload and proxy identities must remain unchanged.
5. Restart the proxy using `docker compose restart ingress`; verify configuration
   resume, certificate identity/persistence and HTTPS. Do not delete certificates
   to pretend a reissue is a scheduled-renewal test.
6. Save structured non-secret receipts, exact source/image identities, footprint
   snapshots and outstanding failures. Mark actual renewal unverified until
   separately exercised. Preserve original backups throughout.

If setup fails, retain its directory and evidence; fix the supported installer
or runtime, retest locally, then rerun the same documented step. Do not repair
control database rows or manually relax authority to make acceptance pass.
