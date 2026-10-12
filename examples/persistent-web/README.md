# Persistent web component

Build this directory as the image context, set the target/image in
`application.json`, and create the application through OAP. Direct Docker needs
the image cached or a supported pull policy; Coolify uses its native image pull.
Add a host port if using direct Docker without external routing.

The non-root server writes a boot counter under `/data` and returns it as JSON.
Deploy once, record `boots`, restart, and observe a higher counter. Publish a
new image digest and deploy again: the counter must keep increasing. Saving a
new limit only affects the next deployment. Inspect the native container to
confirm its memory/CPU cap and the same named volume.

This is an isolated persistence demonstration, not a database or backup service.
Do not store valuable data without an operator backup and tested restore. The
example uses one writer; OAP rejects scaling it beyond one instance or changing
its saved mount. See [the storage contract](../../docs/persistent-volumes.md).
