# Lightweight profile and footprint measurement

Reference: one isolated Linux Docker Engine (Docker Desktop VM), 10 applications,
50 components/instances; no inference, ingress or indexed-log service. Measure the
OAP service and its dedicated PostgreSQL separately from workloads, Engine, builds
and the browser/test runner. This defines the initial lightweight profile, not
production capacity or a multi-server benchmark.

The M2.9 `dogfood` step measures idle (60 seconds), agent observation, deployment
burst, target outage and post-controller-rescue operation (30 seconds each).
Sampling is every five seconds. Agent incident requests occur every ten seconds
on selected apps; complete 50-instance health coverage is checked separately.
Ten concurrent app releases constitute the burst. The test-only Unix proxy counts
only OAP adapter requests, not sampler/host commands, and never records bodies,
headers, object IDs or credentials. It adds overhead and is not a product service.

RSS is `/proc/1/status` VmRSS for OAP and the sum of PostgreSQL process VmRSS.
PostgreSQL shared pages can be counted repeatedly; the sum is conservative and
must not be confused with unique physical memory. Cgroup working set is reported
separately. CPU uses two Docker counter samples, online CPU count and the system
counter delta, following the [Docker CLI calculation](https://github.com/docker/cli/blob/master/cli/command/container/stats_helpers.go).
The CPU percentage is relative to one core, not the whole host. Cache subtraction
for the working set follows that same implementation. Missing/reset samples are
explicit; missing counters never become zero usage. Sampler exec/SQL and normal
health checks can contribute to the measured container CPU. Short sampling can
miss transient peaks; startup/rescue latency is measured separately.

`footprint.json` records image bytes, declared host architecture/CPU/memory/Engine
version, service RSS/working set/CPU, database bytes/connections, runtime request
rate, startup, compatible update and external-rescue latency. Connection counts
include the measurement connection. Database growth reflects this finite run,
not long-term retention. Controller recovery measurements are after the rescue;
latency measures the rescue invocation to readiness, excluding the preceding
diagnostic interval; transient restart peaks may be missed.

## Reference measurement

[Machine-readable reference](footprint-reference.json) was collected by the M2.9
candidate harness using the shipped M2.8 runtime (`1664ceb`) in the isolated Engine.
Each final exact-tree gate generates a new report; the reference is historical
measurement evidence, not the receipt for a later source tree.

| Item | Reference |
| --- | --- |
| Engine / architecture | Docker 29.2.1 / aarch64, 12 CPUs, about 7.65 GiB visible memory |
| Profile | 10 apps, 50 components, 50 instances |
| OAP image | 66.45 MiB |
| Idle OAP RSS peak | 20.64 MiB |
| Idle summed PostgreSQL RSS peak | 205.78 MiB (shared pages may be counted repeatedly) |
| Idle OAP / PostgreSQL CPU | 0.72% / 1.14% of one core |
| Idle runtime adapter requests | 0 per second |
| Observation runtime adapter requests | about 0.50 per second |
| Deployment-burst runtime adapter requests | about 10.94 per second |
| Startup / compatible update / external rescue | 8.25s / 5.98s / 5.87s |
| Metadata database / connections after idle | about 9.82 MiB / 7, including measurement |

Provisional goals remain <=256 MiB service RSS, <=512 MiB service plus dedicated
PostgreSQL RSS at idle, and <=1% of one CPU core average combined idle usage. The
reference meets both memory comparisons and **misses the combined idle CPU goal**
(about 1.86%). The goal is unchanged. Retain this measured gap for M3 one-person
operability; do not claim the CPU target is met, silently raise it, or introduce a
monitoring stack to benchmark. Functional M2 exit and measured operating cost are
separate from a universal performance guarantee.
