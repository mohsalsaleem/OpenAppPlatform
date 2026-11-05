# ADR 0002: Application logs with VictoriaLogs

Status: **proposed** (2026-11-05). The owner chose the placement, access and
release below; the design and tasks await approval. Tasks are in the
[rewrite plan](../rewrite-plan.md#proposals-awaiting-the-owner).

## Context

OAP keeps no application logs. Agents diagnose from health, events and
outcomes only, and the owner reads a container's output with `docker logs`
over SSH, which hand-off removes. Two gaps follow:

- **Diagnosis stops at "unhealthy".** `explain` can say an instance never
  became ready, not why. An agent can't read the stack trace that would let
  it fix forward on its own.
- **Logs are unbounded.** The Docker adapter sets no log options, so every
  container writes Docker's default `json-file` log without rotation. On a
  small VPS a chatty application can fill the disk, which takes down every
  application on the host and OAP with it.

## Owner decisions (2026-11-05)

| Question | Decision |
|---|---|
| Where logs are stored | **VictoriaLogs on the server, optional.** OAP installs and pins it next to Caddy when the owner turns logs on |
| Who reads them | **Agents, scoped to their applications, and the owner, everything** |
| Release | **v0.3** |

## Options for collecting logs

1. **oapd tails container logs through the Docker API and ships them.**
   For each OAP-labelled container, oapd follows `GET /containers/{id}/logs`
   from a saved position and sends batches to VictoriaLogs' JSON-lines
   ingestion with the OAP labels as stream fields. No extra process; the
   local `json-file` log, now rotated, is the buffer, so VictoriaLogs being
   down or restarting loses nothing that is still on disk, and never slows an
   application. `docker logs` keeps working.
2. **A Docker logging driver pointing at VictoriaLogs** (syslog or GELF).
   Nothing to tail, but with most drivers `docker logs` stops working; a
   blocking driver stalls an application when VictoriaLogs is down, and a
   non-blocking one drops lines silently; and a driver can't add OAP's
   labels as stream fields without the logging options of every container
   naming a release.
3. **A collector container** (Vector, Fluent Bit or similar) reading
   Docker's log files. Mature, but another always-on process on a 1 GB host,
   another image to pin, and broad read access to every container's logs,
   including ones OAP doesn't own.

**Proposal: option 1.** It adds no process and keeps OAP's rule that it
only touches OAP-labelled resources (the shipper never follows a container
without a complete OAP label set).

## Options for reading logs

The rewrite doc fixes seven primitives; adding an eighth is an architecture
change.

1. **Extend `observe`.** `ObserveRequest` gains an optional `logs` selector
   (component, instance or release, time range, stream, text to match, line
   limit) and `ObserveResponse` returns the lines. Logs are something an
   agent observes, so no new primitive. `explain` also attaches the last
   error lines of components it names as gaps.
2. **A new `QueryLogs` read RPC.** Clearer as an API, but an eighth
   primitive for one read path, and MCP gains a tool agents must learn
   separately.
3. **`explain` only.** The smallest change, but agents can't look at logs
   before something is already wrong, for example after a deploy that
   reports achieved but behaves badly.

**Proposal: option 1**, with `explain` attaching error lines as in option 3.

## How it works

- **VictoriaLogs as a host service.** `oapctl host logs enable` (owner only)
  installs VictoriaLogs pinned by digest, as Caddy is, on the installation's
  internal network, never published on a host port. It runs with a memory
  limit (128 MiB proposed), a retention period (7 days proposed) and a disk
  limit (proposed: the smaller of 2 GiB and 10% of the disk), all owner
  settings. `disable` stops it and keeps its data unless the owner also
  passes `--delete-data`; uninstall removes it like every OAP resource. The
  exact flags are confirmed against the pinned version when the task starts.
- **Bounded local logs, always.** Every container OAP creates, including
  oapd and Caddy, gets `json-file` with `max-size` and `max-file` (for
  example 10 MiB × 3), whether or not VictoriaLogs is on. This is the buffer
  the shipper reads from, and fixes the unbounded-disk gap on its own.
- **Shipping.** oapd follows each OAP-labelled container's stdout and
  stderr with timestamps, keeps a per-container position in its SQLite store,
  and sends batches with stream fields `application`, `component`,
  `release`, `ordinal`, `instance` and `stream`. Delivery is at least once;
  each line carries the container ID and Docker's timestamp so a reader can
  tell a resend. A bounded buffer in oapd sheds the oldest lines under
  pressure and counts what it dropped; the count appears in `observe`, never
  silently.
- **Scoped queries.** oapd builds the LogsQL itself from the structured
  selector. An agent's query always carries a filter on the applications in
  its scope; text to match is passed as a quoted phrase, never as LogsQL, so
  it can't widen the filter. Only the owner API accepts raw LogsQL. Limits
  on lines, bytes and time range keep a query cheap on a small host.
- **Untrusted and redacted.** Returned lines are marked as untrusted
  content, like app output today. Before returning lines to an agent, oapd
  replaces any value it holds in the secret store for that application with
  `[secret: NAME]`, so an application that prints its own credentials doesn't
  hand them to the agent. The owner can read unredacted lines.
- **Policy.** Enabling or disabling logs, and changing retention, are owner
  actions, not proposals. Reading logs needs no verdict beyond scope, like
  `observe` today.

## Consequences

- Agents can find a cause, not just a symptom: `explain` shows the error
  lines of a failing component, and `observe` lets them check behaviour
  after a deploy.
- The host gains one optional service with a fixed memory and disk budget.
  On the 1 GB host R5.3 peaked at 452 MiB with at least 498 MiB available;
  128 MiB for VictoriaLogs fits but must be measured under the release
  journey with logs on.
- Redaction catches only values OAP knows; secrets an application derives
  or loads elsewhere can still appear. This is recorded as a known
  limitation.
- External VictoriaLogs, metrics and traces are not part of this decision.
