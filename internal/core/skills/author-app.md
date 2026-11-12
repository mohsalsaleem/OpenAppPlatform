# Putting an application on OAP

You are an agent asked to run an application on a server that OAP manages.
OAP doesn't know or care what language or framework the application uses.
It runs **container images** described by an **`oap.yaml`**. Your job is to
produce both from the repository, then propose them to OAP. OAP judges
the proposal against the owner's policy; you never decide that yourself.

This guide comes from the OAP server you are talking to, so it matches that
server's version. The exact format is the JSON Schema at
`oap://schema/application` (MCP resource) or
`/.well-known/oap/schema/application.json` (HTTPS, with your token).

## Rules that are never bent

1. **No secret values anywhere you write.** Not in `oap.yaml`, not in `env`,
   not in a Dockerfile, a build argument, a commit, a log line or your reply.
   A secret is anything that grants access: passwords, API keys, tokens,
   private keys, and connection strings that contain a password. If the
   repository holds real values (a `.env` file, a config file), don't copy
   them; treat them as names of things to ask for.
2. **The owner provides secret values, not you.** Declare a secret
   reference, and ask the owner for the value with an `ask` of kind
   `secret` (see below). Never ask for a value in chat.
3. **Propose, then apply.** Every change goes through `propose` first. Read
   the verdict and its reasons. Apply only an `allowed` proposal; for
   `needs approval`, ask the owner with an `ask` of kind `approval`.
4. **Observe before and after.** `observe` (and the briefing,
   `oap://briefing`) tells you what already runs and what you may do.
   After applying, `await` the operation and check `observe`; use
   `explain` when something differs from what you asked for.

## 1. Read the repository

Find, and write down with the file and line you found it in:

| What | Where it usually is |
|---|---|
| How it starts | `Dockerfile` (`CMD`, `ENTRYPOINT`), `Procfile`, `package.json` scripts (`start`), `main` packages (`go.mod`, `cmd/*/main.go`), `pyproject.toml`/`manage.py`/`wsgi.py`, `Gemfile`/`config.ru`, `docker-compose.yml` services |
| Processes | One per long-running process: the web server, each background worker (queue consumers, schedulers), and one-off tasks such as migrations |
| Port | What the web process listens on: `listen(`, `ListenAndServe`, `PORT` variable, `EXPOSE`, framework defaults. It must listen on `0.0.0.0`, not `127.0.0.1` |
| Health endpoint | A cheap `GET` route such as `/healthz`, `/health`, `/up`, `/ping`. If there is none, use a route that returns 200 without a database query, or suggest adding one |
| Configuration | Every environment variable the code reads: `os.Getenv`, `process.env.X`, `os.environ`, `ENV[...]`, config libraries. Mark each one **secret** or **plain** |
| Data services | Database and cache drivers in the dependencies (`pg`, `pgx`, `psycopg`, `mysql`, `redis`, `ioredis`, `mongodb`), and the variables that point at them (`DATABASE_URL`, `REDIS_URL`) |
| Migrations | `migrate`, `goose`, `prisma migrate deploy`, `knex migrate`, `alembic upgrade`, `rails db:migrate`, `manage.py migrate` |
| Files it keeps | Uploads or data written to disk that must survive a restart: these need a volume |

If something can't be determined, say what you assumed and why.

## 2. The image

Use the repository's `Dockerfile` if it has one that builds the production
process. Otherwise write one:

- Multi-stage: build in one stage, copy only what runs into a slim runtime
  stage. Pin base images to a version tag (a digest is better).
- Run as a non-root user. Listen on `0.0.0.0` and the port you chose.
- Write only to `/tmp` and to volumes. OAP runs every container with a
  read-only root filesystem, no Linux capabilities and no way to gain
  privileges (see "Hardening" below), so an image that writes into its own
  directories, `chown`s files or switches users at start fails there.
- No secrets in the image: no `ARG`/`ENV` with values, no copied `.env`.
  Add a `.dockerignore` that excludes `.env*`, `.git` and build output.
- One image may serve several components (web and worker with different
  start commands), but OAP has no per-component command yet: build one
  image per process, or make the entrypoint choose by a plain env variable
  such as `PROCESS=worker`.

**OAP can't build from source yet.** The image must be in a registry the
server can pull from without credentials (for example a public GitHub
Container Registry or Docker Hub image), referenced by tag or, better, by
digest (`image@sha256:…`). If it can only be private, tell the owner with
an `ask` of kind `blocker`: OAP doesn't pull private images yet.

