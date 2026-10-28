# Agents guide

How an agent (Claude Code, Codex, Cursor or any MCP client) works with OAP.
The owner's side of each step is in [getting started](getting-started.md).

## Sign in

An agent signs in by device login, never with a copied token:

```sh
oapctl login https://oap.203-0-113-5.sslip.io --agent claude-laptop
```

It prints a code; the owner approves it with
`oapctl owner approve CODE --app APPS --may KINDS`. The session is saved on
that machine only. Later commands act as that agent when `OAP_AGENT` names
it:

```sh
export OAP_AGENT=claude-laptop
oapctl observe
```

Without `OAP_AGENT`, `oapctl` on the owner's machine acts as the owner. An
agent's harness should always set it.

## Connect over MCP

`oapctl mcp` serves OAP's tools over MCP on stdin and stdout, as the agent
`OAP_AGENT` names. For Claude Code:

```sh
claude mcp add oap -e OAP_AGENT=claude-laptop -- oapctl mcp
```

Other MCP clients take the same command and environment variable. oapd
also serves MCP over HTTPS at `/mcp`, with the agent's token as a bearer
token.

The tools are the seven primitives:

| Tool | What it does |
|---|---|
| `observe` | The world: applications, components, instances, routes, health |
| `propose` | A change (create, deploy, scale, rollback, configure, add or remove a component, spec patch). OAP returns a plan and a verdict: allowed, needs approval, or refused |
| `apply` | Carry out a proposal. Applying the same proposal twice is safe |
| `await` | Follow an operation to its outcome |
| `watch` | Events as they happen |
| `explain` | Why an application differs from its desired state, with likely causes |
| `ask` | Ask the owner: an approval for a proposal, a secret's value, a decision, or a blocker |

The CLI (`oapctl app apply`, `oapctl app deploy`, `oapctl app scale`,
`oapctl apply`, `oapctl ask`, ...), Connect, gRPC and JSON over HTTPS reach
the same API, and a change made through one is the same change through any
other. Every CLI command takes `--json`, or `--events` for a stream of JSON
lines.

## Read what the server serves

The server serves its own guidance, so an agent always follows the version
it works with:

| What | MCP | CLI | HTTPS (with the token) |
|---|---|---|---|
| Briefing: what runs, what this agent may do, the owner's house rules | resource `oap://briefing` | `oapctl briefing` | `/.well-known/oap/briefing.md` |
| Skill for putting an application on OAP | prompt `author-app`, resource `oap://skills/author-app` | `oapctl skill author-app` | `/.well-known/oap/skills/author-app.md` |
| Schema of `oap.yaml` | resource `oap://schema/application` | `oapctl skill schema` | `/.well-known/oap/schema/application.json` |

An agent should read the briefing at the start of each session.

For Claude Code, [harness/claude-code](../harness/claude-code) has a small
skill that points to these. Copy `skills/oap-author-app` into
`~/.claude/skills/`.

## Working within the envelope

- An agent acts only on the applications, and proposes only the kinds of
  change, the owner approved it for. Anything else is refused.
- A proposal's verdict says what happens next. **Allowed**: apply it.
  **Needs approval**: ask the owner with
  `oapctl ask --kind approval --proposal ID "question" --wait`, then apply
  it once approved. **Refused**: don't work around it; tell the owner.
- Agents never see secret values. A spec refers to secrets by name; when
  one is missing, ask the owner with `--kind secret`, and the owner stores
  it with `oapctl owner secret set`.
- The owner can pause or revoke an agent at any time. Its calls are then
  refused at once.
