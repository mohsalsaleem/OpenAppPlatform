# shop (shop-node-worker) on OAP: dry-run report

## Processes and components

| Process | Found in | Component |
|---|---|---|
| Web server (express, `node src/server.js`, listens on `0.0.0.0:${PORT:-3000}`, `GET /health`) | package.json:7, src/server.js:13,26-27, docker-entrypoint.sh:3, README.md:6 | `web` (kind web, port 3000, health http `/health`, route `/` at shop.example.com) |
| Email worker (bullmq consumer of queue `emails`, `node src/worker.js`) | package.json:8, src/worker.js, docker-entrypoint.sh:2 (`ROLE=worker`), README.md:7-8 | `worker` (kind worker, same image, `ROLE=worker`, health `{mode: image}`) |
| Redis 7 (email queue, ioredis/bullmq) | README.md:3-4, package.json deps | `redis` (kind data, `redis:7.4-alpine`, 1 instance, volume `data:/data`); web and worker declare `uses: {redis: {type: queue}}` |

No migrations, no database, no files written to disk by the app (no volumes needed for web/worker).
Image: `ghcr.io/example/shop:2.1.0` (public, per README.md:10), one image for both processes, chosen by the plain variable `ROLE`.

## Environment variables

| Variable | Read in | Treatment | Value / secret name |
|---|---|---|---|
| `REDIS_URL` | src/server.js:6, src/worker.js:5 | plain (no password in it) | `redis://redis.shop.oap.internal:6379` |
| `STRIPE_SECRET_KEY` | src/server.js:8 | **secret** | `shop-stripe-secret-key` (web) |
| `PUBLIC_BASE_URL` | src/server.js:19-20 | plain | `https://shop.example.com` |
| `PORT` | src/server.js:26 | plain | `3000` |
| `ROLE` | docker-entrypoint.sh:2 | plain | `worker` (worker only) |
| `SMTP_HOST` | src/worker.js:7 | plain | `smtp.mailprovider.example` (from .env) |
| `SMTP_USER` | src/worker.js:9 | plain | `shop@example.com` (from .env) |
| `SMTP_PASSWORD` | src/worker.js:9 | **secret** | `shop-smtp-password` (worker) |

The repository's `.env` contains values for STRIPE_SECRET_KEY and SMTP_PASSWORD; they were not copied anywhere — only the names were used.

## Asks created

| Id | Kind | Question (summary) |
|---|---|---|
| `ask_FT85IQp4PIlT` | secret (`shop-stripe-secret-key`) | Stripe secret key for STRIPE_SECRET_KEY (Stripe dashboard -> Developers -> API keys, live key); store as shop-stripe-secret-key. |
| `ask_fl4Cngoi4N3F` | secret (`shop-smtp-password`) | SMTP password for the receipt mail account (SMTP_HOST/SMTP_USER taken from the repo; say if they differ); store as shop-smtp-password. |
| `ask_PaudhP-fhmR6` | approval (proposal `prop_d63723d2be2d949fbecfbf8606bd9617`) | Approve creating shop at shop.example.com with web, worker and redis; notes the host's 0 MiB available memory vs 576 MiB requested. |

None answered yet (dry run; not waited for).

## Proposals

1. `prop_d63723d2be2d949fbecfbf8606bd9617` (base revision 728), `oapctl app apply oap.yaml` without `--yes`, exit 3.
   - Verdict: **needs approval** (verdict 2), risk sensitive.
   - Reason: `policy.sensitive_change` — "changes domains, which the envelope treats as sensitive".
   - Plan: write desired state; start redis, await ready; start web, await ready, add route, reload edge; start worker, await ready. Downtime: none expected.
   - Not applied (dry run).

No proposal was rejected as invalid.

## Assumptions and notes

- The public image `ghcr.io/example/shop:2.1.0` exists and is pullable without credentials (README).
- Redis runs on the server as a `data` component without a password (the official image can't set one through env alone, and OAP has no per-component command); therefore `REDIS_URL` holds no credential and is plain. Assumes the internal network is reachable only by the server's own components. If the owner prefers a managed/password-protected Redis, `REDIS_URL` becomes a secret.
- `redis:7.4-alpine` chosen to match "Redis 7" in the README; persists to `/data` so queued emails survive restarts.
- SMTP_HOST and SMTP_USER taken from the repository's `.env` as plain values (a hostname and a username grant no access alone); the SMTP ask invites the owner to correct them. SMTP port 587 is hard-coded in src/worker.js:8.
- `PUBLIC_BASE_URL` set to `https://shop.example.com` (README.md:11) instead of the .env's localhost value.
- One instance each for web and worker to fit a small host; both are stateless and could scale to more.
- Health: web uses `wget` against `/health` (node:22-alpine has busybox wget). The published image has **no HEALTHCHECK**, so the worker's required `health: {mode: image}` has nothing to run. `Dockerfile` in this folder is a suggested replacement (multi-stage, pinned base, HEALTHCHECK for both roles) plus `.dockerignore` excluding `.env*`; it would need a new image tag, which was not requested.
- Memory: the briefing reports 950 MiB total and **0 MiB available**; the spec requests 576 MiB (redis 128, web 256, worker 192). OAP's verdict did not flag this, but the deployment may not fit; mentioned in the approval ask.
- Nothing was applied; no asks were answered or waited for.
