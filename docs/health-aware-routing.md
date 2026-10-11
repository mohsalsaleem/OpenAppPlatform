# Health-aware reviewed routing

Configured Coolify staging endpoints now distinguish desired replicas from healthy
routing candidates. Every active replica must still have a unique ordinal, a
verified owned image resource and an allowed origin. A stopped, unhealthy or
unknown-status instance remains bound to its application but is excluded from
the proposed native backend set. Retired bindings retain removal authority.

The owner sees excluded instances and their observed native status in Endpoints
and in the saved review. Applying a review rechecks the complete authority,
including backend health. A health change invalidates an unapplied review. A
recovered instance can be added through a new review without changing the shared
URL or replica count. When no healthy backend remains, review is blocked and the
existing native file is preserved. Missing resources, foreign ownership and
incomplete replica bindings remain blocking errors.

This is manual, reviewed membership management. Native proxy health checks run
independently of OAP; this observation does not guarantee backend availability
between inspection and publication. No automatic routing writes, connection
draining, deployment replacement ordering or zero-downtime guarantee is added.
An already applied receipt records its original outcome; retrying it does not
reapply membership or claim current health. Old prepared reviews must be recreated
because route authority now includes health observations.

Reproducible system tests cover unhealthy and stopped exclusions, recovery,
health drift before apply, empty healthy sets preserving native bytes and
ownership checks on excluded instances. Browser tests verify the warning,
removal diff and healthy count.
