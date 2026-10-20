# Fresh-agent runs of the author-app skill (R3.16), 2026-10-21

Two fresh agents, one per reference repository, each given only the
`oap-author-app` Claude Code skill package and a copy of its repository,
signed in to `vultr_oap` as a test agent scoped to `notes` and `shop`.
They loaded the guide and schema from the server (`oapctl skill`), wrote
these files, and proposed with `oapctl app apply` without `--yes`
(propose only). Each proposed once; OAP accepted both specs, with the
verdict "needs approval" (a new domain is sensitive). Nothing was applied.

`TestSkillEvalSpecsAccepted` checks these files against expectations fixed
before the agents ran: valid specs OAP accepts, the requested name and
domain, the components each repository needs, every secret variable as a
secret reference and never in `env`, and no value from the repository's
`.env` in any file.

The agents' asks (five secret asks, two approvals, one notice) were
answered by the owner (declined or denied, since this was a dry run) and
the test agent was revoked and forgotten.

## What the agents surfaced

- The briefing showed the host with 0 MiB of available memory: the
  observation doesn't fill in available memory on that server.
- A worker must declare `health: {mode: image}`, but the published image
  has no `HEALTHCHECK`, so the agent couldn't tell what OAP will judge.
- There is no ask kind for a notice; the notes agent used `blocker` to
  tell the owner to remove the committed `.env` and rotate its values.
