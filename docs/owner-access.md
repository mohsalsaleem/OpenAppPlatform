# Owner access and workspace boundaries

Open App Platform now defaults to owner authentication. This is a **single self-hosted workspace**, not a multi-tenant hosting service. Migration 006 assigns existing targets, applications, bindings and releases to `default`, preserving IDs and history. Workspace foreign keys and single-workspace checks prohibit a second tenant from being silently added to global tables. The API rejects any other `X-OAP-Workspace` value. Multiple workspaces require a future explicit isolation migration and scoped queries.

## First-run setup and sign-in

Set `DATABASE_URL` and a private `OAP_SETUP_TOKEN` of at least 24 characters in the installation environment. Open the UI, enter your name, email, password and setup secret. The setup secret is sent only in the `Authorization: Setup …` header. Setup is serialized and permits exactly one owner. There is no public signup; subsequent accounts use owner-issued invitations pinned to an email and role, expiring after 48 hours. Invitations are accepted once.

Passwords must contain 12–256 bytes. They use the Go standard library's PBKDF2-HMAC-SHA256, 600,000 iterations, with random 16-byte salts. This keeps the stack to Go and PostgreSQL; the work factor follows [OWASP's PBKDF2 guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html). It does not imply FIPS certification. Credentials and invitation codes are random opaque 256-bit values; only SHA-256 hashes are stored.

Browser sessions use a __Host-prefixed cookie when secure cookies are enabled (plain oap-session on local HTTP), with HttpOnly, SameSite=Strict cookies and expire after 24 hours. Secure cookies are enabled by default. Use HTTPS for hosted installations. `OAP_COOKIE_SECURE=false` is allowed only with a loopback bind for local HTTP development. Cookie-authenticated writes require `X-OAP-CSRF: 1`; browser requests are checked against the request host and expected scheme. No cross-origin API access is enabled. Public account writes require the same header and origin checks. Login/setup/invite attempts are rate limited in PostgreSQL by the direct peer address (10/minute); behind a proxy this intentionally shares the bucket, without trusting spoofable forwarding headers.

Sign-out revokes the server credential. Disabled accounts lose access and their credentials are revoked. Password recovery revokes all owner sessions and agent credentials. An expired session returns the UI to sign-in.

## Roles and agents

- **Owner:** applications plus invitations, member roles/disablement, agent credentials and audit. The owner cannot be demoted or disabled through the membership API.
- **Operator:** read and manage applications, including supported deployment/restart/recovery and configuration actions. No access administration.
- **Viewer:** read applications, targets, health and logs. Writes are denied server-side; lifecycle controls are disabled/hidden in the UI.

Owners issue named agent tokens for one application, with `read` or `operate` scope and a 30-day expiry. Tokens are shown once and can be revoked. `operate` includes the existing supported application mutations, including scale-down and explicit management handoff; it does not grant access administration or unrestricted target discovery. Application-scoped credentials cannot list all applications/targets or inspect another app's deployments/logs. MCP clients can use `get_application` for the permitted app; global enumeration tools are intentionally denied for scoped credentials.

Membership and token status are loaded on every request. Releases record the initiating credential in the same enqueue transaction. Owner-mode workers recheck its expiry, revocation, role and app scope at each release step and before preparation/dispatch. Revoked queued operations move to attention without dispatch. Legacy queued releases without a credential are also held for owner recovery; completed snapshots remain readable. Recovery can attach an authorized current credential while preserving the existing exact-provider-operation checks.

Mutations record an audit intent before executing; failure to write the intent blocks the action. Status is recorded after handling. If completion logging is interrupted, the durable intent remains pending. Permission denials from known credentials are recorded. Audit excludes bodies, passwords, tokens and operator secrets; owners can inspect the latest 100 entries. This is a baseline, not an external tamper-proof audit system.

## Recovery and preview compatibility

If the owner loses their password, run the installed `oap-admin reset-owner-password` command with the new password on stdin and the installation's `DATABASE_URL`. The binary is included in the application container. Keep the password out of command arguments and shell history. This host-side action updates the password, revokes owner credentials, and records recovery. It requires database access; no unauthenticated password-reset endpoint exists.

`OAP_AUTH_MODE=preview` retains the old shared-token flow solely for isolated local fixtures, requires a loopback bind, and refuses an installation that already has an owner. In owner mode, `OAP_API_TOKEN` is ignored by the platform; the MCP client's variable of that name holds a scoped agent token instead. The test runner explicitly selects preview for regression tests, stops that controller, then runs a separate owner-mode browser sequence. These modes never run concurrent workers against the same installation.

Targets remain configured on the server. This slice does not add email delivery, password reset by email, MFA, SSO, multiple workspaces, database RLS, or direct database access for agents. Operator/database credentials stay private to the trusted host. Existing local pre-push verification and disabled GitHub CI remain in place.
