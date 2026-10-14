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

## Packaged ingress reference

[Separate proxy reference](ingress-footprint-reference.json) measures the accepted
`b85c42e` runtime using the candidate M3.1 sampling harness and the documented
packaged installer/owner-grant/agent CLI flow. This is a historical reference,
not the exact-tree receipt for a later commit. Every full local gate now repeats
the profile and writes `ingress-footprint.json` alongside installation acceptance.

This profile has one app, one web instance and local-CA HTTPS. It samples OAP,
dedicated PostgreSQL and packaged Caddy separately every five seconds: 30 seconds
idle, followed by 30 seconds with five fresh verified HTTPS connections every five
seconds (30 successful requests). Actual elapsed time and sample counts are in
the report. This is light burst traffic, not a throughput, multi-app scaling or
public ACME benchmark. No additional monitoring service is installed.

| Service | Idle RSS peak | Idle CPU, percent of one core | Light HTTPS RSS peak | Light HTTPS CPU |
| --- | --- | --- | --- | --- |
| OAP | 21.39 MiB | 0.82% | 21.41 MiB | 0.76% |
| PostgreSQL | 131.40 MiB | 1.17% | 134.29 MiB | 1.34% |
| Caddy | 45.30 MiB | 0.68% | 45.36 MiB | 0.81% |

The conservative sum of idle service peaks is **198.09 MiB**. The existing
256 MiB OAP RSS and 512 MiB stack RSS goals pass in this profile, including Caddy
in the stack. Combined idle CPU is **2.67% of one core**, exceeding the unchanged
1% goal. Keep that gap in M3.7; completing measurement does not establish that the
platform has met all lightweight goals. The older ten-app profile remains a
different measurement, not a directly comparable Caddy on/off experiment.

OAP/Caddy RSS is PID 1 VmRSS. PostgreSQL remains a conservative process sum which
can count shared pages repeatedly. The report includes Docker working set and
per-image size; image sizes can overlap shared layers and are not summed as host
disk usage. CPU includes ordinary health checks and sampler exec overhead. Short
windows can miss peaks and host contention affects results. Workloads, Docker
Engine, builds and test runners are excluded from service accounting. Adapter
request counting is not instrumented in this profile and is explicitly `null`,
not zero. Missing/reset counters fail measurement completeness; exceeded goals
are recorded separately from functional test success. These figures do not claim
the same operating cost on the public VPS or on other architectures.
