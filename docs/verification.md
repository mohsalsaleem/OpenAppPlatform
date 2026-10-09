# Verification

Verified locally on 9 October 2026 with Go 1.26.5, Node 22, PostgreSQL 17,
Chromium, local Docker, and the existing Coolify mac-studio context.

## Passed checks

| Check | Evidence |
| --- | --- |
| Unit and PostgreSQL system tests | go test -race ./... passed with TEST_DATABASE_URL configured |
| Static Go checks | go vet ./... passed |
| Dashboard build | TypeScript and Vite production build passed |
| Browser flow | Authentication rejection and success, two-component setup, deployment confirmation, Escape handling, configuration, target discovery, and mobile overflow checks passed |
| Docker packaging | Non-root controller image built; health and authenticated API smoke checks passed |
| Container browser flow | Playwright workflow passed against the Docker-packaged controller |
| Standalone example | Hello API image built and ran on Docker; root JSON and readiness endpoint returned HTTP 200 |
| Coolify lifecycle | Dedicated staging resources created, deployed, inspected, and queried for logs; immutable image digest matched; real HTTPS endpoint returned HTTP 200 |
| Local release example | hello-coolify adopted the retained image resource and completed a recorded release |

System tests exercise a durable job after controller reconstruction, concurrent
duplicate requests, overlapping-release rejection, immutable release snapshots,
exclusive adoption, an interrupted dispatch, and a mixed successful/failed
component release. Tests use a four-connection query pool and also run four
application releases concurrently. CI exposed a pool-starvation deadlock in the
initial duplicate-delivery path; transaction reuse and dedicated lock sessions
fix it, with regression coverage. Ambiguous dispatch blocks further releases until inspected
and reconciled.

## Real Coolify fixtures

All mutations were confined to the dedicated openappplatform-integration project
and its staging environment. Existing production applications were not changed.

| Fixture | Resource | Verified deployment |
| --- | --- | --- |
| Local dashboard example | ibgspvrw2hvvm6aa2a9sblmv | epqszxtepgdn6lproz8ikrrq |
| New-resource lifecycle fixture | 74gq6dmjkkdyjhqw9pmn0nwe | s3dau0g0qpewmoaxawdc54hl |

The fixture image is axllent/mailpit pinned to digest
sha256:df6c2541907e1be6fac21f509927cf6ed771617a1f4b361ef66d97bd05593d2d.
The local example's recorded controller deployment is
415e07d93892a3bdd3c51d7f958153a8.

The new resource's initial HTTPS check encountered certificate issuance delay.
The live test now waits for readiness with normal TLS validation and a bounded
90-second deadline. Subsequent verification passed without bypassing TLS.

Fixtures are retained for inspection. Resource IDs and target placement are
recorded in .coolify/deploy.yaml; credentials are excluded from Git. The platform
itself runs locally and has not been deployed to Coolify.

## Local review

The local configuration is in ignored .env.local and .local/targets.json. The
platform access token is generated and kept in the local credential file. Browser
screenshots in ignored .local were inspected at desktop and mobile widths.

The Docker Desktop credential helper stalled public image pulls during packaging
verification. An isolated anonymous Docker configuration was used for those
public images; the owner's normal Docker credentials were not changed.

## Scope limits

Standard deployment does not promise uninterrupted traffic. A successful release
requires provider completion and a running workload; configurable application
readiness checks, ongoing health reconciliation, routing, and advanced strategies
are subsequent milestones. Workers, jobs, Git source adoption, source builds,
other adapters, AI, and MCP are not implemented yet.

This is a single-owner preview. Production identity, roles, audit retention,
versioned upgrade migrations, backup and restore verification, and explicit
attention-state resolution remain required before production rollout. The
repository's license choice is also outstanding.

## Follow up offline milestone

The direct Docker adapter passed a real cached-image lifecycle on Docker Desktop:
create, deploy, HTTP readiness, discovery, logs, repeat deployment, artifact
replacement with a changed internal port, stable resource identity, and stop.
No registry pulls were enabled. Fixtures were left stopped for inspection.

Configuration editing passed HTTP, PostgreSQL, concurrent-edit, stale-version,
and frozen-release tests. Migrations passed idempotency and checksum checks.
Deployment admission now records definition versions and preserves idempotent
retries after configuration changes.

The read-only MCP server passed unit tests plus a real stdio handshake, tool
discovery, and authenticated application read against the isolated offline
workspace. This does not imply full conformance testing against every MCP client.
