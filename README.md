# OpenAppPlatform

An application-first control plane for bare Docker and existing deployment operators.

Manage applications, components, environments, and releases directly on Docker
Engine or through Coolify, Dokploy, Dokku, and Portainer, with an experience
inspired by DigitalOcean App Platform.

## Status

Design phase. No runtime or operator adapters have been implemented.

## Direction

- Adopt existing resources without requiring an infrastructure migration.
- Make bare Docker Engine a first-class target with no other platform required.
- Group services, workers, jobs, and linked dependencies into applications.
- Coordinate GitHub-triggered deployments and report partial failures.
- Start with standard deployments; rolling and blue-green are optional later.
- Offer structured API, CLI, and MCP operations, with AI-assisted diagnosis.

## Proposed stack

Go controller and API, React and TypeScript dashboard, PostgreSQL, pgx,
sqlc, and River. Start as one Docker application with a dedicated database.

## Documentation

- [Product and architecture draft](docs/product-and-architecture.md)
- [Bare Docker compatibility design](docs/bare-docker.md)
- [Editable design Page](https://chatgpt.com/space/page_48ca8aa82f6881918cae167d00860fe9)

## Initial milestone

Support both bare Docker Engine and adoption of existing Coolify resources.
Present one coherent application view and coordinate standard deployments with
durable progress and exact version tracking. Validate both adapters against the
same lifecycle contract before extending deployment strategies.
