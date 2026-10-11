# Connected application preview

This is a hands-on staging checkpoint: one OAP application with a frontend and
two API instances, plus a stable shared API URL served by the existing Coolify
Traefik proxy. No additional proxy process or orchestrator is required.

Build `examples/hello-api` using the trusted server builder or the native operator's
build workflow. Use immutable image digests in `application.json`. Replace the
target and shared-domain placeholders before creating the application in OAP.
The example is stateless and contains only synthetic responses.

Deploy `api` first. Capture both native API resource URLs from Live Instances.
Render a native route using:

```sh
python3 scripts/render-coolify-route.py --name oap-connected-preview \
  --domain your-api-domain.example.com \
  --upstream https://first-native-instance.example.com \
  --upstream https://second-native-instance.example.com > route.json
```

The renderer follows Coolify's documented Traefik file-provider configuration.
It never applies the route. Review hostname ownership, proxy reachability and the
native `http`/`https` entrypoints and `letsencrypt` resolver before applying it via
the server's Proxy → Dynamic Configurations. Do not overwrite an existing route.
Point DNS at the existing proxy, validate HTTPS, then deploy `frontend` in OAP.

Open the frontend native URL with `/demo`. Click **Call the API** repeatedly.
Newer sample images show backend instance and boot identities. The API's native
URLs remain independently inspectable; only the shared URL belongs in the
frontend's explicit `serviceEndpoints.api` connection.

## Acceptance journey

1. Verify all three instances are healthy in OAP and the frontend reaches the API.
2. Update only `api` to another immutable image. Verify the frontend instance was
   not redeployed and shared endpoint requests reach the replacement instances.
3. Review an image rollback to the earlier fully verified API release, acknowledge
   data compatibility, execute it and return to the newer verified image.
4. Stop one API instance through the native operator. After the proxy's health
   check detects it, verify requests reach the survivor. Restart through OAP and
   verify both return to service.
5. Stop only the staging OAP controller temporarily, with an independent native
   restart path ready. Verify frontend/API requests continue, restart OAP, and
   verify instance/release state reconnects.

Record versions, release IDs, unchanged resource URLs, frontend process identity,
traffic results and any transient errors. Standard deployments may interrupt
requests; this is not a zero-downtime acceptance claim. Failed backend removal is
eventual according to the native health check, not instantaneous.

This checkpoint uses an explicitly reviewed native route file. OAP does not yet
own route publication, custom-domain lifecycle, automatic route membership after
scaling or connection draining. Those remain batch 5 work. Keep the route's
upstream list aligned with supported instance changes using the native operator.
Native GitHub workflows on other resources are left intact. This demo chooses the
managed-image lane to exercise OAP release controls.

Source: [Coolify native load-balancing documentation](https://coolify.io/docs/core/networking/proxy/traefik/load-balancing).
