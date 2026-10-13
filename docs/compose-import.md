# Docker Compose import (proposal)

Status: **proposed, not implemented.** Scheduled in the [roadmap](../ROADMAP.md)
M3 queue. See [positioning](positioning.md) for why Compose is an input format and
not the runtime model.

Most target users already describe their system in a `docker-compose.yml`. An
import command converts it into a proposed `oap.dev/v1alpha1` `Application`
specification. Import never deploys, never adopts running Compose containers and
never reads secret values. The output goes through the normal
validate → diff → plan → apply path.

```sh
bin/oap import-compose --file docker-compose.yml --target docker-local \
  --environment production > application.json
```

The result is one JSON envelope containing the proposed specification and a list
of **decisions** (inferences the owner or agent must confirm) and **unsupported**
fields that were dropped. Nothing in either list is silently applied.

## Mapping

| Compose | OAP specification | Notes |
|---|---|---|
| project name / top-level `name` | `spec.name` | Overridable by flag |
| `services.<name>` | `components[].name` | |
| `image` | `image` | Mutable tags are kept in the proposal; a deploy plan still requires a digest or cached image ID |
| `build` | none | Decision: build through the external build lane and supply an immutable image |
| `ports` published | `kind: "web"`, `port` = container port | Host port is dropped; publication moves to routes |
| no published ports, long-running command | `kind: "worker"` | Decision when a service exposes ports only internally |
| `expose` / internal ports only | `kind: "web"` without a route | Reachable via app-local DNS |
| `deploy.replicas` | `instances` | Forced to 1 for services with writable volumes |
| `environment` literals | `env` | Values that look secret become decisions → `secretRefs` |
| `env_file`, `secrets` | `secretRefs` | Names only; values are bound separately by the owner |
| `depends_on` | `dependsOn` | `condition: service_completed_successfully` becomes a proposed pre-deploy job |
| references to another service hostname in env | `services` | e.g. `API_URL: http://api:8080` → `services: {"API_URL": "api"}` |
| named `volumes` | `volumes[]` (`name`, `mountPath`) | Bind mounts are unsupported |
| `healthcheck` | `healthCheck: {"mode": "image"}` + `readiness` | Image health check retained |
| `deploy.resources.limits` | `limits` (`memoryMiB`, `cpuMillis`) | |
| `labels` for Traefik/Caddy routing | route proposal (decision) | Host and path rules become route decisions |
| `privileged`, `network_mode: host`, `cap_add`, devices | unsupported | Rejected with a reason |
| `restart` | none | OAP owns restart and recovery |

### Databases and other stateful services

A service whose image is a known database (`postgres`, `mysql`, `mariadb`,
`redis`, `mongo`) becomes a decision with two options:

1. **Link an existing database**: the component is removed and dependents receive
   a `secretRefs` entry, following [existing database links](existing-database-links.md).
2. **Run as an owned single-instance component** with a retained named volume.
   Backup and restore stay explicit owner procedures.

Import never chooses on the user's behalf and never provisions database users,
schemas or migrations.

## Example

```yaml
services:
  web:
    image: ghcr.io/me/shop-web:1.4.0
    ports: ["3000:3000"]
    environment:
      API_URL: http://api:8080
    depends_on: [api]
  api:
    image: ghcr.io/me/shop-api:1.4.0
    ports: ["8080:8080"]
    deploy: { replicas: 2 }
    environment:
      DATABASE_URL: postgres://shop:secret@db:5432/shop
    depends_on: [db]
  worker:
    image: ghcr.io/me/shop-api:1.4.0
    command: ["worker"]
    environment:
      DATABASE_URL: postgres://shop:secret@db:5432/shop
  db:
    image: postgres:17
    volumes: ["pgdata:/var/lib/postgresql/data"]
volumes:
  pgdata: {}
```

Proposed specification (routes stay decisions until M3.1 adds them to the specification):

```json
{
  "apiVersion": "oap.dev/v1alpha1",
  "kind": "Application",
  "spec": {
    "name": "shop",
    "environment": "production",
    "targetId": "docker-local",
    "components": [
      {"name": "web", "kind": "web", "image": "ghcr.io/me/shop-web:1.4.0", "port": 3000, "instances": 1,
       "strategy": "standard", "services": {"API_URL": "api"}, "dependsOn": ["api"]},
      {"name": "api", "kind": "web", "image": "ghcr.io/me/shop-api:1.4.0", "port": 8080, "instances": 2,
       "strategy": "standard", "secretRefs": {"DATABASE_URL": "shop-database-url"}, "dependsOn": ["db"]},
      {"name": "worker", "kind": "worker", "image": "ghcr.io/me/shop-api:1.4.0", "instances": 1,
       "strategy": "standard", "secretRefs": {"DATABASE_URL": "shop-database-url"}},
      {"name": "db", "kind": "worker", "image": "postgres:17", "instances": 1, "strategy": "standard",
       "volumes": [{"name": "pgdata", "mountPath": "/var/lib/postgresql/data"}]}
    ]
  }
}
```

Decisions reported for this example:

- `web` and `api` published host ports; propose routes `/` → `web`, `/api` → `api`.
- `DATABASE_URL` contains a credential; replaced by secret reference `shop-database-url`.
- `db` is a database image: link an existing database, or keep it as an owned
  single-instance component with retained volume `pgdata`.
- `worker` overrides `command`; command overrides are not yet in the specification.
- Image tags are mutable; resolve digests before a deploy plan.

## Acceptance (when scheduled)

- Golden-file tests for the mapping table, including every unsupported field.
- The example above imports, validates, plans and deploys on standalone Docker
  after the decisions are resolved, with no secret values in the output.
- Unknown or ambiguous fields produce decisions, never silent defaults.