## 3. Write `oap.yaml`

```yaml
api_version: oap.dev/v1
kind: Application
name: shop                 # lowercase letters, digits, dashes
environment: production
ingress:                   # only for applications reached from the internet
  domain: shop.example.com # the owner's domain, pointing at the server
  routes:
    /:
      component: web
components:
  web:
    kind: web
    image: ghcr.io/acme/shop:1.4.2
    port: 8080
    instances: 2
    health: {mode: http, path: /healthz}
    env:                   # plain configuration only
      LOG_LEVEL: info
    secrets:               # VARIABLE: secret name; values come from the owner
      DATABASE_URL: shop-database-url
```

If the owner has no domain for it yet, `APP.SERVER-IP-WITH-DASHES.sslip.io`
(for example `shop.203-0-113-5.sslip.io`) resolves to the server without
any DNS change, and OAP gets it a certificate.

Component kinds and their rules (OAP refuses specs that break them):

| Kind | Use it for | Rules |
|---|---|---|
| `web` | Processes that serve HTTP | Needs `port`. Routes point only at web components. `health: {mode: http, path: …}` is recommended, but it runs `wget` inside the container: for images without it (`scratch`, distroless) use `health: {mode: image}` with a `HEALTHCHECK` in the Dockerfile, or none |
| `worker` | Long-running processes without a port: queue consumers, schedulers | No `port`; must have `health: {mode: image}`. OAP then judges the image's `HEALTHCHECK`; if the image has none, the worker counts as healthy (and ready for `after`) as soon as it is running, so a worker that crashes shows as not running. Add a `HEALTHCHECK` to the Dockerfile when running isn't enough |
| `job` | One-off tasks such as migrations | One instance, no port, volumes or health check. Mark `destructive: true` if a run changes data in a way a rollback can't undo (dropping columns, deleting rows): such plans always need the owner |
| `data` | Databases and caches run on the server | Exactly one instance; at least one volume (`volumes: {data: /var/lib/postgresql/data}`) |

Other fields you'll need:

- **`after: [component, …]`**: start this component only once those are
  ready (or, for jobs, succeeded). Put migrations in a `job`, and list it
  in the web and worker components' `after`.
- **`uses: {component: {type: http|database|queue}}`**: a typed connection.
  It also orders startup. `type: http` with `env: VARIABLE` injects that
  component's in-network URL into `VARIABLE`. Database and queue
  connections inject nothing: credentials are secrets.
- **Reaching another component**: on the server's network each component
  answers to `COMPONENT.APP.oap.internal` (for example
  `db.shop.oap.internal`). A connection string to a `data` component holds
  its password, so the whole string is a secret, for example
  `postgres://shop:PASSWORD@db.shop.oap.internal:5432/shop` stored as
  `shop-database-url`.
- **`volumes: {name: /mount/path}`**: data that survives restarts and
  releases. OAP snapshots volumes before changes when the owner's policy
  says so.
- **`resources: {memory_mib: 256, cpu_millis: 500}`**: set them on small
  servers. Check the free memory in `observe` (host capacity) first. If
  `memory_available_mib` is absent the host doesn't report it (the
  briefing says "available memory unknown"): size against
  `memory_total_mib` minus what already runs there, and say so in your
  summary.
- `instances` is 1 to 4. Only stateless web and worker components should
  run more than one.

### Hardening and its exceptions

Every container OAP runs as a non-root user, drops all Linux capabilities,
can't gain privileges (setuid binaries don't elevate), has a read-only root
with a 64 MiB writable `/tmp`, at most 256 processes, and a memory limit
(256 MiB when `resources` sets none). Plan for that; don't ask for
exceptions by default.

**The user.** If the image's own user is root (many images don't set one),
OAP refuses to start it. Set the component's `user` to a numeric uid and
gid, for example `user: "65532:65532"`: the process runs as that user, and
volumes OAP creates for the component belong to it, so it can write its
data. A process that binds a port below 1024 still can as non-root. This is
not an exception and needs no approval.

If an image really needs more, say exactly what under the component's
`security`, with a reason. `run_as_root` is for images that must start as
root, usually to switch to their own user in the entrypoint. Each exception is sensitive, so the owner
usually has to approve it:

```yaml
api_version: oap.dev/v1
kind: Application
name: shop
environment: production
components:
  db:
    kind: data
    image: postgres:17-alpine
    port: 5432
    volumes:
      pgdata: /var/lib/postgresql/data
      pgrun: /var/run/postgresql
    security:
      run_as_root: true
      capabilities: [CHOWN, SETUID, SETGID]
      exception_reason: "the official image chowns its data and drops to its own user at start"
```

