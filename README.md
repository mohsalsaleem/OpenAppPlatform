# Open App Platform

OAP runs your applications on your own server and lets AI agents operate
them: deploy, scale, diagnose and roll back. Agents work inside limits you
set, and ask you only when a decision is yours.

It is built for one person running a few applications on a small server,
with agents doing most of the operating.

## Get started

You need:

- a computer running macOS or Linux, for `oapctl`;
- a server running Ubuntu 22.04, 24.04 or 26.04, or Debian 12 or 13, with
  at least 1 GB of memory, and its root password.

```sh
curl -fsSL https://raw.githubusercontent.com/mohsalsaleem/OpenAppPlatform/main/install.sh | sh
```

```sh
oapctl
```

`oapctl` checks the server and shows each fix before it runs it, with your
approval. It installs OAP and gives your machine an owner key. Then it
removes OAP's SSH access, leaving the server in your hands.
[Getting started](docs/getting-started.md) has every step.

## What an application looks like

An agent describes an application in an `oap.yaml`, and OAP works out each
step to get there:

```yaml
api_version: oap.dev/v1
kind: Application
name: hello
environment: production
ingress:
  domain: hello.203-0-113-5.sslip.io
  routes:
    /:
      component: web
components:
  web:
    kind: web
    image: nginxdemos/hello:0.4
    port: 80
    instances: 1
    health: {mode: http, path: /}
```

## How it works

![OAP architecture](docs/images/rewrite-c4.svg)

- **Everything runs in Docker on your server.** `oapd` and Caddy run as
  containers. Your applications run beside them, behind Caddy, which gets
  HTTPS certificates for every domain.
- **Agents use seven verbs.** They `observe` the world, `propose` a change,
  `apply` it, `await` the outcome, `watch` events, `explain` a difference
  and `ask` you. OAP plans and runs the steps: deploys without dropped
  requests, scaling, migrations and rollback.
- **You set the limits once.** You approve each agent for named
  applications and kinds of change. A policy envelope decides which changes
  apply directly and which wait for your approval. You can pause or revoke
  any agent at once.
- **Secrets stay on the server.** Agents sign in by device login and never
  see secrets. You set them, and OAP passes them only to the containers
  that reference them.
- **One API.** The CLI, MCP, Connect, gRPC and JSON over HTTPS are the same
  API.
- **Extensions run out of process**, with only the access you grant and a
  memory cap. They build images on the server or use a registry, and
  supply facts about a change from GitHub.

## Status

v0.2.0 is the first release of this design, for early users. Its main
limits are:

- one server per installation;
- an application can't be removed yet, and a component can't be scaled to
  0 (planned for v0.2.1);
- releases aren't signed.

All of them are in the [release notes](docs/release-notes/v0.2.0.md) and
the [security model](docs/security.md#known-limitations). What comes next
is in the [roadmap](ROADMAP.md).

v0.1, the earlier design, is retired, and nothing migrates from it. Its
last code is at
[`4020bf4`](https://github.com/mohsalsaleem/OpenAppPlatform/tree/4020bf4912936b1ef119d0b315d7b78abca3c606).

## Documentation

- [Getting started](docs/getting-started.md): from a server to a running
  application
- [Agents guide](docs/agents.md): signing in an agent, MCP, the skill and
  the briefing
- [Security model](docs/security.md): who can do what, and the known limits
- [Architecture](docs/architecture/agent-native-rewrite.md) and the
  [onboarding design](docs/architecture/onboarding.md)
- [All documentation](docs/README.md)

## Building from source

With Go 1.26:

```sh
make build
```

```sh
make test
```

`make build` puts `oapctl` and `oapd` in `bin/`.

## License

MIT. See [LICENSE](LICENSE) and
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
