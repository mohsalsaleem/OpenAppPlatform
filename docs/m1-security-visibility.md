# M1 security and visibility baseline

New OAP-owned standalone Docker containers default to `no-new-privileges:true`,
non-privileged mode, isolated application networks, named volumes and the existing
resource limits. Image users are preserved; this baseline does not claim every
image is non-root or scanned. Existing/linked resources inherit their operator
configuration and are not retrofitted or restarted to impose new flags.

A supported owned-Docker definition may carry a reviewed `security` exception:
`allowPrivilegeEscalation: true` requires a bounded reason and a human owner
session when identity is enforced. Agents/operators cannot create or edit such an
exception. It disables no-new-privileges for newly created containers; it does not
grant privileged mode, arbitrary host binds or host networking. Reasons are public
configuration/audit context, never a place for secrets. Coolify and unsupported
adapters retain native security controls and reject OAP security overrides.

Image references and exact supported artifacts are retained in release history.
Existing digest/pull/login grants, ownership checks and rollback compatibility
checks remain in force. OAP exposes no image pruning/deletion operation, protecting
its active/rollback/retained references from OAP-driven cleanup. Native operator or
manual host cleanup remains outside this guarantee. Image scanning, signing and
richer architecture/provenance policy are future capabilities, not claimed here.

Application Activity → Diagnostic Context uses the same bounded app-scoped API as
agents (`GET /api/v1/applications/{id}/diagnostics`). It includes the latest two
recorded release summaries, exact recorded commit where available, image references
and redacted definition changes. It excludes logs, runtime/environment values and
secret-provider values; requested tags are visibly different from digest references.
This is deterministic context for inspection, not model inference or fresh runtime
health. Existing health/log/recovery controls remain separate.

The PostgreSQL notification outbox records each release outcome once per
release/state, atomically with the journal update. Attention, failure, success and
tracking-control outcomes are local events; repeated writes do not duplicate them.
Historical outcomes are not fabricated/backfilled. At most 20 local events appear
in diagnostic context. No destination, sender, delivery worker, telemetry export,
paid service or AI call is enabled. Delivery integrations and richer monitoring
remain optional later work with explicit destination/configuration and permissions.