This is the official PostgreSQL image under OAP's defaults: it starts as
root and needs those three capabilities to switch to its own user, and a
volume for its socket directory instead of a writable root.

- `capabilities` keeps those capabilities only; anything outside Docker's
  default set (such as `SYS_ADMIN`) is refused. Write names without `CAP_`.
- `writable_root` gives a writable root filesystem. Prefer a volume or
  `/tmp` for what the process writes.
- `allow_privilege_escalation` lets setuid binaries elevate. Almost nothing
  needs it.
- `resources.pids` changes the process limit.

A common case: official images that start as root and switch to their own
user in the entrypoint (many database images) need `CHOWN`, `SETUID` and
`SETGID`, and sometimes `DAC_OVERRIDE` or `FOWNER`. If an instance never
becomes ready, read `explain`: a permission error points at a missing
exception.

Secret names are lowercase letters, digits and dashes. Prefix them with the
application's name (`shop-database-url`, `shop-stripe-key`) so they don't
collide.

## 4. Secrets: declare, then ask

For every variable you marked secret:

1. Put it under the component's `secrets`, with a name.
2. Ask the owner for the value, once per secret:
   `ask` with `kind: ASK_KIND_SECRET`, `secret_ref: <name>`, and a
   `question` that says what it is, what it's for and where the owner can
   find or create it. For example: "The shop needs its Stripe secret key
   (Stripe dashboard → Developers → API keys). Please store it as
   shop-stripe-key." The owner stores it with
   `oapctl owner secret set shop-stripe-key --ask ASK_ID`. The value never
   passes through you.
3. Wait for the answer through `watch` (the `ask_answered` event).
   `provided` means it is stored. `declined` means stop and ask what to do
   instead. If the ask expires, ask again or report it.
4. For a database you run as a `data` component, the owner chooses its
   password too: ask for the database's own password (for example
   `shop-db-password`, used by the `data` component's `POSTGRES_PASSWORD`)
   and for the application's connection string with the same password
   (`shop-database-url`), and say so in the question.

You can't list or read secrets. Don't try to work around that.

## 5. Propose, apply, verify

Through MCP or the API:

1. **Create**: `propose` with `intent.application: <name>` and
   `intent.spec_patch.merge_patch` holding the whole spec (as JSON). With
   the CLI: `oapctl app apply oap.yaml`.
2. **Read the result.** A spec OAP can't accept comes back as an
   `invalid_argument` error whose detail names the field and the rule: fix
   the spec and propose again. Otherwise the proposal has a verdict with
   reasons: `VERDICT_ALLOWED`: go on. `VERDICT_DENIED`: the policy forbids
   it; don't work around it, tell the owner. `VERDICT_NEEDS_APPROVAL`: ask
   with `kind: ASK_KIND_APPROVAL` and the `proposal_id`, then wait for the
   answer.
   A new application usually needs approval, or a grant the owner gives
   you: an agent may only act on applications in its scope.
3. **Apply** the allowed or approved proposal, then **`await`** its
   operation until the outcome.
4. **Verify**: `observe <app>` shows every component converged and
   healthy, and routes at the domain. If not, `explain <app>` names the
   gaps and likely causes. Fix forward with a new proposal, or roll back.

Later changes use the narrow intents: `deploy` (new images), `scale`,
`configure` (plain env), `rollback`, and `spec_patch` for anything else.

## Checklist before proposing

- [ ] Every process in the repository is a component, or you said why not.
- [ ] Web components have the right `port` and listen on `0.0.0.0`.
- [ ] Every secret is under `secrets` and none appears anywhere else.
- [ ] Every secret has an ask, and every ask was answered `provided`.
- [ ] Migrations run as a `job` that the components using the database
      list in `after` (or `uses`).
- [ ] `data` components have a volume; nothing else writes data it needs
      to keep outside a volume.
- [ ] The image can be pulled without credentials.
- [ ] Every component runs as non-root: the image sets a non-root user, or
      the spec sets `user`, or `run_as_root` with a reason.
- [ ] The image writes only to `/tmp` and volumes, or the spec has
      `writable_root` with a reason; any `capabilities` are the fewest it
      needs.
- [ ] Memory fits: the components' `resources` add up to less than the
      host's available memory in `observe` (or, if that is unknown, its
      total less what already runs).
