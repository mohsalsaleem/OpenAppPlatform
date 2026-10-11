# Domain and TLS readiness: configured Coolify endpoints

M0 batch 5 adds a saved, human-owner verification for an existing configured shared
endpoint. **Membership Current** describes native configuration; it does not imply
DNS correctness, certificate validity or an HTTP readiness response. The Endpoints
tab now presents these separately under **Domain & TLS**.

## Trusted configuration

Extend the existing `OAP_NATIVE_ROUTES` binding with `expectedAddresses`, containing
one to eight unique IP addresses controlled by the deployment administrator.
Hostname, readiness path, target and file directory retain their existing trusted
configuration. Browser/API callers supply only the configured route name and
expected application version. There is no arbitrary URL, address, port, certificate
bypass or response-body input.

```json
{
  "applicationId": "7cc13fa9d4bb51314240aabdcc7b851d",
  "component": "api",
  "targetId": "your-staging-target",
  "name": "oap-preview-7cc13fa9d4bb",
  "domain": "oap-api-preview.mohsal.dev",
  "healthPath": "/health/ready",
  "directory": "/native-routes",
  "expectedAddresses": ["192.0.2.10"]
}
```

The example IP is documentation-only. Determine actual expected addresses from
trusted server/operator configuration; do not copy a fresh DNS result into the
allowlist merely to silence a mismatch. Every returned DNS address must match an
explicit expected address. An unexpected or additional address blocks all network
requests. Missing expected-address configuration produces **Verification Not
Configured**. Private/loopback addresses require explicit administrator configuration;
they are never inferred from browser input. Reverse proxies/CDNs require their
own explicitly maintained expected-address policy; origin-server matching must
not be claimed for a CDN address.

## Verification and evidence

`POST /api/v1/applications/{id}/endpoint-verifications` accepts
`{ "name": "oap-route", "expectedVersion": 6 }`. Existing owner-session, CSRF,
workspace/app scope and mutation-audit controls apply. Agents can read saved
app-scoped evidence through `GET .../routes`; they cannot initiate probes.

Verification requires healthy owned image bindings, synchronized native membership,
no unresolved publication and no active or uncertain application work. The
application admission lock keeps configuration and release admission stable while
the check runs. An external native edit or replaced directory invalidates the
check before it is saved. No provider, DNS, certificate or route configuration is
changed by checking readiness.

DNS lookup is bounded to three seconds. A fresh HTTPS connection is pinned to
validated resolved IPs on port 443, with the configured hostname for SNI and normal
certificate-chain/hostname/validity checking. Environment HTTP proxies are not
used. TLS 1.2 is the minimum. The configured readiness path must return HTTP 200;
redirects are reported and never followed. The overall probe is bounded to ten
seconds. Response bodies are discarded with a bounded read; headers, cookies and
credentials are not retained or sent.

Saved evidence includes DNS addresses, TLS status, public certificate issuer,
fingerprint/expiry, HTTP status, latency and check time, plus opaque configuration,
native-file and directory fingerprints. It is stored in PostgreSQL migration 013,
so it survives an OAP restart. It becomes **Check Expired or Configuration Changed**
after ten minutes, certificate expiry, or changed authority/native bytes/directory.
A certificate expiring within seven days carries a warning. A failed verification
remains a recorded result and never silently becomes a successful current check.

The check observes one readiness request through the shared endpoint. It does not
prove every replica received a request, application correctness, ownership of a
DNS zone, a continuously healthy endpoint or future availability. Native TLS
issuance/renewal remains the operator's responsibility.

## Acceptance and remaining batch 5 work

The reproducible local gate includes real TLS fixtures for trusted certificates,
wrong hostname, expiry and unknown CA; DNS mismatch/private-address injection;
redirect refusal and non-200 readiness; persisted evidence and staleness; owner
session/agent denial and audit; and the browser verification/failure journey.
The retained staging custom hostname is checked against trusted native server
identity before enabling expected-address verification.

Domain creation/changes, DNS-provider automation, automatic membership reconciliation,
connection draining and bare Docker shared proxy routing remain separate batch 5
gates. This readiness milestone does not complete the entire batch.
