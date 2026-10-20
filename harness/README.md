# Harness packages

Small packages that let agent harnesses use OAP. They hold no guidance of
their own: they point to what `oapd` serves (skills, schema, briefing), so
an agent always follows the version of the server it works with.

- `claude-code/skills/oap-author-app`: a Claude Code skill. Copy the
  directory into `~/.claude/skills/` (or a project's `.claude/skills/`).
