# Security model

Who can do what in OAP, how it's enforced, and what it doesn't protect
against yet. The design is in
[the architecture](architecture/agent-native-rewrite.md#8-identity-and-access);
the pre-release review that checked it is R5.8 in
[the rewrite plan](architecture/rewrite-plan.md#status).

## Actors

| Actor | Holds | Can |
|---|---|---|
| **Owner** | An owner key on their machine (`oapctl owner enroll`), which signs them in over HTTPS | Everything: approve and revoke agents, answer asks, set secrets, set house rules and the envelope, install extensions |
| **Agent** | A session from device login, stored on its own machine | Observe, and propose and apply changes only to the applications and change kinds the owner approved it for, within the envelope |
| **Extension** | The grants it was installed with | Only what its grants allow: a setting, a secret, Docker's socket |
| **OAP during onboarding** | Its own SSH key, until hand-off | Run the read-only check, the fixes the owner approved, and install |

## What protects what

- **The root password is typed once, by the owner, into `ssh`.** It installs
  OAP's SSH key and is never stored, logged or shown to an agent.
- **Host changes are approved and recorded.** Every fix shows its exact
  commands and runs only when the owner approves it; each is recorded in
  `/var/lib/oap/host-manifest.jsonl`, so uninstall reverses exactly what OAP
  did. OAP never changes containers, services or files it didn't create.
- **OAP's host access ends at hand-off.** Its SSH key is removed and checked
  to be refused. Break-glass access is temporary (1 to 24 hours) and the
  owner starts it.
- **Agents never hold long-lived credentials.** Device login gives each
  agent a scoped session the owner approved. oapd keeps only a hash of
  each token; the token itself stays on the agent's machine, in files
  only its user can read.
- **The envelope is enforced by oapd**, not by the agent: refused changes
  are refused at the API, whichever interface the agent uses. Sensitive
  changes wait for the owner. By default these are domains, routes,
  volumes, resources, secret bindings, connections, host ports, removed
  components, extensions and anything OAP can't classify; image, instance,
  environment and health changes are routine. The owner's envelope can
  change any tier.
- **Agents never see secret values.** The owner stores them
  (`oapctl owner secret set`); oapd passes them to the containers whose
  spec references them. Agents see names only.
- **Pause and revoke take effect at once.**
- **Extensions run out of process**, with memory caps, as `nobody` unless
  granted Docker's socket. A crashing or hanging extension fails only its
  own calls.
- **The public listener is bounded**: logins anyone can start (16 waiting at
  most), request bodies (4 MiB), idle connections and header sizes.
- **Releases are pinned.** `install.sh` installs `oapctl` only if it matches
  the release's `SHA256SUMS`; `oapctl` installs the oapd image by digest.

## Known limitations

- Releases aren't signed. `SHA256SUMS` catches a corrupted or swapped
  download, not a compromised release.
- An extension granted Docker's socket (the builders) is root on the host
  in effect. Grant it only to extensions you trust.
- Extensions' network grants are recorded, not enforced.
- An agent refused for an application outside its scope learns that the
  application exists, from the refusal.
- Only logins are rate-limited on the public listener; a signed-in agent's
  calls aren't.
- After hand-off the server may still accept SSH password logins, including
  for root. OAP reports this and leaves it to you: once your own SSH key
  works, set `PasswordAuthentication no` and
  `PermitRootLogin prohibit-password` in `/etc/ssh/sshd_config`.

Report a security problem privately to the maintainer, not in a public
issue.
