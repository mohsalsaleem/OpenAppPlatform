# vultr_oap, 2026-10-22/28: R5.3, the release journey on the owner's server

Released v0.2.0-rc.2 installed from scratch on the owner's 1 GB server,
the release journey run against it before hand-off, Nisha Shipping
redeployed, and the server handed off. Times are UTC. The run followed
[the release plan](../../architecture/release-plan.md#r53-on-vultr_oap).

## Server

`vultr_oap`: Ubuntu 26.04.1 LTS, x86_64, 1 vCPU, 950 MiB memory, 2.4 GiB
swap, 13.2 GiB disk free.

## Result

**Passed** on the second run (`receipt-run-r2.json`, 587 s), with the
journey's own test (`TestReleaseJourney` with `OAP_JOURNEY_SERVER`; the
runner is `run-journey.sh`). Three agents signed in by device login over
oapd's public HTTPS and the owner approved each.

| Run | Result |
|---|---|
| CLI, full | Created, deployed with a migration first, scaled 1 → 3 → 1 with 0 failed requests, a failing migration blocked its rollout, a broken release didn't complete and desired state was restored, corrected to v2, rolled back (the migration skipped), the volume's data kept, a replay of the same operation was idempotent, and a deploy that needs approval was proposed rather than applied |
| Connect JSON, smoke | Passed; another agent's application is refused (S6) |
| MCP, smoke | Passed; another agent's application is refused (S6) |
| Coexistence (X1) | Each application served on its own HTTPS host name with a real certificate; an unknown name got none of them |
| oapd restart (X2) | oapd stopped for 30 s under traffic: 659 requests to the three apps, 0 failed; the CLI worked again after it came back |
| Host (X3) | Memory peak 452 MiB, at least 498 MiB available throughout, `oom_kill 0` |

`logs/10-watch.txt` is `oapctl watch` across both runs: 12 operations, 7
achieved and 5 not achieved (the first run's three failed creates and
the second run's two deliberate failures).

## Steps

| When | Step | Log (in `logs/`) |
|---|---|---|
| 2026-10-21 13:47 | The owner installed rc.1's `oapctl` with `install.sh` | `01-install-oapctl.txt` |
| 2026-10-22 14:37 | Nisha Shipping's image saved to `/root` first (sha256 `1d9811…6675`); then `oapctl host uninstall` removed the old installation, every step approved; Docker stays, as it has no undo. The site is down from here | `02-uninstall.txt` |
| 2026-10-22 18:02 | `host connect`: the owner confirmed the host key and typed the root password once | `03-connect.txt` |
| 2026-10-22 18:03 | Check: ufw blocks 80 and 443; the owner approved `open-web-ports` | `04-fix.txt` |
| 2026-10-22 18:03 | `host install` refused: it offered this Mac's arm64 copy of the image instead of letting the server pull it (finding 1) | `05-install.txt` |
| 2026-10-23 10:47 | v0.2.0-rc.2 released with the fix (`c18a6ac`) | `06-release-rc2.txt`, `07-install-oapctl-rc2.txt` |
| 2026-10-26 16:16 | `host install` (rc.2): the first attempt couldn't pull, as the GHCR package was private (finding 2); once the owner made it public, the server pulled the pinned digest and oapd and Caddy started | `08-install-rc2.txt` |
| 2026-10-26 16:17 | `oapctl owner enroll` | `09-owner-enroll.txt` |
| 2026-10-26 16:18 | Journey, run 1: **failed**. The fixture image's base layer was this Mac's arm64 busybox, so its health check couldn't run on the server; and the harness's `*.journey.test` names don't resolve on a real server | `10-journey-r1.txt`, `receipt-run-r1.json` |
| 2026-10-26 16:24 | Journey, run 2: **passed**, after the harness fixes below | `10-journey-r2.txt`, `receipt-run-r2.json` |
| 2026-10-26 16:35 | Nisha Shipping redeployed through OAP as the owner, from its saved image and recorded spec; www.nishashipping.com serves again | `11-nisha-redeploy.txt` |
| 2026-10-26 16:36 | Host snapshot before hand-off: containers, memory, oapd warnings (none), the host manifest | `12-host-before-handoff.txt` |
| 2026-10-28 07:17 | `oapctl host handoff`: OAP's SSH key removed; independently, the server refuses it (`Permission denied`); owner commands work over HTTPS; Nisha returns 200 | `13-handoff.txt`, `14-key-refused.txt`, `15-owner-https.txt` |

Nisha Shipping was down from 2026-10-22 14:37 to 2026-10-26 16:35, the
window the plan allowed between uninstall and redeploy, stretched by
rc.2 and the GHCR package.

## Harness changes for a real server

Committed with this pack, in `cmd/oapctl`:
- Applications and agents take a run suffix (`OAP_JOURNEY_RUN`), so a
  rerun doesn't meet an earlier run's applications.
- Host names come from `OAP_JOURNEY_DOMAIN` (the server's sslip.io name)
  and are requested over HTTPS, waiting for Caddy's certificate.
- The fixture is built with buildx for the server's platform, and the
  test checks the image's architecture.

## Findings

1. `host install` shipped this Mac's copy of a released image instead of
   letting the server pull it. Fixed in rc.2 (`c18a6ac`).
2. The GHCR package was private, so servers couldn't pull anonymously. The
   owner made it public; R5.6's evidence was corrected (`9385169`).
3. The uninstall confirmation asks the owner to type the server address
   but doesn't show the exact text to type.
4. The spec's `ingress.https` field is ignored: with TLS, every host
   redirects to HTTPS.
5. No component can be scaled to 0, and no application removed. The
   journey's 18 containers (`journey-*-r2` and the first run's three `kv`
   instances) stay running; the first run's applications show as
   unhealthy in `observe`. Owner decision, 2026-10-28: hand off anyway, and
   clear them through OAP once it can remove an application.
6. A failed *create* leaves the application in desired state.
7. OAP saves no log of onboarding runs, and has no `oapctl logs`; this
   pack's logs were captured with `script` around each command.
8. The check's advice says hand-off offers to turn off SSH password login;
   hand-off only advises it.

Findings 3 to 8 are proposed as plan changes.
