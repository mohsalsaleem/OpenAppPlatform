# Getting started

From a server's IP address and root password to an application running on
it, managed by an agent. About fifteen minutes.

## What you need

- A server: Ubuntu 22.04, 24.04 or 26.04, or Debian 12 or 13, with at least
  1 GB of memory. A fresh VPS is fine; one already in use is fine too, as
  OAP doesn't touch what it didn't create.
- Its IP address and a username and password that can become root (often
  `root`, from your provider's email).
- A Mac or Linux machine for `oapctl`.

## 1. Install oapctl

```sh
curl -fsSL https://raw.githubusercontent.com/mohsalsaleem/OpenAppPlatform/main/install.sh | sh
```

It downloads `oapctl` for your machine from the latest release, refuses it
unless it matches the release's `SHA256SUMS`, and installs it in
`~/.local/bin` (no sudo). `OAP_VERSION=v0.2.0` picks a release.

## 2. Put OAP on the server

Run `oapctl` with no arguments at any point: it shows where onboarding
stands and the next command. The steps are:

| Step | Command | What happens |
|---|---|---|
| connect | `oapctl host connect root@203.0.113.5` | You check the host key against your provider's console and type the password once. It installs OAP's own SSH key and is never stored or shown to an agent |
| check | `oapctl host check` | A read-only look at the server: OS, memory, disk, Docker, firewall, ports. Each problem comes with a fix |
| fix | `oapctl host fix` | Each fix shows the exact commands it will run, and runs only if you approve it. Fixes that change security, such as installing Docker or opening ports 80 and 443, are marked sensitive. Each is recorded so it can be undone |
| install | `oapctl host install` | The server pulls the oapd image this release pins by digest, and starts oapd and Caddy as containers |
| owner | `oapctl owner enroll` | Creates your owner key on this machine. Owner commands then sign in over HTTPS with it |
| hand off | `oapctl host handoff` | Removes OAP's SSH key from the server and checks it's refused. From now on you and your agents work only through OAP |

After hand-off, oapd answers at `https://oap.203-0-113-5.sslip.io` (your
server's address with dashes), with a real certificate. If you ever need
SSH access for OAP again, for example to upgrade, use
`oapctl host connect ADDR --break-glass`: it grants temporary access.

`oapctl host uninstall` reverses everything OAP did, one approved step at a
time. Docker stays, as removing it could break other software.

## 3. Sign in an agent

On the machine where your agent runs:

```sh
oapctl login https://oap.203-0-113-5.sslip.io --agent claude-laptop
```

It prints a code and waits. Approve it from your machine, choosing what the
agent may act on:

```sh
oapctl owner logins                       # the waiting logins and their codes
oapctl owner approve CODE --app shop --may scale,deploy,rollback,configure
```

`--app` takes application names (comma-separated, or `*`). `--may` takes
the kinds of change the agent may propose: `scale`, `deploy`, `rollback`,
`configure`, `add-component`, `remove-component` and `spec-patch`. The
agent's key is trusted by default, so its later sessions sign in without
asking; `--trust=false` makes you approve each one.

Then connect the agent's harness to OAP: see the [agents guide](agents.md).

## 4. Your first application

Ask your agent to put an application on OAP. It reads the server's skill
(`oapctl skill`), writes an `oap.yaml`, and proposes it. A small one:

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

```sh
OAP_AGENT=claude-laptop oapctl app apply oap.yaml
```

Creating an application adds a domain, which the default envelope treats as
sensitive, so the proposal needs your approval. The agent asks for it
(`oapctl ask --kind approval --proposal ID "Create hello?" --wait`; `apply`
prints this command), and you answer:

```sh
oapctl owner asks
oapctl owner answer ASK_ID approve      # or deny
```

The agent then applies the approved proposal (`oapctl apply PROPOSAL_ID`).
OAP carries out the plan (start, wait until healthy, route, reload the
edge), and the application serves at `https://hello.203-0-113-5.sslip.io`.

## Day to day

| You want to | Command |
|---|---|
| See everything | `oapctl observe` |
| Know why an app isn't as desired | `oapctl explain APP` |
| Follow events | `oapctl watch` |
| Answer what agents ask | `oapctl owner asks`, `oapctl owner answer ID ANSWER` |
| Store a secret an app references | `oapctl owner secret set NAME` (the value is never shown) |
| Stop an agent at once | `oapctl owner pause NAME`, or `oapctl owner revoke NAME` |
| Set house rules for agents | `oapctl owner briefing --file rules.md` |

`oapctl` with no arguments, and every command's `--json`, give the same
information in a form agents read.
