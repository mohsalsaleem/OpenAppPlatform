# Opt-in automatic route membership

A human workspace owner can enable automatic membership for an existing configured
Coolify staging endpoint after reviewing its current healthy membership. OAP checks
enabled routes through its existing PostgreSQL/River worker once per minute and at
worker startup. No additional service or proxy is introduced. The endpoint address,
TLS policy and native proxy health checks stay under the existing routing authority.

The grant pins the native file bytes, directory identity, configured binding and
target settings and credential reference. Each check derives healthy owned backends from a complete replica
binding set. It can remove stopped/unhealthy instances and add recovered or new
healthy instances after deployment/scaling completes. Active or uncertain deployment
work, missing ownership evidence and an empty healthy set block updates. Native
edits, replaced directories, changed target/configuration authority or a revoked
owner account pause and disable the grant. A blocked grant retries next minute;
a paused grant needs owner review and explicit re-enabling. Native credentials
remain server-side. Agents can inspect status but cannot grant or disable automation.
The owner's session can expire without cancelling an explicit durable grant; the
owner account must remain active. To return to manual routing, disable automatic
updates first. Disabling never clears an uncertain publication fence.

## Publication and recovery

The worker acquires the same application admission lock as owner publications and
lifecycle admission. It persists a prepared plan and its policy reference atomically,
rechecks current health, commits publishing intent before writing the native file,
then atomically records the applied receipt and new native baseline. Automatic
receipts retain owner attribution and an automatic marker. Native drift is never
silently adopted. A normal tick with current membership creates no new publication.

A crash after native write can resume the exact saved intent without replacing an
already desired file, if backend and routing authority still match. Changed backend
state during uncertain intent pauses automation and leaves the lifecycle fence for
owner recovery. Prepared stale intent is cancelled before a fresh check. An owner
can resume/cancel uncertain work through the existing Endpoints recovery controls;
re-enabling then requires synchronized membership and pins the reviewed current file.

The database lock cannot prevent an independent native editor from racing a file
publication. The file protocol checks hashes and uses atomic replacement, but this
is not a distributed transaction or universal filesystem compare-and-swap. Use one
publisher workflow at a time. Controller downtime stops reconciliation while the
native proxy and existing route continue independently. Observations can become
stale between health inspection and publication. Updates do not drain connections,
create domains, provision TLS, change deployment ordering or guarantee zero downtime.
Production and bare Docker routing remain outside this pilot.

## Reproducible verification

System tests exercise default-off behavior, stale enablement, agent denial,
automatic failure/recovery, empty healthy sets, duplicate ticks, controller restart,
native drift, binding/target/directory changes, durable fence recovery, health changes
during uncertain intent, revoked owners and the real River worker. HTTP tests check
owner-session enforcement, required flags and grant audit. Browser tests exercise
the opt-in dialog, automatic status, manual-control exclusion, disable and pause.
