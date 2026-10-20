# OAP briefing: vps-1

Generated from the world model at revision 43. It is advice for agents: OAP enforces policy itself, and the world model (`observe`) is the source of truth.

## What runs here

Host `vps-1`: 985 MiB memory (412 MiB available), 1000 CPU millis.

### topology-example

Health unhealthy; spec `sha256:566c4be03990…`.

- `web` (web): **not converged**, 1 of 1 running, release `r-bb461c12ea14`, image `example/web:2`, health healthy
- `worker` (worker): **not converged**, 0 of 1 running, release `r-b50a2ccf5e34`, image `example/worker:2`, health unhealthy

## What you may do

You are agent `claude`. Applications: *. Actions: scale, deploy, rollback.

- topology-example: deploy (allowed), rollback (allowed), add-component (needs approval), spec-patch (needs approval)
- topology-example/web: scale (allowed), configure (needs approval), remove-component (needs approval)
- topology-example/worker: scale (allowed), configure (needs approval), remove-component (needs approval)

Always needs the owner: irreversible changes (volume deletion, destructive migrations); changes the envelope treats as sensitive (domains, secret bindings). Propose such changes; when a proposal needs approval, ask the owner with `ask` (kind approval).

## Open incidents

- topology-example/web: 1 of 1 running; release `r-previous` instead of `r-bb461c12ea14`. Run `explain` for causes.
- topology-example/worker: 0 of 1 running; health unhealthy. Run `explain` for causes.

## Recent changes

None.

## Pending asks

None.
