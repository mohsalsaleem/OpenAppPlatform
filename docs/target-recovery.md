# Target connection evidence and recovery

Deployment Targets shows the latest recorded discovery check independently of workload health. The controller performs one read-only adapter discovery per explicit check, with a 10-second probe deadline and a 15-second HTTP deadline. It does not schedule background probes, retry failed deployments or change native configuration.

- **Unknown:** no observation for the current target configuration.
- **Connected:** discovery succeeded, including an empty operator scope. This does not mean every workload is healthy.
- **Unreachable:** the adapter transport failed or the probe deadline expired.
- **Failed:** configuration, credentials, scope or the operator response prevented a valid check. Raw upstream errors and bodies are excluded.
- **Stale:** the last observation expired after two minutes. Its recorded state and time remain available; it is not live evidence.

The PostgreSQL record survives controller restart. The full logical target configuration and credential reference are fingerprinted; a changed configuration invalidates the old record. Credential values are never stored in the observation or fingerprint. Checks started earlier cannot overwrite a newer check, and a scope change during discovery rejects publication of its result. A cancelled request does not manufacture a successful observation.

The UI refreshes recorded metadata every 30 seconds while visible, without contacting the operator. It also honors the recorded expiry locally, including an expired cached response, so an API refresh failure does not keep expired evidence labeled Connected. Operators/owners can Verify Connection. Viewers can read the record; application-scoped agents cannot inspect global target state. Existing GET connection verification remains compatible, but only the new POST connection-checks path records this journal and receives the normal mutation audit intent.

A target card links up to 20 recently held operations to the exact application Activity entry. A successful recheck leaves those operations held. Review the existing recovery controls, current generation, resource identity and exact provider operation before resuming. Uncertain dispatch is never treated as permission to deploy again. Existing durable retries, worker deadlines and revocation checks remain in force; the connection journal is not a scheduler or an automatic restore/reconciliation system.

Reproducible tests cover target transport failure, reachable rejection, empty discovery, expiry, configuration changes, tighter caller deadlines, persistence across controller restart, redaction and a held operation remaining unchanged after recheck. Browser tests cover outage visibility after navigation, manual recheck, stale evidence and exact-operation review links.
