# Handoff: v0.1 release journey on a 1 GB VPS

Give this prompt to the testing agent. Replace the placeholders.

---

You are testing Open App Platform (OAP) v0.1. Agents are OAP's users, so your job
is to prove an agent can manage a multi-component application end to end,
reliably, through each agent interface (CLI, REST API and MCP), on a **1 GB VPS**.

- Repository: `mohsalsaleem/openappplatform`, commit `<RC_SHA>`. Record it.
- Follow `tests/vps/release-journey/README.md` exactly: Phase A (prepare on the
  build machine, preflight, load, install), Phase B, the journey for `cli`, `rest`
  and `mcp` in that order, then Phase X.
- Build machine: `<your machine or CI runner>`. VPS: `<host / SSH details>`,
  platform `<linux/amd64 or linux/arm64>`.
- Base domain: `<base>`; `cli.`, `rest.` and `mcp.` records point at the VPS.
  Use `letsencrypt`. Only if DNS or ports block you, use `internal` and record
  that public HTTPS was not tested.
- Owner steps: perform only the steps marked **Owner**, with the owner credential
  only. Never use it in agent steps.

Rules that matter most:

- **Never build anything on the VPS.** No `docker build`, `go build` or `npm`
  there. Everything comes from the verified bundle (`bundle.py`).
- Keep `monitor.py` running from Phase A2 to X5. If available memory stays below
  100 MiB for over a minute, or SSH stops answering, stop and record it. Do not
  add swap or resize the server.
- Copy evidence off the VPS after every phase.
- In each run, use only that run's interface for OAP operations.
- Never edit the database, call undocumented endpoints, hand-edit Caddy or
  restart containers to make a step pass.
- If a result differs from **Expect**, stop that run, capture evidence and record
  a finding. After an uncertain result, reuse the same plan or idempotency key.
- Never print or paste credentials or the owner password.

Hand back:

1. `release-journey-cli.json`, `release-journey-rest.json`,
   `release-journey-mcp.json` and `parity.json`.
2. `prepare.json`, the VPS evidence directory and the build machine's `evidence/`
   (including probes and `monitor.jsonl`), as one archive.
3. A short summary: pass/fail per run, host health (lowest available memory, OOM
   kills, SSH), every finding with its step and evidence file, and any interface
   gaps.

Do not fix OAP code. If something blocks you, report it with evidence. Commit the
receipts and summary to `docs/` only if asked.
