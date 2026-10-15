# v0.1 release journey: 2026-10-15

Candidate: `11d5b85c38a390d84c464cd04e9defa522915953`, clean tree,
`linux/amd64` bundle. Followed the candidate's reduced v0.1 scope: full CLI
journey, REST and MCP smoke checks, then shared checks. No product code changed.
The required local journey passed before the VPS run began.

| Run | Result | Duration |
|---|---|---|
| Local revised journey, internal TLS | Passed | 11m 32s |
| VPS CLI journey | **Failed at H, scale-down traffic** | 11m 46s |
| VPS REST smoke | Passed | 1m 47s |
| VPS MCP smoke | Passed | 1m 37s |
| VPS including preparation, phase evidence copies and final checks | **Failed** | 23m 19s |

## Acceptance failure: VPS-H-001

Scale-down from three API instances to one reported `succeeded`, but **2 of 277
public requests failed**: one `RemoteDisconnected`, one `URLError`. Expected:
zero failed requests. The CLI run stopped at this assertion; it was not retried.
Independent smoke and final checks continued. Local CLI scale-down recorded zero
failures in 899 requests; this does not cancel the VPS failure.

A Caddy reload and `server shutdown: context canceled` occurred within the probe
window. API containers reported no OOM kills or restarts. **Root cause remains
unconfirmed:** this probe records exception classes, not per-request timestamps
or the underlying `URLError` reason. See [findings.json](findings.json) for the
operation, instance, incident, traffic and host diagnostic evidence references.
An earlier full REST journey also observed scale-down disconnects; that finding
remains unresolved. This candidate is not accepted for the zero-loss scale-down
promise.

## Checks that passed

- All three domains used system-trusted Let's Encrypt certificates:
  `cli.nishashipping.com`, `rest.nishashipping.com`, `mcp.nishashipping.com`.
  Certificate renewal was not tested.
- Coexistence and unknown-host rejection passed. Controller-outage traffic had
  zero failures; workload/proxy container IDs stayed unchanged. All three
  interfaces worked after controller restart.
- VPS: one CPU, 985 MiB RAM, no swap, zero kernel OOM kills, SSH responsive,
  minimum available memory 256 MiB. Observed OAP/PostgreSQL/Caddy peaks were
  24.8/70.7/27.0 MiB. Raw monitor output contains an unused `--` placeholder stats
  entry; host memory and kernel OOM evidence are independent of it.
- CLI steps before H passed: migration, replay, scale-up, injected migration and
  API failures, incident diagnosis, correction, app-wide rollback and volume
  retention.

Each agent run used only its designated interface. Owner setup, grants and
approvals used Owner CLI. MCP uses documented `inspect_instances` because it has
no `verify` tool; no new smoke interface gaps were found. Full REST/MCP journeys,
interface parity, the isolation matrix and pause/revoke remain deferred by the
candidate roadmap.

## Artifacts

- [CLI receipt](release-journey-cli.json)
- [REST smoke receipt](smoke-rest.json)
- [MCP smoke receipt](smoke-mcp.json)
- [Overall receipt](v0.1-journey.json)
- [Findings](findings.json)
- [Complete evidence archive](release-journey-evidence.tar.gz)

Evidence references inside receipts resolve relative to the extracted archive's
`release-journey-11d5b85/` directory. It contains local evidence, merged VPS/client
evidence, bundle provenance, the driver sources and execution logs, plus the
unmodified archive of `/opt/oap-journey-evidence`. Original driver filenames
`X3-*` for controller outage and `X5-*` for footprint are retained; copies with
the current runbook's `X2-*` and `X3-*` names are included. These are filename
aliases, not extra test runs.

The complete archive was audited for credential values and private files.
Credentials, installation environment files, databases and private keys are
excluded. The test stack remains running on the VPS; the previous
`/opt/oap-standalone` stack remains stopped as authorized. No prior test evidence
or volumes were deleted. No OAP code fix or manual workload recovery was used.
