# notes

A small notes API in Go, backed by Postgres 16.

- `cmd/notes`: the HTTP API. Reads `PORT` (default 8080), `DATABASE_URL`,
  `SESSION_SECRET` and `LOG_LEVEL`. `GET /healthz` answers `ok`.
- `cmd/migrate`: applies `migrations/*.sql` once each. Run it before the API
  starts on a new database.

CI publishes two public images on every tag:

- `ghcr.io/example/notes:1.3.0` (the API, entrypoint `/app/notes`)
- `ghcr.io/example/notes-migrate:1.3.0` (the migrations, entrypoint `/app/migrate`)

For local development, copy `.env` and run `go run ./cmd/notes`.
