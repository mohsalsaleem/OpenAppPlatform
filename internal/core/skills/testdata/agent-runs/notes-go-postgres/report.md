# notes (notes-go-postgres) on OAP: dry-run report

Application `notes`, domain `notes.139-180-132-23.sslip.io`. Proposed only; nothing applied.

## Processes and components

| Process | Found in | Component |
|---|---|---|
| HTTP API (`ListenAndServe(":"+port)`, all interfaces, default 8080; `GET /healthz` returns `ok` without a DB query) | `cmd/notes/main.go:42-47`, `:25`; README lines 5-6 | `web` (kind web, port 8080, health http `/healthz`, after `migrate`, uses `db`) |
| Migrations (applies `/app/migrations/*.sql` once each) | `cmd/migrate/main.go`; README lines 7-8 | `migrate` (kind job, uses `db`; `web` lists it in `after`). Not destructive: only `CREATE TABLE` (`migrations/001_notes.sql`) |
| Postgres 16 (pgx driver in `go.mod:5`; README line 3) | `go.mod`, README | `db` (kind data, `postgres:16.4-alpine`, 1 instance, volume `data: /var/lib/postgresql/data`) |

Images: `ghcr.io/example/notes:1.3.0` and `ghcr.io/example/notes-migrate:1.3.0`, public per README lines 10-13.

## Environment variables

| Variable | Read in | Treatment |
|---|---|---|
| `DATABASE_URL` | `cmd/notes/main.go:16`, `cmd/migrate/main.go:16` | secret `notes-database-url` (contains the DB password) on `web` and `migrate` |
| `SESSION_SECRET` | `cmd/notes/main.go:20` (required) | secret `notes-session-secret` on `web` |
| `PORT` | `cmd/notes/main.go:42` | plain, `8080` |
| `LOG_LEVEL` | `cmd/notes/main.go:46` (only logged) | plain, `info` |
| `POSTGRES_PASSWORD` (postgres image) | `db` component | secret `notes-db-password` |
| `POSTGRES_USER`, `POSTGRES_DB` (postgres image) | `db` component | plain, `notes` / `notes` |

`.env` in the repository holds real-looking development values for `DATABASE_URL` and `SESSION_SECRET`; they were not copied anywhere.

## Asks created (none answered yet)

| Id | Kind | Question (summary) |
|---|---|---|
| ask_9ss0ipnew8cO | secret `notes-db-password` | Choose the Postgres password for component db (user/db `notes`); same password goes into notes-database-url |
| ask_dX8aZFjgsNq1 | secret `notes-database-url` | Store `postgres://notes:PASSWORD@db.notes.oap.internal:5432/notes?sslmode=disable` with the password from notes-db-password |
| ask_XEmuQeD-IsNg | secret `notes-session-secret` | Generate a long random SESSION_SECRET (e.g. `openssl rand -hex 32`), not the .env value |
| ask_tmescLKal5Z8 | approval for prop_78ff26c8b33ddd9d856ae57145073654 | Approve creating notes at the domain (sensitive: domain change) |
| ask_aTbBpzB1hPaF | blocker (notice) | The repo commits a `.env` with dev credentials; consider removing it and rotating reused values |

## Proposals

1. `prop_78ff26c8b33ddd9d856ae57145073654` (spec `oap.yaml`): accepted as valid; verdict **needs approval** (`policy.sensitive_change`: "changes domains, which the envelope treats as sensitive"). Risk: sensitive. Plan: write desired state, start db, await ready, run migrate job, start web, await ready, add route, reload edge. Downtime: none expected. Exit code 3, as expected. Not applied.

## Files produced

- `oap.yaml`: the spec.
- `Dockerfile`, `Dockerfile.migrate`, `.dockerignore`: suggested for the repository (it has none); reference builds matching the README's images (alpine runtime, non-root, no secrets).

## Assumptions

- The README is right that both images are public and built with entrypoints `/app/notes` and `/app/migrate`, and the migrate image contains `/app/migrations`.
- The API image contains `wget` (as an alpine-based image would, like the suggested Dockerfile); otherwise the health check must switch to `mode: image` or be dropped.
- `sslmode=disable` is fine inside the server's private network.
- Memory: the briefing reports 950 MiB total with 0 MiB available, and `observe` shows no applications and no available-memory figure, so I read "0 available" as not reported rather than exhausted. Resources requested total 448 MiB (db 256, web 128, migrate 64); the owner should confirm it fits.
- One web instance: the app is stateless, but one is enough for a small 1-CPU host.
- Postgres pinned to `postgres:16.4-alpine` (README says Postgres 16); a digest would be better.
