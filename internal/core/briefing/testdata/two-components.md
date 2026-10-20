# OAP briefing: vps-1

Generated from the world model at revision 42. It is advice for agents: OAP enforces policy itself, and the world model (`observe`) is the source of truth.

## What runs here

Host `vps-1`: 985 MiB memory (412 MiB available), 1000 CPU millis.

### two-components

Health healthy; spec `sha256:16538edcb988…`.

- `api` (web): converged, 1 of 1 running, release `r-0f14102c7145`, image `nginx:1.27-alpine`, health healthy
- `frontend` (web): converged, 1 of 1 running, release `r-0f14102c7145`, image `nginx:1.27-alpine`, health healthy

## What you may do

You are agent `claude`. Applications: *. Actions: scale, deploy, rollback.

- two-components: deploy (allowed), rollback (allowed), add-component (needs approval), spec-patch (needs approval)
- two-components/api: scale (allowed), configure (needs approval), remove-component (needs approval)
- two-components/frontend: scale (allowed), configure (needs approval), remove-component (needs approval)

Always needs the owner: irreversible changes (volume deletion, destructive migrations); changes the envelope treats as sensitive (domains, secret bindings). Propose such changes; when a proposal needs approval, ask the owner with `ask` (kind approval).

## Open incidents

None.

## Recent changes

None.

## Pending asks

None.
