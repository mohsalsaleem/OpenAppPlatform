# Onboarding: from a server password to a running OAP

Status: **accepted** by the owner (2026-10-19). Tasks R3.10–R3.14 in the
[rewrite plan](rewrite-plan.md).

This replaces the bootstrap half of [zero to live](../zero-to-live.md) and the
Python and Compose installer in [headless bootstrap](../headless-bootstrap.md)
for the rewrite.

## Starting point

What an owner has when they start, usually from a VPS provider's email:

- the server's IP address,
- a username (often `root`),
- a password.

They have an agent (for example Claude Code) on their own machine, and `oap`
installed there. Nothing is installed on the server yet, and the server may
be old, small or already in use.

## Principles

1. **The password is typed once, by the owner, and never reaches the agent or
   OAP.** It is only used to install a bootstrap key. The agent never
   receives it, and neither does a file, an argument or the agent's
   context.
2. **Everything OAP runs is a container**, including Caddy and later the
   builder. The host needs Docker Engine and nothing else: no Python, no
   Compose, no Go and no binaries in `/opt`.
3. **OAP checks and the agent proposes; the owner approves every change to
   the host.** Each prerequisite check reports a typed finding with a remedy,
   and each fix is a step with the exact commands, a risk tier and an undo
   when one exists. Nothing changes on the host until the owner has approved
   it.
4. **Every host change is recorded** in a host manifest, so `oap host
   uninstall` can reverse exactly what onboarding did and nothing else.
