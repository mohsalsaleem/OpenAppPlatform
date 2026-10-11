# Worker Example

Build from this directory with `docker build -t oap-worker-example .`. The image
starts a long-running Go process, emits bounded operational messages and updates a
local heartbeat. `/worker check-health` exits successfully only for a recent
heartbeat. It has no HTTP listener, queue service or application database.

Use [application.json](application.json) after replacing the target and image with
your configured target and immutable image reference. Add this component beside
web/API components in a new logical application. Deploy, inspect logs, restart the
worker instance and verify the web/API process identities stay unchanged.

The reproducible `make test-local` environment builds this source with pinned
images and runs the native Docker worker/web integration and browser journey.
Database linkage is the next roadmap priority; this example does not claim it.
