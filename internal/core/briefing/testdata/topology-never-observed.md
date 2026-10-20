# OAP briefing: this server

Generated from the world model at revision 1. It is advice for agents: OAP enforces policy itself, and the world model (`observe`) is the source of truth.

## What runs here

### topology-example

Health unknown; spec `sha256:566c4be03990…`.

- `web` (web): **not converged**, 0 of 1 running, release `r-bb461c12ea14`, image `example/web:2`, health unknown
- `worker` (worker): **not converged**, 0 of 1 running, release `r-b50a2ccf5e34`, image `example/worker:2`, health unknown

## What you may do

You are agent `claude`. Applications: *. Actions: scale, deploy, rollback.

- topology-example: deploy (allowed), rollback (allowed), add-component (needs approval), spec-patch (needs approval)
- topology-example/web: scale (allowed), configure (needs approval), remove-component (needs approval)
- topology-example/worker: scale (allowed), configure (needs approval), remove-component (needs approval)

Always needs the owner: irreversible changes (volume deletion, destructive migrations); changes the envelope treats as sensitive (domains, secret bindings). Propose such changes; when a proposal needs approval, ask the owner with `ask` (kind approval).

## Open incidents

- topology-example/web: 0 of 1 running; health unknown; observation stale; missing observation. Run `explain` for causes.
- topology-example/worker: 0 of 1 running; health unknown; observation stale; missing observation. Run `explain` for causes.

## Recent changes

None.

## Pending asks

None.
