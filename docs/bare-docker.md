# Bare Docker compatibility

OpenAppPlatform will run applications directly on Docker Engine as a first-class
target. Coolify, Dokploy, Dokku, Portainer, Swarm, and Kubernetes are not required.
This is a design specification; the runtime and adapters are not yet implemented.

## Responsibilities

The direct Docker adapter will manage image pulls, explicitly owned containers,
application networks, environment configuration, mounts, resource limits, restart
policies, health inspection, bounded logs, and lifecycle operations.

The controller provides application grouping, webhook handling, release records,
deployment serialization, and reconciliation, using the same model as operator
targets.

## Connection modes

Support local Unix socket access first. Remote connections can use SSH or
mutually authenticated TLS after their compatibility and authorization are tested.
Docker access grants substantial host privileges; do not expose an
unauthenticated Docker API. Scope deployment credentials to trusted targets.

## Artifacts and deployment

Start with prebuilt immutable images identified by digest. An operator build or
external CI can produce the artifact. Building from source directly on a bare
Docker target is a separate future capability.

Standard deployment is an explicit stop-and-replace lifecycle and may cause
downtime. Rolling and blue-green strategies remain optional future capabilities
requiring supported routing and spare capacity.

## Networking

Use an application and environment-specific user-defined Docker network with
stable component aliases. Alias-based discovery alone is not a health-aware load
balancer. Explicit host port mappings support the initial standard lifecycle;
domains, TLS, and advanced replacement strategies require a routing integration.

Docker bridge networks are local to one host. This target does not create a
cross-host cluster or provide recovery from host failure.

## Resource ownership and persistence

Label managed resources with application, environment, component, instance, and
release identifiers. Discover and reconcile only owned or explicitly adopted
resources. Never take control of resources managed by another operator implicitly.

Named volumes and bind mounts outlive container replacement. Adopt existing
storage by explicit reference. Normal stop, replacement, release cleanup, and
rollback must retain durable volumes; destructive storage removal is a separate
authorized action.

Inject secrets at runtime from secret references and exclude values from
manifests, logs, API output, and AI context. Docker environment values are visible
to privileged host users; they are not a secret vault.

## Initial acceptance criteria

- Deploy a prebuilt application on a server running only Docker Engine.
- Track the exact image digest and report actual container and health state.
- Expose configured ports and discover internal components by network alias.
- Support bounded logs, stop, restart, and standard replacement.
- Recover from controller restart and lost Docker API responses without duplicates.
- Preserve mounted data through replacement and retirement.
- Leave unrelated containers, networks, and storage untouched.
- Keep existing workloads running when the controller is unavailable.
