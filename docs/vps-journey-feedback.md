# VPS journey feedback: make 1 GB the test target

Recorded 14 October 2026 against `5a63037fabf9cc0470bb1a70f82694aedb271eb3`.

Owner feedback: constrain or reduce the release journey so it can run on a
**1 GB VPS**. Raising the server requirement is not the intended solution for
OAP's one-person, lightweight hosting use case.

## Observations

The target VPS reported 985 MiB of RAM and no swap. The current release journey
asks for at least 2 GB RAM and builds the platform, frontend, CLI, MCP server and
application fixtures on the target host. Phase A reached Go compilation and
frontend transformation. SSH subsequently ended before authentication or timed
out during banner exchange. Memory exhaustion is a hypothesis, not a confirmed
cause: no kernel OOM evidence was retrieved.

Testing stopped before any CLI, REST or MCP application journey or Phase X.
Build cancellation, installation state and the remote evidence archive could
not be confirmed after SSH became unavailable. The recorded partial attempt
took approximately 7 minutes 35 seconds. This is an environment/test-design
finding, not evidence that any OAP interface failed.

## Required test changes

- Target 1 GB total server RAM; measure available memory and disk before starting.
  Do not silently require swap or a larger server to pass.
- Separate build requirements from runtime requirements. Provide a documented,
  reproducible way to build the exact candidate and fixtures off-host and supply
  immutable images plus matching CLI/MCP binaries to the VPS. Do not require
  frontend or Go compilation on the 1 GB runtime host.
- Bound fixture memory and concurrency. Keep the multi-component app, migration,
  replica scaling, persistent volume and failure/recovery assertions; reduce
  fixture overhead rather than removing the behaviors under test.
- Run CLI, REST and MCP sequentially and budget for all three retained apps during
  coexistence checks. Avoid overlapping builds or unnecessary background work.
- Record platform, PostgreSQL, Caddy and fixture memory, peak use where measurable,
  disk use, timings and host responsiveness. Keep evidence collection bounded and
  preserve partial receipts when the host becomes unreachable.
- Document preparation and cleanup through supported installation and operating
  interfaces. Preserve one interface per agent run, separate owner credentials,
  public HTTPS, stop-on-surprise behavior and stable replay identities.

## Acceptance

The revised runbook completes Phase A, Phase B, the CLI/REST/MCP journeys and
Phase X on a 1 GB VPS without OOM termination or loss of host responsiveness.
It produces all three run receipts, parity, timings and the evidence archive.
The preparation/build lane and exact candidate identity are recorded.

## Response (runbook revision)

The [runbook](../tests/vps/release-journey/README.md) now targets a 1 GB VPS
without swap:

- **Off-host build lane:** `bundle.py prepare` builds the candidate image, the
  `oap` and `oap-mcp` binaries for the VPS platform and every fixture image on a
  build machine, with a manifest of the commit, image IDs and checksums.
  `bundle.py load` verifies checksums, loads the images and confirms each image
  ID. No Go, Node or Docker builds run on the VPS.
- **Memory budget:** OAP, PostgreSQL and Caddy from the measured footprint
  (~200 MiB together); total estimate ~615 MiB of 985, with a stop rule below
  100 MiB available.
- **Bounded fixtures:** every fixture has a 64 MiB cap (OAP's minimum limit;
  actual use is a few MiB). Runs are sequential; each run ends scaled back to one
  API instance, so retained apps stay small during coexistence checks.
- **Traffic off the host:** `probe.py` runs on the build machine against the
  public URLs, at lower concurrency.
- **Evidence:** `monitor.py` samples memory, swap, load and container usage every
  5 s and summarizes peaks and kernel OOM kills; evidence is copied off the VPS
  after every phase; preparation, load and phase timings are recorded.

The behaviors under test are unchanged. 1 GB support and VPS interface parity
are still unproven until the revised journey passes.
