# Native HTTP health checks

Health check configuration is optional and belongs to OAP-owned image components. Existing adopted/observed resources keep their native health authority. A component may specify:

```json
{
  "healthCheck": {
    "mode": "http",
    "path": "/health/ready",
    "intervalSeconds": 10,
    "timeoutSeconds": 3,
    "retries": 3,
    "startPeriodSeconds": 10
  },
  "readiness": { "timeoutSeconds": 120 }
}
```

The check runs HTTP GET inside the container against `127.0.0.1` on the component's internal port. Paths are bounded, absolute local paths without queries/fragments or shell characters. Default interval/attempt timeout/retries are 10s/3s/3; start period defaults to zero. Limits are interval 1–300s, timeout 1–30s, retries 1–20, start period 0–600s. HTTP mode automatically requires operator-reported healthy status before release completion, even without `requireHealthy`; the release observation deadline remains separate.

Direct Docker creates an exec-form native health check using `wget`, which must exist in the image. Coolify configures its native HTTP check and reads its fields back before dispatch; its image needs curl or wget. HTTP success follows the native command's exit status; exact response-code/body matching is not configurable. The controller does not fetch arbitrary URLs. See [Coolify health check behavior](https://coolify.io/docs/applications/configuration/health-checks).

Saving a definition does not restart containers. A subsequent reviewed image deployment applies the check, using the existing standard deployment strategy. Changes to Docker native health settings replace the container; changing only the release deadline does not. Coolify image workflows remain native builds/deployments through the adapter.

Omit `healthCheck` for existing unmanaged health settings. After configuring it, use `{ "mode": "image" }` to remove the managed HTTP override explicitly. Removing the field is rejected to avoid silently relinquishing native configuration. Image mode restores the image's health check, if present. A healthless image reports running only, so a separate `requireHealthy` gate can continue to wait. Image mode contains no HTTP fields.

Native fields are checked against the frozen release before dispatch, during observation, during recovery, for unselected dependencies, and in image rollback safety checks. Drift holds the release for owner review. Checks never replace an exact recorded provider operation ID or authorize redispatch. Native HTTP/image settings are included in runtime comparison and image-only rollback configuration compatibility.

The Settings UI exposes path/timings with a readable diff; release history shows the frozen native check. `make test-local` reproduces unit, provider adapter, isolated PostgreSQL/controller, real Docker health-failure/image-return and browser tests. No migration is required. Older controllers do not understand this field; do not use them to process a release relying on its health configuration without a compatibility review.
