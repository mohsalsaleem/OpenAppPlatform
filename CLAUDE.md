# Working on the OAP rewrite

The rewrite is planned in [docs/architecture/rewrite-plan.md](docs/architecture/rewrite-plan.md)
and designed in [docs/architecture/agent-native-rewrite.md](docs/architecture/agent-native-rewrite.md).
Read both before starting a task. The plan's guardrails are binding; this file
is the short version.

- Work on one task from the plan at a time, in order. Name it in every commit
  (`Task: R1.4`).
- Do only what the task says. Anything else needs a pull request to the plan,
  approved by the owner, before code relies on it.
- Don't edit the frozen old tree (listed in the plan) except for security fixes
  the owner approves.
- Never skip, disable, weaken or delete a test or acceptance check to get green.
  Bug fixes start with a failing test.
- Keep the layer rules: `internal/core` has no I/O, time or randomness;
  dependencies point downward only. `tests/arch` enforces them.
- Run `make rewrite-check` before every push; it must pass. Set `REWRITE_BASE`
  if the base branch isn't `origin/main`.
- No new dependencies outside the plan's allowlist without owner approval.
- Never commit, log or print secrets or credentials.
- Record each task's acceptance evidence in the plan's status table, and report
  failures as failures.
- Stop and ask the owner when acceptance can't be met as written, scope must
  grow, a listed decision is needed, or a security or data-loss question comes up.
- On a server: touch only OAP-labelled resources, don't build there unless the
  task says so, don't hand-edit databases or Caddy, and ask before anything
  destructive.
