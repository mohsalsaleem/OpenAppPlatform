# Positioning: an application platform on one server, operated by an agent

> DigitalOcean App Platform, on your own server, built for your agent to operate.

This document records why OAP exists, what it is compared with, and which
capabilities define it. The [roadmap](../ROADMAP.md) decides execution order.

## Problem

A solo developer hosting their own systems on one server wants to treat a system
(frontend, API, worker, and the database it uses) as **one application**: one
definition, deployed, scaled, rolled back and diagnosed together. They also want
an AI agent to do that work through safe, structured interfaces instead of a
dashboard or a root shell.

Existing self-hosting tools cover the operational primitives but miss one or both
of these:

- **Coolify, Dokploy, Portainer** manage individual resources well. Grouping
  several services into one scalable application is not the primary model:
  per-component replicas, app-wide releases and app-wide health are not
  first-class, and their interfaces are built for people using a dashboard.
- **Dokku** has process types (web/worker) inside one app, but not several
  services with separate images forming one application.

## The two pillars

1. **The application as a unit.** One specification describes every component,
   its kind (web/worker), instance count, routes, internal connections, secret
   references and volumes. Deploy, scale, release history, rollback and health
   apply to the whole application.
2. **Agents as the operator.** The specification is a file an agent edits,
   validates, diffs, plans and applies. Every outcome is a durable operation with
   structured evidence. Authority is bounded, enforced by the server, auditable and
   revocable. The agent never needs a root shell.

The pillars reinforce each other: a declarative application specification is the
interface agents handle best. The defining loop is:

```text
edit spec → oap validate/diff → oap plan → (owner review if required) → oap apply → oap verify
```

### Defining capability: the scale loop

Changing `"instances": 1` to `"instances": 3` on a web component and applying
must result in three healthy copies serving traffic behind one stable,
HTTPS-terminated address. A new copy joins the route only after it is ready; a
removed copy leaves the route and is drained before it stops; routes keep working
while OAP is down. Workers scale without routing. Components with writable local
volumes stay at one instance.

Today OAP creates and retires instances, but standalone Docker publishes a
component through a fixed host port, which is limited to one instance. Replica
routing is the missing layer and is the first M3 slice.

## Comparison

| | Docker Compose | Docker Swarm | Kamal | k3s / MicroK8s | **OAP (target)** |
|---|---|---|---|---|---|
| Application as one unit | Loose (project) | Loose (stack) | One app + accessories | No (resources; Helm approximates) | **Yes** |
| Replicas from the definition | Yes | Yes | Yes | Yes | Yes |
| Health-aware routing + TLS | DIY with labels | Routing mesh, TLS DIY | Yes (kamal-proxy) | Yes, with ingress setup | **Built in** |
| Health-gated rollout | No (`up` recreates) | Yes | Yes | Yes | Standard first; optional later |
| Release history / rollback | No | Partial | Yes | Yes | Yes |
| Long-running controller, drift visibility | No (one-shot CLI) | Yes | No (deploy tool) | Yes | Yes |
| Footprint on one small VPS | Minimal | Small | Small | Heavy | **Small (measured)** |
| Bounded agent authority and audit | No (shell) | No (shell) | No (SSH) | Possible, complex RBAC | **Core** |

Notes:

- **Compose** gets a skilled developer most of the way with a reverse proxy, and
  `deploy.replicas` works. It is a one-shot converter from a file to containers:
  no memory of releases, no health-gated rollout, no controller after the command
  exits, and no interface an agent can use without a shell. It is too generic to
  know that one service is public at `/api` and another is a worker.
- **k3s/MicroK8s** solve every runtime problem here, and agents know `kubectl`.
  The cost is memory, concept sprawl (Deployments, Services, Ingress, ConfigMaps,
  PVCs, Helm) and complex agent authorization. OAP should stay far smaller than
  Kubernetes and never re-implement it.
- **Swarm and Kamal** are the honest lightweight baselines. Neither has an
  application model spanning several services nor a bounded agent interface.

## What OAP builds and what it reuses

Build: the application model and specification, the release/health controller,
durable operations, bounded agent authority, incident evidence and verification.

Reuse: Docker Engine for containers and networks, Caddy (or an equivalent proxy)
for routing and automatic certificates, an external build lane for images, and
Compose files as an [import format](compose-import.md).

## Non-goals

No Kubernetes API, multi-tenant fleet, embedded model, mandatory dashboard flow,
or multi-server scheduling before the single-server application loop is proven.

## Reference application

Acceptance should use one realistic system on a small VPS: web frontend, API,
background worker, and a linked PostgreSQL, behind one domain with `/` and `/api`
routes. An agent creates it from an empty server, scales the API from 1 to 3 and
back without failed requests, ships a change, diagnoses an injected regression,
fixes it and verifies application behavior.
