# Recoverable local secret store

The provider is optional. Existing environment references and native operator
secret management remain available. OAP adds no vault service or model dependency.

For packaged installs, the optional `deploy/compose.secrets.yaml` overlay provides
the read-only mount after host initialization; it is not enabled by default.

Set `OAP_SECRET_STORE_FILE` and `OAP_SECRET_KEY_FILE` to absolute container paths
on a dedicated private directory mounted read-only into the controller. Files must
be regular, private (0600 or stricter), and readable by the runtime UID (10001 in
the shipped image). The CLI writer needs a private directory (0700). Use a separate
short-lived host CLI invocation with write access; do not give the controller a
writable secret mount. Keep the directory outside public backup-report mounts.

Run `oap-admin secret-store-init` on the trusted host with those paths configured.
It preserves an existing owner-supplied raw 32-byte key, or generates one privately
when none exists. It refuses to overwrite an existing store and never prints the
key. Save a protected recovery copy of the original key before using the provider.
If initialization partially fails, retain the generated key; fix the path or
permissions rather than overwriting an existing store/key pair.

`oap-admin secret-put` reads one bounded JSON document on stdin:

```json
{"reference":"api-token-v1","targetId":"docker-local","applicationId":"<exact 32-character application ID>","component":"api","variable":"API_TOKEN","value":"<private value supplied on stdin>"}
```

Use a private input file or protected pipe, never command-line values or shell
history. Grants are exact, values are 8–4096 bytes, and the store is bounded to 128
records. AES-256-GCM authenticates the versioned encrypted payload, including grant
metadata. Values and grant metadata are encrypted on disk; only owner-visible
metadata is exposed after loading. Duplicate grants across providers are rejected,
not silently overridden. Registry credentials keep their current provider.

Each controller loads an immutable startup snapshot. Restart after a reviewed
change; Refresh Status does not reload keys or values. Queued releases keep their
private input fingerprints: changed/missing values hold work before dispatch,
and recovery uses existing exact-operation and generation checks. No update is
silently pushed into native workloads. A missing/wrong/tampered store or key makes
this provider unavailable; unrelated environment references continue working.

Workspace Settings → Secrets shows readiness and exact grant metadata, never
plaintext values, keys or ciphertext. There is no secret upload/read-value API or
agent database access. The host filesystem/key holder is the trust boundary;
encryption at rest does not protect against a compromised running controller.

For recovery, save the encrypted store and original key separately with private
permissions and a protected off-host copy. OAP metadata backups do not include
these files. Test the exact pair in an isolated installation, including wrong and
missing keys, before relying on it. Do not generate a replacement key for an
existing store. A crashed writer can leave `<store>.lock`: stop other writers,
inspect the host, and remove only that stale lock manually before retrying. No
rotation, key deletion, external vault provisioning or automatic stale-lock cleanup
is implemented in this baseline.
