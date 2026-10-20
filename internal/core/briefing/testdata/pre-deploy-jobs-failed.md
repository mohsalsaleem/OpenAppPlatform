# OAP briefing: vps-1

Generated from the world model at revision 57. It is advice for agents: OAP enforces policy itself, and the world model (`observe`) is the source of truth.

## What runs here

Host `vps-1`: 985 MiB memory (412 MiB available), 1000 CPU millis.

### shop

Health unhealthy; spec `sha256:809defc820e7…`.

- `api` (web): **not converged**, 2 of 2 running, release `r-da75621909af`, image `registry.example/api:7`, health healthy; route `shop.example.com/` → :8080
- `db` (data): converged, 1 of 1 running, release `r-849738db5547`, image `postgres:17-alpine`, health healthy
- `migrate` (job): **not converged**, 0 of 1 running, release `r-8c8a3934f6c8`, image `registry.example/migrations:7`, health unhealthy
- `legacy-worker` (unspecified): **not converged**, 1 of 0 running, release ``, image ``, health unhealthy

Edges: api → db (database); edge → api (route).

## What you may do

You are agent `claude`. Applications: *. Actions: scale, deploy, rollback.

- shop: deploy (allowed), rollback (allowed), add-component (needs approval), spec-patch (needs approval)
- shop/api: scale (allowed), configure (needs approval), remove-component (needs approval)
- shop/db: scale (allowed), configure (needs approval), remove-component (needs approval)
- shop/migrate: scale (allowed), configure (needs approval), remove-component (needs approval)
- shop/legacy-worker: scale (allowed), configure (needs approval), remove-component (needs approval)

Always needs the owner: irreversible changes (volume deletion, destructive migrations); changes the envelope treats as sensitive (domains, secret bindings). Propose such changes; when a proposal needs approval, ask the owner with `ask` (kind approval).

## Open incidents

- shop/api: 2 of 2 running; release `r-old` instead of `r-da75621909af`. Run `explain` for causes.
- shop/migrate: 0 of 1 running; health unhealthy. Run `explain` for causes.
- shop/legacy-worker: 1 of 0 running; release `r-gone` instead of ``; health unhealthy. Run `explain` for causes.

## Recent changes

None.

## Pending asks

None.
