# Operator-first implementation audit

Reviewed against the repository on 10 October 2026, after hosted staging release
verification. This is a product/architecture correction and delivery plan; it does
not change running operator resources or transfer deployment ownership.

## Product contract

Open App Platform unifies applications already running on an owner's operator.
The primary journey is configure/connect operator → discover → select resources →
review application/environment mappings → group → inspect health, releases and logs.
Existing native builds, GitHub Apps/webhooks, domains, volumes, secrets and deployments
keep operating. Grouping adds OAP metadata and permissions; it is not migration.

Use verified operator capabilities first. Fill missing application-level behavior
with OAP coordination or an explicitly selected optional adapter. A separate builder,
registry, repository webhook, router or host agent is not required merely to group
an existing application. Bare Docker remains first-class for owners without an operator.

## What the code does today

| Area | Implemented behavior | Remaining gap |
| --- | --- | --- |
| Operator connection | Server-side target JSON and environment secret references; Coolify and direct Docker factories | Guided connection/validation and safe configuration UX; no target credential-entry UI |
| Discovery/grouping | Scoped Coolify image/source discovery, Select → Review → Create observed application, atomic resource reservations | Main empty state still leads with creating a new application; grouping is under Deployment targets |
| Existing workloads | Exact resource-bound health and bounded logs; no release dispatch when grouping | Databases, workers/jobs, dependency topology and all other operator resource kinds are not imported |
| Native Coolify source workflow | `coolify-github-app` hook mode observes exact-commit GitHub App deployments with an app-scoped read credential; source components stay observed | Manual mapping/configuration; no native source lifecycle handoff or native image-artifact extraction/reuse |
| Lifecycle management | Standard image releases and supported restart/configuration; explicit image-only Coolify handoff with version/drift checks | No operation-by-operation authority model; source-backed grouped components cannot deploy/restart through OAP |
| Optional build lane | Trusted command/private HTTP host builder; signed GitHub intake, exact-commit digest releases, receipt reuse and scoped audit; real staging verification | This is a selected fallback, not the operator-first default or proof that native Coolify builds are OAP-controlled |
| Application environments | Each current manifest has one `environment` and one `targetId` | Batch 3: logical application with multiple environment mappings, preserving IDs/history |
| Log search/monitoring | Live health, bounded per-instance operator logs and source activity | VictoriaLogs native scoped indexing/search is planned; no collector/index backend is installed |
| Other operators | Interface and architecture candidates | Dokploy, Dokku and Portainer adapters are not implemented |

Evidence inspected:

- [Operator interfaces](../internal/operator/operator.go): current runtime capabilities;
  `NativeSourceObserver` explicitly observes rather than starts builds/deployments.
- [Coolify adapter](../internal/operator/coolify/client.go) and
  [native source observation](../internal/operator/coolify/source.go).
- [Grouping/handoff](../internal/controller/assembly.go) and
  [source modes/worker](../internal/controller/source.go).
- [Target UI](../web/src/features/targets/Targets.tsx),
  [application entry](../web/src/features/applications/Applications.tsx), and
  [source activity](../web/src/features/applications/SourceEvents.tsx).
- Existing assembly tests verify reservations, health/logs and mutation denial;
  native source tests verify observation without deploy or implicit handoff.
- [Live verification](verification.md), [GitHub evidence](github-releases.md) and
  [server builder evidence](server-build-deploy.md) retain the actual staging results.

## Corrections to the previous direction

The architecture draft incorrectly said adoption disables operator automatic
build/deployment. Remove that rule. Observed resources keep their current operator
workflow. A deliberate workflow change needs an explicit owner-selected authority
transition, resource/trigger inspection, drift checks and rollback instructions.
Grouping itself cannot perform that transition.

The private builder and signed staging branch are useful optional capability
fixtures. They do not establish a requirement for Coolify users to install a second
build system, publish a new image repository or replace existing webhooks. Keep
this fixture and its evidence; do not delete or disable it as a side effect of the
product correction. Label onboarding/docs accordingly.

Build and execution are distinct capabilities that one operator may provide.
Native Coolify build behavior has been verified. The current observer does not
expose an OAP-owned native build API or retrievable immutable build artifact.
Do not advertise native replica artifact reuse until an adapter proves it. Existing
native deployments remain useful and observable without that capability.

## Planned action-authority contract

Specify actor and capability separately for each action: source-event sequencing,
build, deploy, restart, configuration, routing and retirement. Source subscriptions
can observe operator-owned activity without authorizing mutation. If the operator
owns automatic deployment, OAP must not issue a parallel automatic deployment for
the same event. Explicit owner-triggered native commands are a future capability,
not permission to bypass today's observe-only boundary.

Every operation plan should show who executes it, which provider resource is used,
what changes and which capability is missing. An operator can execute native build
and deployment while OAP supplies grouping, authorization, status and selected
coordination. Capability absence produces a clear limit or owner-selected fallback;
never provision replacement infrastructure silently.

## Ordered roadmap follow-through

Keep M0 batch 3 active. Its acceptance must exercise existing Coolify resources:

1. Put existing-service grouping first in onboarding/application empty states.
   Show configured target validation and preserve an honest manual setup path
   until safe connection/secrets UX exists; never pretend configuration is automatic.
2. Organize selected native image/source resources into one logical application
   with staging/production mappings. Preserve resource IDs, existing app/release IDs,
   native GitHub ownership, routes, volumes and secret references through migration.
3. Show per-component source/workflow ownership and capabilities. A grouped native
   source application remains usable for health/logs/activity without a new builder.
4. Extend later release/recovery and workload batches with operation-specific native
   delegation only after scope, authority/drift and unsupported-action tests exist.
5. M3 adds validated native-build contracts and optional fallback build providers,
   plus additional operator adapters. M4 adds the planned lightweight indexed search.

Required staged acceptance: group existing image and GitHub App source services
without changing provider configuration or creating replacement resources; observe
an operator-owned native deployment and exact commit; preserve endpoints/history
through application/environment organization; reject unauthorized/unsupported
commands; show external changes as drift rather than silently overwriting them.
Connection/grouping must succeed with no custom builder or registry configured.
Native management transfer is not claimed until separately implemented and verified.
