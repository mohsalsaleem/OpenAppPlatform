# Open App Platform

OAP runs your applications on your own server and lets AI agents operate
them: deploy, scale, diagnose, roll back. The agents work inside limits you
set, and they ask you only when a decision is yours.

It is for one person running a few applications on a small server (1 GB is
enough) with agents doing most of the operating.

```sh
curl -fsSL https://raw.githubusercontent.com/mohsalsaleem/OpenAppPlatform/main/install.sh | sh
oapctl
```

`oapctl` walks you from a server's IP address and root password to OAP
running on it, an owner key on your machine, and OAP's SSH access removed.
[Getting started](docs/getting-started.md) has every step.

## How it works

- **One server, one binary.** `oapd` and Caddy run as containers on the
  server. Your applications run as containers next to them, behind Caddy,
  with HTTPS certificates obtained for you.
- **Agents describe what they want.** An application is an `oap.yaml`
  (components, routes, health checks, secret references). Agents `observe`
  the world, `propose` a change, `apply` it, `await` the outcome, `watch`
  events, `explain` a difference, and `ask` you. OAP plans and carries out
  every step.
- **You set the limits once.** Each agent is approved for named
  applications and kinds of change. A policy envelope decides which changes
  apply directly and which wait for you. You can pause or revoke any agent
  at once.
- **Credentials stay on the server.** Agents sign in by device login and
  never see secrets: you set them, and OAP passes them to the containers
  that reference them.
- **Every interface is the same API**: the CLI, MCP, Connect, gRPC and
  JSON over HTTPS.

## Documentation

- [Getting started](docs/getting-started.md): a server to a running app
- [Agents guide](docs/agents.md): signing an agent in, MCP, the skill and
  the briefing
- [Security model](docs/security.md): who can do what, and the known limits
- [Release notes](docs/release-notes/v0.2.0.md) and [roadmap](ROADMAP.md)
- [Architecture](docs/architecture/agent-native-rewrite.md) and
  [onboarding design](docs/architecture/onboarding.md)

## Status

v0.2.0 is the first release of this design, for early users. v0.1 (the
earlier design) is retired; its code is in Git history
before the v0.2.0 tag.

## License

See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