5. **Host access ends with onboarding.** The bootstrap key is removed and
   reported as removed. After that the agent holds only an OAP credential
   ([identity](agent-native-rewrite.md#8-identity-and-access)).
6. **Never touch what OAP didn't create.** Other containers, services and
   files are reported, not changed. If one blocks onboarding (for example,
   something is already on port 80), the owner decides what happens to it.

## The flow

```
owner + agent (laptop)                      server
──────────────────────                      ──────
1 connect   oap host connect root@203.0.113.5
            owner checks the host key fingerprint
            owner types the password on the TTY  ──►  bootstrap key installed
2 check     oap host check (read-only probe)     ──►  JSON findings
3 fix       agent proposes fixes; owner approves ──►  approved fixes applied, manifest written
4 install   oap host install                     ──►  oapd + Caddy containers
5 owner     one-time owner login on oapd (R3.6)
6 hand off  bootstrap key removed and verified gone; agent signs in with device login
```

## First launch

`oap` with no arguments never prints a usage dump. It answers two questions:
**where am I in the journey, and what happens next, by whom?** The same
journey serves two kinds of caller:

- **The owner at a terminal** (stdin and stdout are a TTY) gets a guided,
  resumable walkthrough.
- **An agent** (no TTY, or `--json`) gets the same state as JSON, including
  which steps only the owner can do and exactly what to ask them.

### The journey state

The journey lives on the laptop in `~/.config/oap/journey.json` (0600, in a
0700 directory), one entry per server. Every step is idempotent, so running
`oap` again resumes where the journey stopped, whether after a crash, a
reinstall or the next day.

| State | Reached when | Next step | Who |
|---|---|---|---|
| `new` | No server known | Connect a server | Owner (password, fingerprint) |
| `connected` | Bootstrap key works | Check the server | Agent or owner |
| `checked` | Check has run | Approve fixes, or reinstall the server if a check is a stop | Owner approves, agent proposes |
| `ready` | No blocking findings | Install OAP | Agent or owner |
| `installed` | `oapd` and Caddy healthy | Create the owner account | Owner |
| `owned` | Owner account exists | Connect an agent (device login) | Agent starts, owner approves |
| `handed-off` | Bootstrap key removed and verified | Deploy a first app | Agent |

Once every server is `handed-off`, `oap` with no arguments shows the day-to-day
digest instead: servers, apps, pending asks and anything stale.

### The owner's first launch

```
$ oap
OAP runs your apps on your own server, operated by your AI agent.
You don't have a server connected yet. Setup takes about 10 minutes and needs:
the server's IP address, its username and its password.

  1 connect   ← you are here
  2 check     3 fix   4 install   5 owner account   6 hand off

Server address: 203.0.113.5
Username [root]:
Host key: ED25519 SHA256:3f9c…a1  Does it match your provider's console? [y/N] y
Password for root@203.0.113.5 (used once to install a key, never stored): ••••
✓ Connected with an OAP bootstrap key (your password is no longer needed)

Checking the server (read-only)…
✗ Ubuntu 18.04 isn't supported: it no longer gets security updates.
  Reinstall it with Ubuntu 26.04 from your provider's console, then run `oap` again.
! No swap on a 1 GB server. Fix: add a 1 GiB swap file (routine, undoable).
· Also on this server, left alone: container `tura`, MySQL on 127.0.0.1:3306.

Stopped at step 2: the server needs a reinstall. Run `oap` again afterwards.
```

The rules behind it:

- **One question at a time, with a default** where one is safe.
- **Each owner-only step says why** it is owner-only ("your password",
  "this deletes data").
- **Stops are explicit.** A stop explains what blocks the journey, what the
  owner needs to do outside OAP, and that `oap` resumes afterwards.
- **Nothing is lost** if the owner presses Ctrl-C: the journey keeps
  the last completed step.

### An agent's first launch

Usually the owner tells their agent "host this app on my server". The agent
installs `oap` and runs it:

```
$ oap --json
{"state": "new",
 "next": {"step": "connect", "by": "owner",
          "why": "Connecting needs the server password, which agents must not handle.",
          "ask_owner": "Please run `oap` in a terminal on this machine and follow the steps. Tell me when it says Connected.",
          "then": "oap --json"},
 "journey": ["connect", "check", "fix", "install", "owner", "hand-off"]}
```

The agent relays `ask_owner` and runs `oap --json` again later. Steps the agent
may run itself (`check`, `install` once fixes are approved, and starting the
device login) carry `"by": "agent"` and a `command`. The exit codes are 0
(done, or the next step is the agent's), 3 (waiting for the owner) and 4
(stopped: something outside OAP must change first), so an agent can branch
without parsing prose. Until R3.12–R3.14 land, a step that isn't built yet
exits 5 and names the task that delivers it.

On a terminal, `oapctl` draws the journey with Charm's Bubble Tea and Lip
Gloss: one bordered panel per server, key-press prompts, and a spinner with
the elapsed time and the last lines of remote output while slow steps run.
`OAP_PLAIN=1` (or `TERM=dumb`) prints plain lines instead; agents always get
JSON.

`oapctl` (the CLI's working name) implements this as of R3.0, with
`host connect` (R3.10) and `host check` (R3.11). The probe is
`internal/core/host/probe.sh`.

### 1. Connect

`oap host connect root@IP` creates a bootstrap key pair in OAP's own
directory on the laptop (`~/.config/oap/hosts/<ip>/`), not in `~/.ssh`.

- **Host key:** the first connection shows the server's fingerprint and asks
  the owner to compare it with the provider's console. OAP pins it in its own
  `known_hosts`, never in the user's.
- **Password:** `oap` never reads it. It runs the system `ssh` client, which
  asks for the password on the controlling terminal (`/dev/tty`), and
  `connect` refuses to start without a terminal. An agent driving `oap` can't
  supply the password and never sees it. One SSH session appends the bootstrap public key to `authorized_keys`, tagged
  `oap-bootstrap <date>`, and then closes. The password isn't stored.
- **No `~/.ssh/config` edits.** v0.1 added a `Host` entry without an
  `IdentityFile`. The agent's first SSH attempt on 2026-10-19 failed on it.
  OAP passes its key and pinned host key explicitly on each connection.

If the provider gave a key rather than a password, `connect --identity
<file>` skips the password and still adds a separate bootstrap key, so the
owner's own key is never the one that gets removed.

### 2. Check (read-only)

`oap host check` runs a POSIX `sh` probe over the bootstrap key. The probe
changes nothing. It returns findings as JSON, each with a `why` code, the
observed value, the requirement and a remedy, using the same shape as API
errors.

| Check | Requirement | Example `why` |
|---|---|---|
| OS and version | A supported, maintained release (Ubuntu 22.04, 24.04 or 26.04; Debian 12 or 13) | `host.os.unsupported` |
| Architecture | amd64 or arm64 | `host.arch.unsupported` |
| Memory and swap | 1 GB or more; swap on hosts under 2 GB | `host.swap.missing` |
| Disk | 5 GB free or more | `host.disk.low` |
| Docker Engine | Installed through a supported package source, version ≥ the minimum, daemon running, cgroup v2 preferred | `host.docker.missing`, `host.docker.unmanaged` |
| Ports | 80 and 443 free, or held by OAP's own Caddy | `host.port.busy` (names the process) |
| Firewall | 22, 80 and 443 reachable; reported, not changed by default | `host.firewall.blocks` |
| Time | Clock synchronized (TLS and token expiry depend on it) | `host.time.unsynced` |
| Existing OAP | A previous install, by labels and manifest | `host.oap.present` |
| Other workloads | Containers and services OAP didn't create, listed | `host.foreign.present` (information only) |
| SSH hardening | Password login and root login state | `host.ssh.password_login` (advice) |

The check also reports the host's size, which sets defaults such as
building on the server or not (zero to live, "Builds on small servers").

### 3. Fix (with permission)

For each finding that blocks onboarding, the agent proposes a fix. A fix is
a host step with:

- the exact commands it will run,
- a risk tier: **routine** (for example adding a swap file or enabling time
  sync), **sensitive** (for example installing or upgrading Docker, which
  restarts its containers) or **destructive** (for example stopping a
  service that holds port 80),
- what it records in the manifest, and its undo.

The owner approves fixes on the TTY. A routine fix can be approved as a
group; sensitive and destructive fixes are approved one at a time. Some
findings have no automatic fix and are reported as stops: an unsupported OS
needs a reinstall or a new server, and OAP doesn't upgrade an OS in place.

Approvals use the same owner-present TTY as the password, so an agent can
propose fixes but can't approve them.

`oap host fix` runs each approved fix over the bootstrap key, appends it to
the host manifest (`/var/lib/oap/host-manifest.jsonl`) and checks the server
again. `oap host undo FIX` reverses a fix the manifest shows OAP applied.
`oap host fix --json` shows an agent the proposal, with every command, and
applies nothing.

#### Docker on servers that already have it

Real servers often have Docker already, installed in ways OAP didn't
choose. OAP judges Docker by what it needs: Docker Engine 24 or later,
running as root, answering its API, with Docker's own volume, health-check
and event behavior. It changes Docker only through the package manager
that owns it (owner decision, 2026-10-19).

| Found | OAP does |
|---|---|
| No Docker | Proposes installing Docker Engine from Docker's apt repository (sensitive) |
| Docker from a package (`docker-ce` or the distribution's `docker.io`), 24 or later | Uses it |
| Docker from a package, older than 24 | Proposes upgrading it through the same package (`apt-get install --only-upgrade`). The fix lists every running container that restarts; if any isn't OAP's, the fix is destructive and approved on its own |
| Docker no package manages (a static binary or a custom build), 24 or later | Uses it, with advice that it gets no security updates. Never replaced automatically |
| The same, older than 24 | Stop: upgrade it the way it was installed, or remove it so OAP can install Docker Engine |
| Snap Docker | Stop: the snap's confinement blocks the socket OAP's Caddy shares |
| Podman's `docker` command | Stop: its compatibility differs in volumes, health checks and events |
| Rootless Docker | Stop: it can't publish ports 80 and 443 for Caddy |
| A daemon that doesn't answer | Stop, with the commands to diagnose it |

### 4. Install

`oap host install` uses Docker only:

1. Pull the `oapd` image by digest and verify it.
2. Create `oap-system` (a network), `oap-data` (a volume holding SQLite, the
   desired-state Git repository and the encrypted secret store) and the
   secret store key. The key is shown to the owner once, for their backup.
3. Run `oapd` with the Docker socket. All OAP resources carry the system
   label `io.openappplatform.system=true`.
4. `oapd` starts its own Caddy container through the Docker runtime adapter
   (R2.9), with the admin API on a socket shared only with `oapd`, ports 80
   and 443, and a reload grace period. From then on Caddy is an ordinary
   OAP-managed system component: observed, health-checked and restarted like
   any other.
5. `oapd` gets a hostname, either the owner's domain or an automatic
   `oap.<ip>.sslip.io`, and waits for its TLS certificate.

The install is idempotent. Running it again converges to the same state,
and an upgrade is the same command with a newer digest.

### 5. Owner login

The owner opens the one-time setup link printed by `install` over HTTPS,
or over an SSH tunnel when there is no hostname yet. There they create the
owner account (R3.6). The link expires in 15 minutes and works once.

### 6. Hand off

1. The agent signs in with a device login (`oap login https://oap.<ip>.sslip.io`).
   The owner approves it with a scope and an envelope.
2. `oap host connect` removes the bootstrap key line it added, reconnects to
   prove the key no longer works, and records both in the manifest.
3. OAP offers, without applying it, to turn off SSH password login.
   This is a sensitive step: it runs only after the owner confirms their
   own key works, to avoid locking them out.

From here the agent works only through OAP. Getting host access back is
break-glass: the owner runs `oap host connect` again, which is recorded and
time-limited.

## Uninstall

`oap host uninstall` reads the manifest and reverses it in the reverse
order: application containers and volumes (after the owner confirms the
data loss), then OAP's Caddy, `oapd`, the network, the volume and the image.
Then come the host fixes that have an undo, each one shown before it runs.
Anything without OAP labels or a manifest entry stays.

## Worked example: `vultr_oap`, 2026-10-19

After the v0.1 install was torn down, a check would report:

| Finding | Observed | Proposed |
|---|---|---|
| `host.os.unsupported` | Ubuntu 18.04.2 LTS (standard support ended May 2023), kernel 4.15 | Stop: reinstall with Ubuntu 26.04 from the provider's console |
| `host.docker.unmanaged` | Docker 27.5.1 from a static tarball in `/opt/docker-27.5.1`, started by a systemd drop-in `oap-test-engine.conf` that v0.1 testing added | After the reinstall, install Docker from the official apt repository (sensitive) |
| `host.swap.missing` | 985 MiB memory, no swap | Add a 1 GiB swap file (routine; undo removes it) |
| `host.foreign.present` | `tura` container (stopped), MySQL on 127.0.0.1:3306 | Information only; left alone |
| `host.ssh.password_login` | `PermitRootLogin yes`, password login on | Offer at hand-off |

The OS finding means the reference server should be reinstalled before the
release journey (R5.3) relies on it. Until then it can serve as a test
server for the adapter contract suites (R2.9), which only need Docker.

## Plan change

These are tasks in phase 3, after R3.6, because install needs the
owner login and hand-off needs device login:

| ID | Task | Acceptance |
|---|---|---|
| R3.10 | `oap host connect`: bootstrap key, pinned host key, password from the TTY only, break-glass | A test drives `connect` without a TTY and the password is refused; the key is in `authorized_keys` with its tag; `~/.ssh` is untouched |
| R3.11 | `oap host check`: read-only probe, typed findings | Golden findings for fixture hosts (supported, old OS, no swap, port busy, foreign workloads, existing OAP); the probe makes no writes (checked by a read-only filesystem in the test) |
| R3.12 | Host fixes as approved steps, with a manifest and undo | Each fix's apply then undo returns the fixture host to its starting state; an unapproved fix never runs; destructive fixes can't be grouped |
| R3.13 | `oap host install`: `oapd` and Caddy as containers, idempotent, upgrade by digest | Running install twice changes nothing the second time; Caddy is observed as an OAP system component; a fresh 1 GB host stays inside the footprint budget |
| R3.14 | Hand-off and uninstall | After hand-off the bootstrap key no longer authenticates; uninstall on a fresh host leaves only what was there before, compared by a host snapshot |

R5.3 then starts from a clean, supported server and runs the whole flow,
from the password to the release journey, as its evidence.

### Decisions (owner, 2026-10-19)

1. **Supported systems:** Ubuntu 22.04, 24.04 and 26.04, and Debian 12 and 13.
   The first list left out Ubuntu 26.04, the current LTS; the first real
   onboarding run stopped on it, so the owner widened the list the same day.
2. **OAP installs Docker itself**, as a sensitive fix the owner approves, and
   handles Docker it finds as in "Docker on servers that already have it".
3. **Automatic hostname:** sslip.io by default; the owner's domain when given.
