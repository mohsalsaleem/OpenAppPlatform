# Connected preview: retained staging deliverable

Verified on 11 October 2026. This is a working stateless application checkpoint
within M0 batch 5, not completion of automatic domain/routing management.

## Try it

- [Open the interactive demo](https://r86qkfu0ig6cgeyqs51mrkbh.mohsal.dev/demo)
- [Manage the application in OAP](https://oap-staging.mohsal.dev/applications/7cc13fa9d4bb51314240aabdcc7b851d)
- [Inspect the shared API endpoint](https://oap-api-preview.mohsal.dev)

Click **Call the API** repeatedly in the demo. Responses identify the native API
instance and process boot identity. In OAP, Overview shows two components and
three healthy instances; Activity contains the update, rollback/return and restart.
Review Deployment lets you select the API separately from the frontend. Cancel a
review if you are only inspecting it; execution changes the retained fixture.

The existing owner workspace session is required for management. No credentials
are embedded in this document or the public demo. These endpoints contain synthetic
data only and the demo is retained for owner testing.

## Verified journey

| Check | Observed result |
| --- | --- |
| Connected deployment | One OAP application, frontend ×1 and API ×2, all native Coolify HTTP health checks passing |
| Shared domain/TLS | Existing Coolify Traefik file provider, real HTTPS verification and two reachable native upstream URLs |
| API-only image update | Both API instances replaced; frontend boot identity unchanged; 61 sampled frontend requests succeeded with 0 failures over 60 seconds spanning old/new responses |
| Image rollback and return | Earlier verified API image restored, then newer verified image restored; same frontend and shared URL; one initial return request rejected with 422 and a fresh reviewed plan accepted |
| API replica outage | Exact demo replica stopped; after 5 seconds of health-check settling, 20 requests succeeded through the surviving replica |
| OAP restart of stopped replica | Recorded native restart succeeded and both API instances returned to shared traffic |
| Controller outage | Staging OAP controller confirmed unavailable during a controlled stop; 8 frontend/API requests succeeded with 0 failures; independent native restart restored OAP |
| Metadata recovery | Fresh PostgreSQL backup restored into a separate inactive database; 11 migrations, owner, six terminal demo releases and rollback provenance verified |
| Browser journey | Actual demo button received an API response; OAP Overview showed 3/3 healthy after controller recovery |

These are bounded samples, not an availability guarantee. Standard deployments
remain supported; zero downtime, immediate health convergence and stream draining
are not claimed. The first return request's detailed rejection was not retained;
no release rows or safeguards were changed to complete the fresh-plan retry.

## Artifacts and identities

Example feature commit: `c4f24e9`. Platform staging remains on `197cab8`; this
checkpoint changes example source, route tooling and native demo resources rather
than the platform binary.

Baseline image (sample source at `197cab8`):
`127.0.0.1:5001/openappplatform/connected-preview@sha256:7459091ae341715779b8a85a2fd92d738fe1c2cdd824950fd98545cd5b7c608b`.

Retained image (sample source at `c4f24e9`):
`127.0.0.1:5001/openappplatform/connected-preview@sha256:f7a3e00c05c2339301e8030296151a719efb54cbe5dc4ef4f8a9f4149400d19f`.

| Operation | OAP release |
| --- | --- |
| Initial API | `a020d85192c6051ebc89fa402feb5eaa` |
| Initial frontend | `ea887dd3f4d6d3760d3e4efa80ea4a5c` |
| API update | `b757ea94dc250296ca7854259b675663` |
| API rollback | `8bbbc9ba3bcfadde84e8edf1314ccb11` |
| API return | `407b005499f9a8a77c0e951f09997ffc` |
| Outage restart | `13f6a8b25ce1e0e1b57bb1e6fbf38c2d` |

Native API resources remain `tewkzpfharq1e47llgbq1zrp` and
`q56odfifosic4u713btkepyt`; frontend is `r86qkfu0ig6cgeyqs51mrkbh`.
The isolated native route file is
`/traefik/dynamic/oap-preview-7cc13fa9d4bb.yaml` in `coolify-proxy`.
No additional proxy process or production resource was added. Existing native
GitHub workflows were preserved; this demo explicitly exercises the managed-image
lane and does not demonstrate automatic GitHub delivery for this new application.

The [committed example](../examples/connected-preview/README.md) reproduces the
setup and journey. Local-only receipts, probe counters, backup verification and
screenshots are retained under `.local/connected-preview*` and
`.local/preview-traffic-*`. They contain no published login credentials.

## Next roadmap work

The native route was reviewed and published explicitly; OAP does not yet reconcile
membership after scaling or own custom-domain/TLS lifecycle. Automatic route
publication, authority/drift checks, membership updates and connection draining
remain batch 5 work. Bare Docker shared proxy routing is also not delivered by this
Coolify checkpoint. Keep these boundaries visible while iterating from the working
demo rather than marking the whole batch complete.
