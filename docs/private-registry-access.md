# Private Registry Access

OAP-owned standalone Docker components can declare `registryCredentialRef`.
Only the reference is stored in definitions, releases and exports. The platform
administrator grants credentials to an exact target/application/component and
registry host. Registry credentials never become container environment variables,
Docker labels or a shared `docker login` configuration.

Create the application first to obtain its ID, then configure OAP's server:

```json
[
  {
    "reference": "registry-read-v1",
    "targetId": "docker-local",
    "applicationId": "<32-character application ID>",
    "component": "web",
    "registry": "ghcr.io",
    "usernameEnv": "OAP_SECRET_REGISTRY_USER",
    "passwordEnv": "OAP_SECRET_REGISTRY_PASSWORD"
  }
]
```

Set this JSON as `OAP_REGISTRY_INPUTS`, and supply the two dedicated environment
variables privately through the platform's deployment configuration. Restart the
OAP controller to load the bindings. Values are read once at startup. Use a
read-only repository token and an immutable reference name. Passwords/tokens must
be 8–4096 bytes. Never put values in Git, application JSON or target settings.

Set the Docker target's `pullPolicy` to `if-missing` and the component's
`registryCredentialRef` to `registry-read-v1`. The Create and Settings forms expose
the reference under Private Registry for supported targets. OAP rejects missing
or unauthorized bindings before admitting a release. The grant's host must match
the requested image; unqualified images use `docker.io`. Explicit ports form part
of the host. Use image digests for reproducible releases.

Docker receives `X-Registry-Auth` only on its image-pull request. Pull failures
are sanitized and stop preparation before replacement. Cached images do not
trigger authentication or freshness checks; `never` remains the default pull
policy. Registry access is not a continuous credential validation feature.

Credentials are included in private release recovery fingerprints. A changed or
missing binding holds an in-flight operation; restore the admitted binding before
retrying. Redaction covers passwords and auth encodings. Recover OAP with the same
private deployment environment and grants as well as its metadata backup. Keep
historical bindings available for log masking. Rotation, dynamic tokens, credential
helpers, build-time credentials and a general vault remain deferred.

## Operator-managed Login

Coolify uses its native server registry login. Configure it in Coolify or log in
as its server SSH user following the
[Coolify registry instructions](https://coolify.io/docs/core/infrastructure/servers/build-servers).
OAP does not write or replace that server-wide configuration. Coolify advertises
`registryCredentials: false` for OAP reference injection; the UI explains native
login and API validation rejects such overrides. Linked/adopted resources keep
all configuration in the operator. Existing native login continues to work for
OAP-created image workloads.

## Reproducible Acceptance

`make test-local` runs an in-memory authenticated Distribution v2 fixture against
the isolated real Docker Engine, with no external registry account. It checks
missing grants without dispatch, invalid authentication, successful pull and
execution, reference-only serialization, scope/host isolation, redaction and
changed-binding recovery holds. The fixture is a test server, not a new production
registry service. Its loopback HTTP endpoint exists only in the disposable shared
runner/Engine network namespace. Production registry transport/trust remains
owned by Docker or the native operator.
